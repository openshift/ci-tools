package api

import (
	"context"
	"fmt"
	"strings"
)

// Step is a self-contained bit of work that the
// build pipeline needs to do.
type Step interface {
	Inputs(dry bool) (InputDefinition, error)
	Run(ctx context.Context, dry bool) error

	// Name is the name of the stage, used to target it.
	// If this is the empty string the stage cannot be targeted.
	Name() string
	// Description is a short, human readable description of this step.
	Description() string
	Requires() []StepLink
	Creates() []StepLink
	Provides() (ParameterMap, StepLink)
}

type InputDefinition []string

type ParameterMap map[string]func() (string, error)

// StepLink abstracts the types of links that steps
// require and create.
type StepLink interface {
	// SatisfiedBy determines if the other link satisfies
	// the requirements of this one, either partially or
	// fully. If so, the other step will be executed first.
	SatisfiedBy(other StepLink) bool
}

// internalImageStreamLink describes all tags in
// an ImageStream in the test's namespace
type internalImageStreamLink struct {
	name string
}

func (l *internalImageStreamLink) SatisfiedBy(other StepLink) bool {
	// an ImageStream in an internal namespace may only
	// be provided by a literal link for that stream
	switch link := other.(type) {
	case *internalImageStreamLink:
		return l.name == link.name
	default:
		return false
	}
}

// internalImageStreamTagLink describes a specific tag in
// an ImageStream in the test's namespace
type internalImageStreamTagLink struct {
	name, tag string
}

func (l *internalImageStreamTagLink) SatisfiedBy(other StepLink) bool {
	// an ImageStreamTag in an internal namespace may
	// either be provided by a literal link for that tag
	// or by a link that provides the full stream
	switch link := other.(type) {
	case *internalImageStreamTagLink:
		return l.name == link.name && l.tag == link.tag
	case *internalImageStreamLink:
		return l.name == link.name
	default:
		return false
	}
}

func AllStepsLink() StepLink {
	return allStepsLink{}
}

type allStepsLink struct{}

func (_ allStepsLink) SatisfiedBy(_ StepLink) bool {
	return true
}

func ExternalImageLink(ref ImageStreamTagReference) StepLink {
	return &externalImageLink{
		namespace: ref.Namespace,
		name:      ref.Name,
		tag:       ref.Tag,
	}
}

type externalImageLink struct {
	namespace, name, tag string
}

func (l *externalImageLink) SatisfiedBy(other StepLink) bool {
	switch link := other.(type) {
	case *externalImageLink:
		return l.name == link.name &&
			l.namespace == link.namespace &&
			l.tag == link.tag
	default:
		return false
	}
}

// InternalImageLink describes a dependency on a tag in the pipeline stream
func InternalImageLink(tag PipelineImageStreamTagReference) StepLink {
	return &internalImageStreamTagLink{
		name: PipelineImageStream,
		tag:  string(tag),
	}
}

func ReleasePayloadImageLink(tag string) StepLink {
	return &internalImageStreamTagLink{
		name: ReleaseImageStream,
		tag:  tag,
	}
}

func ImagesReadyLink() StepLink {
	return &imagesReadyLink{}
}

type imagesReadyLink struct{}

func (l *imagesReadyLink) SatisfiedBy(other StepLink) bool {
	switch other.(type) {
	case *imagesReadyLink:
		return true
	default:
		return false
	}
}

func RPMRepoLink() StepLink {
	return &rpmRepoLink{}
}

type rpmRepoLink struct{}

func (l *rpmRepoLink) SatisfiedBy(other StepLink) bool {
	switch other.(type) {
	case *rpmRepoLink:
		return true
	default:
		return false
	}
}

// StableImagesLink describes the content of a stable(-foo)?
// ImageStream in the test namespace.
func StableImagesLink(name string) StepLink {
	return &internalImageStreamLink{
		name: StableStreamFor(name),
	}
}

// StableImageTagLink describes a specific tag in a stable(-foo)?
// ImageStream in the test namespace.
func StableImageTagLink(name, tag string) StepLink {
	return &internalImageStreamTagLink{
		name: StableStreamFor(name),
		tag:  tag,
	}
}

func StableStreamFor(name string) string {
	if name == LatestStableName {
		return StableImageStream
	}

	return fmt.Sprintf("%s-%s", StableImageStream, name)
}

type StepNode struct {
	Step     Step
	Children []*StepNode
}

// BuildGraph returns a graph or graphs that include
// all steps given.
func BuildGraph(steps []Step) []*StepNode {
	var allNodes []*StepNode
	for _, step := range steps {
		node := StepNode{Step: step, Children: []*StepNode{}}
		allNodes = append(allNodes, &node)
	}

	var roots []*StepNode
	for _, node := range allNodes {
		isRoot := true
		for _, other := range allNodes {
			for _, nodeRequires := range node.Step.Requires() {
				for _, otherCreates := range other.Step.Creates() {
					if nodeRequires.SatisfiedBy(otherCreates) {
						isRoot = false
						addToNode(other, node)
					}
				}
			}
		}
		if isRoot {
			roots = append(roots, node)
		}
	}

	return roots
}

// BuildPartialGraph returns a graph or graphs that include
// only the dependencies of the named steps.
func BuildPartialGraph(steps []Step, names []string) ([]*StepNode, error) {
	if len(names) == 0 {
		return BuildGraph(steps), nil
	}

	var required []StepLink
	candidates := make([]bool, len(steps))
	var allNames []string
	for i, step := range steps {
		allNames = append(allNames, step.Name())
		for j, name := range names {
			if name != step.Name() {
				continue
			}
			candidates[i] = true
			required = append(required, step.Requires()...)
			names = append(names[:j], names[j+1:]...)
			break
		}
	}
	if len(names) > 0 {
		return nil, fmt.Errorf("the following names were not found in the config or were duplicates: %s (from %s)", strings.Join(names, ", "), strings.Join(allNames, ", "))
	}

	// identify all other steps that provide any links required by the current set
	for {
		added := 0
		for i, step := range steps {
			if candidates[i] {
				continue
			}
			if HasAnyLinks(required, step.Creates()) {
				added++
				candidates[i] = true
				required = append(required, step.Requires()...)
			}
		}
		if added == 0 {
			break
		}
	}

	var targeted []Step
	for i, candidate := range candidates {
		if candidate {
			targeted = append(targeted, steps[i])
		}
	}
	return BuildGraph(targeted), nil
}

func addToNode(parent, child *StepNode) bool {
	for _, s := range parent.Children {
		if s == child {
			return false
		}
	}
	parent.Children = append(parent.Children, child)
	return true
}

func HasAnyLinks(steps, candidates []StepLink) bool {
	for _, candidate := range candidates {
		for _, step := range steps {
			if step.SatisfiedBy(candidate) {
				return true
			}
		}
	}
	return false
}

func HasAllLinks(needles, haystack []StepLink) bool {
	for _, needle := range needles {
		contains := false
		for _, hay := range haystack {
			if hay.SatisfiedBy(needle) {
				contains = true
			}
		}
		if !contains {
			return false
		}
	}
	return true
}
