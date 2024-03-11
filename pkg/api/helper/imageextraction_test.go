package helper

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"testing"

	fuzz "github.com/google/gofuzz"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/github"
)

func TestTestInputImageStreamTagsFromResolvedConfigReturnsAllImageStreamTags(t *testing.T) {
	for i := 0; i < 100; i++ {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			t.Parallel()
			var numberInsertedElements int
			f := fuzz.New().NilChance(0.5).Funcs(
				func(istr *api.ImageStreamTagReference, c fuzz.Continue) {
					numberInsertedElements++
					// Avoid getting deduplicated
					istr.Namespace = fmt.Sprintf("ns-%d", numberInsertedElements)
					istr.Name = "name"
					istr.Tag = "tag"
				},
				// We only care about the *api.ImageStreamTagReference fields so prevent the
				// fuzzer a bit from creating unreadable output.
				func(_ *string, _ fuzz.Continue) {},
				func(_ *api.ClusterProfile, _ fuzz.Continue) {},
				// TestInputImageStreamTagsFromResolvedConfig assumes that the config is already
				// resolved and will error if thats not the case (MultiStageTestConfiguration != nil && MultiStageTestConfigurationLiteral == nil)
				func(_ **api.MultiStageTestConfiguration, _ fuzz.Continue) {},
				// Don't set build_roots, that is mutually exclusive with build_root and only set by ci-operator-configresolver when merging configs
				func(_ map[string]api.BuildRootImageConfiguration, _ fuzz.Continue) {},
			).
				// Using something else messes up the result, apparently the fuzzer sometimes overwrites the whole
				// map/slice after inserting into it.
				NumElements(1, 1)

			cfg := api.ReleaseBuildConfiguration{}
			f.Fuzz(&cfg)
			for _, rawStep := range cfg.RawSteps {
				// These are output ImageStreamTags
				if rawStep.OutputImageTagStepConfiguration != nil {
					rawStep.OutputImageTagStepConfiguration = nil
					numberInsertedElements--
				}
			}
			if cfg.InputConfiguration.BuildRootImage != nil && cfg.InputConfiguration.BuildRootImage.UseBuildCache {
				numberInsertedElements++
			}

			res, err := TestInputImageStreamTagsFromResolvedConfig(cfg, nil)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if n := len(res); n != numberInsertedElements {
				serialized, _ := json.Marshal(cfg)
				tmpFile, err := os.CreateTemp("", "imagestream-extration-fuzzing")
				if err != nil {
					t.Errorf("failed to create tmpfile: %v", err)
				} else if err := os.WriteFile(tmpFile.Name(), serialized, 0644); err != nil {
					t.Errorf("failed to write config to disk: %v", err)
				}
				// Do _not_ print the cfg. You have been warned.
				t.Errorf("got %d results, but inserted %d. cfg written to: %s", n, numberInsertedElements, tmpFile.Name())
			}
		})
	}
}

func fakeRepoFileGetter(org, repo, branch string, _ ...github.Opt) github.FileGetter {
	return func(path string) ([]byte, error) {
		return []byte(`build_root_image:
  name: boilerplate
  namespace: openshift
  tag: image-v3.0.2`), nil
	}
}

func TestTestInputImageStreamTagsFromConfigParsing(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name           string
		config         api.ReleaseBuildConfiguration
		expectedResult string
	}{
		{
			name: "happy path",
			config: api.ReleaseBuildConfiguration{
				InputConfiguration: api.InputConfiguration{BaseImages: map[string]api.ImageStreamTagReference{"": {Namespace: "foo", Name: "Baz", Tag: "Bar"}}},
			},
			expectedResult: "foo/Baz:Bar",
		},
		{
			name: "boot image from repo",
			config: api.ReleaseBuildConfiguration{
				InputConfiguration: api.InputConfiguration{
					BuildRootImage: &api.BuildRootImageConfiguration{FromRepository: true}},
			},
			expectedResult: "openshift/boilerplate:image-v3.0.2",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := TestInputImageStreamTagsFromResolvedConfig(tc.config, fakeRepoFileGetter)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if n := len(result); n != 1 {
				t.Fatalf("expected one result, got %d", n)
			}
			for _, item := range result {
				if item.String() != tc.expectedResult {
					t.Errorf("expected result %s, got result %s", tc.expectedResult, item.String())
				}
			}
		})
	}
}

func TestTestInputImageStreamTagsFromResolvedConfigErrorsOnUnresolvedConfig(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name          string
		config        api.ReleaseBuildConfiguration
		expectedError string
	}{
		{
			name:          "Unresolved, error",
			config:        api.ReleaseBuildConfiguration{Tests: []api.TestStepConfiguration{{MultiStageTestConfiguration: &api.MultiStageTestConfiguration{}}}},
			expectedError: "got unresolved config",
		},
		{
			name: "Resolved, no error",
			config: api.ReleaseBuildConfiguration{Tests: []api.TestStepConfiguration{{
				MultiStageTestConfiguration:        &api.MultiStageTestConfiguration{},
				MultiStageTestConfigurationLiteral: &api.MultiStageTestConfigurationLiteral{},
			}}},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {

			var errStr string
			_, err := TestInputImageStreamTagsFromResolvedConfig(tc.config, nil)
			if err != nil {
				errStr = err.Error()
			}

			if errStr != tc.expectedError {
				t.Errorf("expected error: %q, got error: %q", tc.expectedError, errStr)
			}
		})
	}
}
