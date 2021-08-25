package validation

import (
	"errors"
	"fmt"
	"testing"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/defaults"
	"github.com/openshift/ci-tools/pkg/testhelper"
)

func TestIsValidGraph_Names(t *testing.T) {
	input := api.InputConfiguration{
		BaseImages: map[string]api.ImageStreamTagReference{"from": {}},
	}
	tests := func(names ...string) (ret []api.TestStepConfiguration) {
		for _, n := range names {
			ret = append(ret, api.TestStepConfiguration{
				As: n,
				ContainerTestConfiguration: &api.ContainerTestConfiguration{
					From: "from",
				},
			})
		}
		return
	}
	errs := func(msgs ...string) error {
		var ret []error
		for _, m := range msgs {
			ret = append(ret, fmt.Errorf("configuration contains duplicate target: %s", m))
		}
		return utilerrors.NewAggregate(ret)
	}
	for _, tc := range []struct {
		name     string
		config   api.ReleaseBuildConfiguration
		expected error
	}{{
		name: "valid",
	}, {
		name: "duplicate input image",
		config: api.ReleaseBuildConfiguration{
			InputConfiguration: api.InputConfiguration{
				BaseImages: map[string]api.ImageStreamTagReference{
					"from":      {},
					"duplicate": {},
				},
			},
			Tests: tests("[input:duplicate]"),
		},
		expected: errs("[input:duplicate]"),
	}, {
		name: "duplicate directory builds",
		config: api.ReleaseBuildConfiguration{
			InputConfiguration:      input,
			BinaryBuildCommands:     "binary build commands",
			TestBinaryBuildCommands: "test binary build commands",
			RpmBuildCommands:        "RPM build commands",
			Tests:                   tests("bin", "test-bin", "rpms"),
		},
		expected: errs("bin", "test-bin", "rpms"),
	}, {
		name: "duplicate source build",
		config: api.ReleaseBuildConfiguration{
			InputConfiguration: input,
			Tests:              tests("src"),
		},
		expected: errs("src"),
	}, {
		name: "duplicate operator source",
		config: api.ReleaseBuildConfiguration{
			InputConfiguration: input,
			Operator:           &api.OperatorStepConfiguration{},
			Tests:              tests("src-bundle"),
		},
		expected: errs("src-bundle"),
	}, {
		name: "duplicate operator bundle",
		config: api.ReleaseBuildConfiguration{
			InputConfiguration: input,
			Operator: &api.OperatorStepConfiguration{
				Bundles: []api.Bundle{{As: "bundle"}},
			},
			Tests: tests("bundle"),
		},
		expected: errs("bundle"),
	}, {
		name: "duplicate operator index",
		config: api.ReleaseBuildConfiguration{
			InputConfiguration: input,
			Operator: &api.OperatorStepConfiguration{
				Bundles: []api.Bundle{{As: "bundle"}},
			},
			Tests: tests("ci-index-bundle-gen"),
		},
		expected: errs("ci-index-bundle-gen"),
	}, {
		name: "duplicate image build",
		config: api.ReleaseBuildConfiguration{
			InputConfiguration: input,
			Images: []api.ProjectDirectoryImageBuildStepConfiguration{{
				To: "duplicate",
			}},
			Tests: tests("duplicate"),
		},
		expected: errs("duplicate"),
	}, {
		name: "duplicate base RPM image",
		config: api.ReleaseBuildConfiguration{
			InputConfiguration: api.InputConfiguration{
				BaseImages: map[string]api.ImageStreamTagReference{"from": {}},
				BaseRPMImages: map[string]api.ImageStreamTagReference{
					"duplicate": {},
				},
			},
			Tests: tests("duplicate"),
		},
		expected: errs("duplicate"),
	}, {
		name: "duplicate RPM server",
		config: api.ReleaseBuildConfiguration{
			InputConfiguration: input,
			RpmBuildCommands:   "RPM build commands",
			Tests:              tests("[serve:rpms]"),
		},
		expected: errs("[serve:rpms]"),
	}, {
		name: "duplicate tag specification",
		config: api.ReleaseBuildConfiguration{
			InputConfiguration: api.InputConfiguration{
				BaseImages:              map[string]api.ImageStreamTagReference{"from": {}},
				ReleaseTagConfiguration: &api.ReleaseTagConfiguration{},
			},
			Tests: tests("[release-inputs]", "[release:initial]", "[release:latest]"),
		},
		expected: errs("[release-inputs]", "[release:initial]", "[release:latest]"),
	}, {
		name: "duplicate releases",
		config: api.ReleaseBuildConfiguration{
			InputConfiguration: api.InputConfiguration{
				BaseImages: map[string]api.ImageStreamTagReference{"from": {}},
				Releases:   map[string]api.UnresolvedRelease{"release": {}},
			},
			Tests: tests("[release:release]"),
		},
		expected: errs("[release:release]"),
	}, {
		name: "duplicate build root",
		config: api.ReleaseBuildConfiguration{
			InputConfiguration: api.InputConfiguration{
				BaseImages: map[string]api.ImageStreamTagReference{"from": {}},
				BuildRootImage: &api.BuildRootImageConfiguration{
					ProjectImageBuild: &api.ProjectDirectoryImageBuildInputs{},
				},
			},
			Tests: tests("root"),
		},
		expected: errs("root"),
	}} {
		t.Run(tc.name, func(t *testing.T) {
			graphConf := defaults.FromConfigStatic(&tc.config)
			graphConf.Steps = append(graphConf.Steps, api.StepConfiguration{
				SourceStepConfiguration: &api.SourceStepConfiguration{
					To: api.PipelineImageStreamTagReferenceSource,
				},
			})
			err := IsValidGraphConfiguration(graphConf.Steps)
			testhelper.Diff(t, "error", err, tc.expected, testhelper.EquateErrorMessage)
		})
	}
}

