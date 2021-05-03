package api

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/go-cmp/cmp"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/sets"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// Step is a self-contained bit of work that the
// build pipeline needs to do.
type Step interface {
	Inputs() (InputDefinition, error)
	// Validate checks inputs of steps that are part of the execution graph.
	Validate() error
	Run(ctx context.Context) error

	// Name is the name of the stage, used to target it.
	// If this is the empty string the stage cannot be targeted.
	Name() string
	// Description is a short, human readable description of this step.
	Description() string
	Requires() []StepLink
	Creates() []StepLink
	Provides() ParameterMap
	// Objects returns all objects the client for this step has seen
	Objects() []ctrlruntimeclient.Object
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
	// UnsatisfiableError returns a human-understandable explanation
	// of where exactly in the config the requirement came from and
	// what needs to be done to satisfy it. It must be checked for
	// emptyness and only be used when non-empty.
	UnsatisfiableError() string
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

func (l *internalImageStreamLink) UnsatisfiableError() string {
	return ""
}

// internalImageStreamTagLink describes a specific tag in
// an ImageStream in the test's namespace
type internalImageStreamTagLink struct {
	name, tag, unsatisfiableError string
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

func (l *internalImageStreamTagLink) UnsatisfiableError() string {
	return l.unsatisfiableError
}

func AllStepsLink() StepLink {
	return allStepsLink{}
}

type allStepsLink struct{}

func (_ allStepsLink) SatisfiedBy(_ StepLink) bool {
	return true
}

func (_ allStepsLink) UnsatisfiableError() string {
	return ""
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

func (l *externalImageLink) UnsatisfiableError() string {
	return ""
}

type StepLinkOptions struct {
	// UnsatisfiableError holds a human-understandable explanation
	// of where exactly in the config the requirement came from and
	// what needs to be done to satisfy it.
	UnsatisfiableError string
}

type StepLinkOption func(*StepLinkOptions)

func StepLinkWithUnsatisfiableErrorMessage(msg string) StepLinkOption {
	return func(slo *StepLinkOptions) {
		slo.UnsatisfiableError = msg
	}
}

// InternalImageLink describes a dependency on a tag in the pipeline stream
func InternalImageLink(tag PipelineImageStreamTagReference, o ...StepLinkOption) StepLink {
	opts := StepLinkOptions{}
	for _, o := range o {
		o(&opts)
	}
	return &internalImageStreamTagLink{
		name:               PipelineImageStream,
		tag:                string(tag),
		unsatisfiableError: opts.UnsatisfiableError,
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

func (l *imagesReadyLink) UnsatisfiableError() string {
	return ""
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

func (l *rpmRepoLink) UnsatisfiableError() string {
	return ""
}

// ReleaseImagesLink describes the content of a stable(-foo)?
// ImageStream in the test namespace.
func ReleaseImagesLink(name string) StepLink {
	return &internalImageStreamLink{
		name: ReleaseStreamFor(name),
	}
}

// ReleaseImageTagLink describes a specific tag in a stable(-foo)?
// ImageStream in the test namespace.
func ReleaseImageTagLink(name, tag string) StepLink {
	return &internalImageStreamTagLink{
		name: ReleaseStreamFor(name),
		tag:  tag,
	}
}

func Comparer() cmp.Option {
	return cmp.AllowUnexported(
		internalImageStreamLink{},
		internalImageStreamTagLink{},
		externalImageLink{},
	)
}

// ReleaseStreamFor determines the ImageStream into which a named
// release will be imported or assembled.
func ReleaseStreamFor(name string) string {
	if name == LatestReleaseName {
		return StableImageStream
	}

	return fmt.Sprintf("%s-%s", StableImageStream, name)
}

// ReleaseNameFrom determines the named release that was imported
// or assembled into an ImageStream.
func ReleaseNameFrom(stream string) string {
	if stream == StableImageStream {
		return LatestReleaseName
	}

	return strings.TrimPrefix(stream, fmt.Sprintf("%s-", StableImageStream))
}

// IsReleaseStream determines if the ImageStream was created from
// an import or assembly of a release.
func IsReleaseStream(stream string) bool {
	return strings.HasPrefix(stream, StableImageStream)
}

// IsReleasePayloadStream determines if the ImageStream holds
// release paylaod images.
func IsReleasePayloadStream(stream string) bool {
	return stream == ReleaseImageStream
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

// ValidateGraph performs validations on each step in the graph once.
func ValidateGraph(nodes []*StepNode) []error {
	var errs []error
	IterateAllEdges(nodes, func(n *StepNode) {
		if err := n.Step.Validate(); err != nil {
			errs = append(errs, fmt.Errorf("step %q failed validation: %w", n.Step.Name(), err))
		}
	})
	return errs
}

// IterateAllEdges applies an operation to every node in the graph once.
func IterateAllEdges(nodes []*StepNode, f func(*StepNode)) {
	iterateAllEdges(nodes, sets.NewString(), f)
}

func iterateAllEdges(nodes []*StepNode, alreadyIterated sets.String, f func(*StepNode)) {
	for _, node := range nodes {
		if alreadyIterated.Has(node.Step.Name()) {
			continue
		}
		iterateAllEdges(node.Children, alreadyIterated, f)
		if alreadyIterated.Has(node.Step.Name()) {
			continue
		}
		f(node)
		alreadyIterated.Insert(node.Step.Name())
	}
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

type CIOperatorStepGraph []CIOperatorStepDetails

// MergeFrom merges two CIOperatorStepGraphs together using StepNames as merge keys.
// The merging logic will never ovewrwrite data and only set unset fields.
// Steps that do not exist in the first graph get appended.
func (graph *CIOperatorStepGraph) MergeFrom(from ...CIOperatorStepDetails) {
	for _, step := range from {
		var found bool
		for idx, existing := range *graph {
			if step.StepName != existing.StepName {
				continue
			}
			found = true
			(*graph)[idx] = mergeSteps(existing, step)
		}
		if !found {
			*graph = append(*graph, step)
		}
	}

}

func mergeSteps(into, from CIOperatorStepDetails) CIOperatorStepDetails {
	if into.Description == "" {
		into.Description = from.Description
	}
	if into.Dependencies == nil {
		into.Dependencies = from.Dependencies
	}
	if into.StartedAt == nil {
		into.StartedAt = from.StartedAt
	}
	if into.StartedAt == nil {
		into.StartedAt = from.StartedAt
	}
	if into.FinishedAt == nil {
		into.FinishedAt = from.FinishedAt
	}
	if into.Duration == nil {
		into.Duration = from.Duration
	}
	if into.Manifests == nil {
		into.Manifests = from.Manifests
	}
	if into.LogURL == "" {
		into.LogURL = from.LogURL
	}
	if into.Failed == nil {
		into.Failed = from.Failed
	}
	if into.Substeps == nil {
		into.Substeps = from.Substeps
	}

	return into
}

type CIOperatorStepDetails struct {
	CIOperatorStepDetailInfo `json:",inline"`
	Substeps                 []CIOperatorStepDetailInfo `json:"substeps,omitempty"`
}

type CIOperatorStepDetailInfo struct {
	StepName     string                     `json:"name"`
	Description  string                     `json:"description"`
	Dependencies []string                   `json:"dependencies"`
	StartedAt    *time.Time                 `json:"started_at"`
	FinishedAt   *time.Time                 `json:"finished_at"`
	Duration     *time.Duration             `json:"duration,omitempty"`
	Manifests    []ctrlruntimeclient.Object `json:"manifests,omitempty"`
	LogURL       string                     `json:"log_url,omitempty"`
	Failed       *bool                      `json:"failed,omitempty"`
}

func (c *CIOperatorStepDetailInfo) UnmarshalJSON(data []byte) error {
	raw := map[string]interface{}{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	manifests := []*unstructured.Unstructured{}
	if rawManifests, ok := raw["manifests"]; ok {
		serializedManifests, err := json.Marshal(rawManifests)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(serializedManifests, &manifests); err != nil {
			return err
		}
		delete(raw, "manifests")
	}
	reserializedWithoutManifests, err := json.Marshal(raw)
	if err != nil {
		return err
	}

	type silbling CIOperatorStepDetailInfo
	var unmarshalTo silbling
	if err := json.Unmarshal(reserializedWithoutManifests, &unmarshalTo); err != nil {
		return err
	}
	*c = CIOperatorStepDetailInfo(unmarshalTo)
	c.Manifests = nil
	for _, manifest := range manifests {
		c.Manifests = append(c.Manifests, manifest)
	}
	return nil

}

const CIOperatorStepGraphJSONFilename = "ci-operator-step-graph.json"

// StepGraphJSONURL takes a base url like https://storage.googleapis.com/origin-ci-test/pr-logs/pull/openshift_ci-tools/999/pull-ci-openshift-ci-tools-master-validate-vendor/1283812971092381696
// and returns the full url for the step graph json document.
func StepGraphJSONURL(baseJobURL string) string {
	return strings.Join([]string{baseJobURL, "artifacts", CIOperatorStepGraphJSONFilename}, "/")
}

// LinkForImage determines what dependent link is required
// for the user's image dependency
func LinkForImage(imageStream, tag string) StepLink {
	switch {
	case imageStream == PipelineImageStream:
		// the user needs an image we're building
		return InternalImageLink(PipelineImageStreamTagReference(tag))
	case IsReleaseStream(imageStream):
		// the user needs a tag that's a component of some release;
		// we cant' rely on a specific tag, as they are implicit in
		// the import process and won't be present in the build graph,
		// so we wait for the whole import to succeed
		return ReleaseImagesLink(ReleaseNameFrom(imageStream))
	case IsReleasePayloadStream(imageStream):
		// the user needs a release payload
		return ReleasePayloadImageLink(tag)
	default:
		// we have no idea what the user's configured
		return nil
	}
}
