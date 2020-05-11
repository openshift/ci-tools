package helper

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"strconv"
	"testing"

	"github.com/google/gofuzz"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/load"
)

func TestGetAllImageStreamTagReturnsAllImageStreamTags(t *testing.T) {
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
			).
				// Using something else messes up the result, apparently the fuzzer sometimes overwrites the whole
				// map/slice after insering into it.
				NumElements(1, 1)

			cfg := load.ByOrgRepo{}
			f.Fuzz(&cfg)
			for _, org := range cfg {
				for _, repo := range org {
					for _, cfg := range repo {
						for _, rawStep := range cfg.RawSteps {
							// These are output ImageStreamTags
							if rawStep.OutputImageTagStepConfiguration != nil {
								rawStep.OutputImageTagStepConfiguration = nil
								numberInsertedElements--
							}
						}
					}
				}
			}

			res := GetAllTestInputImageStreamTags(cfg)
			if n := len(res); n != numberInsertedElements {
				serialized, _ := json.Marshal(cfg)
				tmpFile, err := ioutil.TempFile("", "imagestream-extration-fuzzing")
				if err != nil {
					t.Errorf("failed to create tmpfile: %v", err)
				} else if err := ioutil.WriteFile(tmpFile.Name(), serialized, 0644); err != nil {
					t.Errorf("failed to write config to disk: %v", err)
				}
				// Do _not_ print the cfg. You have been warned.
				t.Errorf("got %d results, but inserted %d. cfg written to: %s", n, numberInsertedElements, tmpFile.Name())
			}
		})
	}
}

func TestGetAllImageStreamTagsParsing(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name           string
		istr           api.ImageStreamTagReference
		expectedResult string
	}{
		{
			name:           "happy path",
			istr:           api.ImageStreamTagReference{Namespace: "foo", Name: "Baz", Tag: "Bar"},
			expectedResult: "foo/Baz:Bar",
		},
		{
			name:           "cluster field is ignored",
			istr:           api.ImageStreamTagReference{Namespace: "foo", Name: "Baz", Tag: "Bar", Cluster: "Bee"},
			expectedResult: "foo/Baz:Bar",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			config := load.ByOrgRepo{"org": {"repo": []api.ReleaseBuildConfiguration{{
				InputConfiguration: api.InputConfiguration{BaseImages: map[string]api.ImageStreamTagReference{"": tc.istr}},
			}}}}
			result := GetAllTestInputImageStreamTags(config)
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
