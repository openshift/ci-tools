package api

// Step is a self-contained bit of work that the
// build pipeline needs to do.
type Step interface {
	Run(dry bool) error
	Done() (bool, error)

	Requires() []StepLink
	Creates() []StepLink
}

// StepLink abstracts the types of links that steps
// require and create.
type StepLink interface {
	Matches(other StepLink) bool
}

func ExternalImageLink(ref ImageStreamTagReference) StepLink {
	return &externalImageLink{image: ref}
}

type externalImageLink struct {
	image ImageStreamTagReference
}

func (l *externalImageLink) Matches(other StepLink) bool {
	switch link := other.(type) {
	case *externalImageLink:
		return l.image == link.image
	default:
		return false
	}
}

func InternalImageLink(ref PipelineImageStreamTagReference) StepLink {
	return &internalImageLink{image: ref}
}

type internalImageLink struct {
	image PipelineImageStreamTagReference
}

func (l *internalImageLink) Matches(other StepLink) bool {
	switch link := other.(type) {
	case *internalImageLink:
		return l.image == link.image
	default:
		return false
	}
}

func RPMRepoLink() StepLink {
	return &rpmRepoLink{}
}

type rpmRepoLink struct{}

func (l *rpmRepoLink) Matches(other StepLink) bool {
	switch other.(type) {
	case *rpmRepoLink:
		return true
	default:
		return false
	}
}

func ReleaseImagesLink() StepLink {
	return &releaseImagesLink{}
}

type releaseImagesLink struct{}

func (l *releaseImagesLink) Matches(other StepLink) bool {
	switch other.(type) {
	case *releaseImagesLink:
		return true
	default:
		return false
	}
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
					if nodeRequires.Matches(otherCreates) {
						isRoot = false
						other.Children = append(other.Children, node)
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