func TestIsValidGraph_ContainerTestFrom(t *testing.T) {
	tests := func(from ...string) (ret []api.TestStepConfiguration) {
		for _, f := range from {
			ret = append(ret, api.TestStepConfiguration{
				As: "test-" + f,
				ContainerTestConfiguration: &api.ContainerTestConfiguration{
					From: api.PipelineImageStreamTagReference(f),
				},
			})
		}
		return
	}
	errs := func(msgs ...string) error {
		var ret []error
		for _, m := range msgs {
			ret = append(ret, errors.New(m))
		}
		return utilerrors.NewAggregate(ret)
	}
	for _, tc := range []struct {
		name     string
		config   api.ReleaseBuildConfiguration
		expected error
	}{{
		name: "invalid",
		config: api.ReleaseBuildConfiguration{
			Tests: tests("invalid"),
		},
		expected: errs(`tests[test-invalid].from: unknown image "invalid"`),
	}, {
		name:   "from src",
		config: api.ReleaseBuildConfiguration{Tests: tests("src")},
	}, {
		name: "from root tag",
		config: api.ReleaseBuildConfiguration{
			InputConfiguration: api.InputConfiguration{
				BuildRootImage: &api.BuildRootImageConfiguration{
					ImageStreamTagReference: &api.ImageStreamTagReference{},
				},
			},
			Tests: tests("root"),
		},
	}, {
		name: "from base image",
		config: api.ReleaseBuildConfiguration{
			InputConfiguration: api.InputConfiguration{
				BaseImages: map[string]api.ImageStreamTagReference{"from": {}},
			},
			Tests: tests("from"),
		},
	}, {
		name: "from base RPM image",
		config: api.ReleaseBuildConfiguration{
			InputConfiguration: api.InputConfiguration{
				BaseRPMImages: map[string]api.ImageStreamTagReference{
					"from": {},
				},
			},
			Tests: tests("from", "from-without-rpms"),
		},
	}, {
		name: "from binaries image",
		config: api.ReleaseBuildConfiguration{
			BinaryBuildCommands: "binary build commands",
			Tests:               tests("bin"),
		},
	}, {
		name: "from test binaries image",
		config: api.ReleaseBuildConfiguration{
			TestBinaryBuildCommands: "test binary build commands",
			Tests:                   tests("test-bin"),
		},
	}, {
		name: "from RPMS image",
		config: api.ReleaseBuildConfiguration{
			RpmBuildCommands: "RPM build commands",
			Tests:            tests("rpms"),
		},
	}, {
		name: "from unnamed bundle images",
		config: api.ReleaseBuildConfiguration{
			Operator: &api.OperatorStepConfiguration{
				Bundles: []api.Bundle{{}},
			},
			Tests: tests("src-bundle", "ci-index", "ci-index-gen", "ci-bundle0"),
		},
	}, {
		name: "from bundle images",
		config: api.ReleaseBuildConfiguration{
			Operator: &api.OperatorStepConfiguration{
				Bundles: []api.Bundle{{As: "bundle"}},
			},
			Tests: tests("src-bundle", "ci-index-gen", "bundle", "ci-index-bundle"),
		},
	}, {
		name: "from image",
		config: api.ReleaseBuildConfiguration{
			Images: []api.ProjectDirectoryImageBuildStepConfiguration{
				{To: "from"},
			},
			Tests: tests("from"),
		},
	}, {
		name: "from root",
		config: api.ReleaseBuildConfiguration{
			InputConfiguration: api.InputConfiguration{
				BuildRootImage: &api.BuildRootImageConfiguration{
					ProjectImageBuild: &api.ProjectDirectoryImageBuildInputs{},
				},
			},
			Tests: tests("root"),
		},
	}} {
		t.Run(tc.name, func(t *testing.T) {
			graphConf := defaults.FromConfigStatic(&tc.config)
			err := IsValidGraphConfiguration(graphConf.Steps)
			testhelper.Diff(t, "error", err, tc.expected, testhelper.EquateErrorMessage)
		})
	}
}
