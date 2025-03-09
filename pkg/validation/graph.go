package validation

import (
	"errors"
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
	var containerTests, multiStageTests []*api.TestStepConfiguration
	names := sets.New[string]()
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
			if s.InputImageTagStepConfiguration.ExternalPullSpec != "" {
				ret = append(ret, fmt.Errorf("it is not permissible to directly set external_pull_spec. this should only be used to programttically override an image"))
			}
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
			} else if c.MultiStageTestConfigurationLiteral != nil {
				multiStageTests = append(multiStageTests, c)
			}
		} else if c := s.ProjectDirectoryImageBuildInputs; c != nil {
			addName(string(api.PipelineImageStreamTagReferenceRoot))
			pipelineImages[api.PipelineImageStreamTagReferenceRoot] = sets.Empty{}
		}
	}
	for _, t := range containerTests {
		ret = append(ret, validateContainerTest(pipelineImages, t)...)
	}
	for _, t := range multiStageTests {
		ret = append(ret, validateMultiStageTest(pipelineImages, t)...)
	}
	return utilerrors.NewAggregate(ret)
}

func validateContainerTest(
	pipelineImages pipelineImageSet,
	s *api.TestStepConfiguration,
) (ret []error) {
	c := s.ContainerTestConfiguration
	if _, ok := pipelineImages[c.From]; !ok {
		msg := fmt.Sprintf("tests[%s].from: unknown image %q", s.As, c.From)
		if s := pipelineImageToConfigField[c.From]; s != "" {
			msg = fmt.Sprintf("%s (configuration is missing `%s`)", msg, s)
		}
		ret = append(ret, errors.New(msg))
	}
	return
}

func validateMultiStageTest(
	pipelineImages pipelineImageSet,
	s *api.TestStepConfiguration,
) (ret []error) {
	f := func(phase string, i int, step api.LiteralTestStep) (ret []error) {
		from := api.PipelineImageStreamTagReference(step.From)
		if _, ok := pipelineImages[from]; !ok {
			// `from` not being a pipeline image is not necessarily an error,
			// but a reference to a known image not present in the graph is.
			if msg := pipelineImageToConfigField[from]; msg != "" {
				ret = append(ret, fmt.Errorf("tests[%s].steps.%s[%d].from: unknown image %q (configuration is missing `%s`)", s.As, phase, i, from, msg))
			}
		}
		return
	}
	ms := s.MultiStageTestConfigurationLiteral
	for i, step := range ms.Pre {
		ret = append(ret, f("pre", i, step)...)
	}
	for i, step := range ms.Test {
		ret = append(ret, f("test", i, step)...)
	}
	for i, step := range ms.Post {
		ret = append(ret, f("post", i, step)...)
	}
	return
}

var pipelineImageToConfigField = map[api.PipelineImageStreamTagReference]string{
	api.PipelineImageStreamTagReferenceRoot:         api.PipelineImageStreamTagSourceRoot,
	api.PipelineImageStreamTagReferenceBinaries:     api.PipelineImageStreamTagSourceBinaries,
	api.PipelineImageStreamTagReferenceTestBinaries: api.PipelineImageStreamTagSourceTestBinaries,
	api.PipelineImageStreamTagReferenceRPMs:         api.PipelineImageStreamTagSourceRPMs,
}
