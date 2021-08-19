package validation

import (
	"fmt"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/openshift/ci-tools/pkg/api"
)

// IsValidGraphConfiguration verifies the intermediary configuration is correct.
// This is the ideal place for validations since the graph configuration is the
// intermediary structure between the input configuration and the final
// execution graph.  Validations done here take advantage of all the parsing
// logic that has already been applied.
func IsValidGraphConfiguration(rawSteps []api.StepConfiguration) error {
	var ret []error
	names := sets.NewString()
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
		} else if c := s.PipelineImageCacheStepConfiguration; c != nil {
			addName(c.TargetName())
		} else if c := s.SourceStepConfiguration; c != nil {
			addName(c.TargetName())
		} else if c := s.BundleSourceStepConfiguration; c != nil {
			addName(c.TargetName())
		} else if c := s.IndexGeneratorStepConfiguration; c != nil {
			addName(c.TargetName())
		} else if c := s.ProjectDirectoryImageBuildStepConfiguration; c != nil {
			addName(c.TargetName())
		} else if c := s.RPMImageInjectionStepConfiguration; c != nil {
			addName(c.TargetName())
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
		} else if c := s.ProjectDirectoryImageBuildInputs; c != nil {
			addName(string(api.PipelineImageStreamTagReferenceRoot))
		}
	}
	return utilerrors.NewAggregate(ret)
}
