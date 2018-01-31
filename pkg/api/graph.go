package api

// Step is a self-contained bit of work that the
// build pipeline needs to do.
type Step interface {
	Run() error
	Done() (bool, error)

	Requires() []StepLink
	Creates() []StepLink
}

// StepLink abstracts the types of links that steps
// require and create. Only one of the fields may be
// non-nil.
type StepLink struct {
	externalImage *ImageStreamTagReference
	internalImage *PipelineImageStreamTagReference
	rpmRepo       *bool
}

func InternalImageLink(ref PipelineImageStreamTagReference) StepLink {
	return StepLink{internalImage: &ref}
}

func ExternalImageLink(ref ImageStreamTagReference) StepLink {
	return StepLink{externalImage: &ref}
}

func RPMRepoLink() StepLink {
	link := true
	return StepLink{rpmRepo: &link}
}

func (r *StepLink) Matches(other StepLink) bool {
	if r.externalImage != nil && other.externalImage != nil {
		return *r.externalImage == *other.externalImage
	}

	if r.internalImage != nil && other.internalImage != nil {
		return *r.internalImage == *other.internalImage
	}

	if r.rpmRepo != nil && other.rpmRepo != nil {
		return *r.rpmRepo == *other.rpmRepo
	}

	return false
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
