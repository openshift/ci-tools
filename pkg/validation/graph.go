package validation

import (
	"fmt"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/openshift/ci-tools/pkg/api"
)

type pipelineImageSet map[api.PipelineImageStreamTagReference]sets.Empty

// IsValidGraphConfiguration verifies the intermediary configuration is correct.
// This is the ideal place for validations since the graph configuration is the
// intermediary structure between the input configuration and the final
// execution graph.  Validations done here take advantage of all the parsing
// logic that has already been applied.
func IsValidGraphConfiguration(rawSteps []api.StepConfiguration) error {
	var ret []error
	var containerTests []*api.TestStepConfiguration
	names := sets.NewString()
	pipelineImages := pipelineImageSet{
		// `src` can only be validated at runtime
		api.PipelineImageStreamTagReferenceSource: {},
	}
	addName := func(n string) {
		if names.Has(n) {
			ret = append(ret, fmt.Errorf("configuration contains duplicate target: %s", n))
		} else {
			names.Insert(n)
		}
	}
	for _, s := range rawSteps {
		if c := s.InputImageTagStepConfiguration; c != nil {
			addName(c.TargetName())
			pipelineImages[c.To] = sets.Empty{}
		} else if c := s.PipelineImageCacheStepConfiguration; c != nil {
			addName(c.TargetName())
			pipelineImages[c.To] = sets.Empty{}
		} else if c := s.SourceStepConfiguration; c != nil {
			addName(c.TargetName())
			pipelineImages[c.To] = sets.Empty{}
		} else if c := s.BundleSourceStepConfiguration; c != nil {
			addName(c.TargetName())
			pipelineImages[api.PipelineImageStreamTagReferenceBundleSource] = sets.Empty{}
		} else if c := s.IndexGeneratorStepConfiguration; c != nil {
			addName(c.TargetName())
			pipelineImages[c.To] = sets.Empty{}
			pipelineImages[api.PipelineImageStreamTagReferenceIndexImageGenerator] = sets.Empty{}
		} else if c := s.ProjectDirectoryImageBuildStepConfiguration; c != nil {
			addName(c.TargetName())
			pipelineImages[c.To] = sets.Empty{}
		} else if c := s.RPMImageInjectionStepConfiguration; c != nil {
			addName(c.TargetName())
			pipelineImages[c.To] = sets.Empty{}
		} else if c := s.RPMServeStepConfiguration; c != nil {
			addName(c.TargetName())
		} else if c := s.OutputImageTagStepConfiguration; c != nil {
			addName(c.TargetName())
		} else if c := s.ReleaseImagesTagStepConfiguration; c != nil {
			addName(c.InputsName())
			addName(c.TargetName(api.InitialReleaseName))
			addName(c.TargetName(api.LatestReleaseName))
		} else if c := s.ResolvedReleaseImagesStepConfiguration; c != nil {
			addName(c.TargetName())
		} else if c := s.TestStepConfiguration; c != nil {
			addName(c.TargetName())
			if c.ContainerTestConfiguration != nil {
				containerTests = append(containerTests, c)
			}
		} else if c := s.ProjectDirectoryImageBuildInputs; c != nil {
			addName(string(api.PipelineImageStreamTagReferenceRoot))
			pipelineImages[api.PipelineImageStreamTagReferenceRoot] = sets.Empty{}
		}
	}
	for _, t := range containerTests {
		ret = append(ret, validateTestStepConfiguration(pipelineImages, t)...)
	}
	return utilerrors.NewAggregate(ret)
}

func validateTestStepConfiguration(
	pipelineImages pipelineImageSet,
	s *api.TestStepConfiguration,
) (ret []error) {
	c := s.ContainerTestConfiguration
	if _, ok := pipelineImages[c.From]; !ok {
		ret = append(ret, fmt.Errorf("tests[%s].from: unknown image %q", s.As, c.From))
	}
	return
}
