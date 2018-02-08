package steps

import (
	"bytes"
	"fmt"
	"reflect"
	"testing"

	"github.com/openshift/ci-operator/pkg/api"
)

func TestStepConfigsForBuild(t *testing.T) {
	var testCases = []struct {
		name    string
		input   *api.ReleaseBuildConfiguration
		jobSpec *JobSpec
		output  []api.StepConfiguration
	}{
		{
			name: "minimal information provided",
			input: &api.ReleaseBuildConfiguration{
				TestBaseImage: &api.ImageStreamTagReference{
					Tag: "manual",
				},
			},
			jobSpec: &JobSpec{
				Refs: Refs{
					Repo: "repo",
				},
			},
			output: []api.StepConfiguration{{
				SourceStepConfiguration: &api.SourceStepConfiguration{
					From: api.PipelineImageStreamTagReferenceBase,
					To:   api.PipelineImageStreamTagReferenceSource,
				},
			}, {
				InputImageTagStepConfiguration: &api.InputImageTagStepConfiguration{
					BaseImage: api.ImageStreamTagReference{
						Namespace: StableImageNamespace,
						Name:      "repo-test-base",
						Tag:       "manual",
					},
					To: api.PipelineImageStreamTagReferenceBase,
				},
			}},
		},
		{
			name: "binary build requested",
			input: &api.ReleaseBuildConfiguration{
				TestBaseImage: &api.ImageStreamTagReference{
					Tag: "manual",
				},
				BinaryBuildCommands: "hi",
			},
			jobSpec: &JobSpec{
				Refs: Refs{
					Repo: "repo",
				},
			},
			output: []api.StepConfiguration{{
				SourceStepConfiguration: &api.SourceStepConfiguration{
					From: api.PipelineImageStreamTagReferenceBase,
					To:   api.PipelineImageStreamTagReferenceSource,
				},
			}, {
				InputImageTagStepConfiguration: &api.InputImageTagStepConfiguration{
					BaseImage: api.ImageStreamTagReference{
						Namespace: StableImageNamespace,
						Name:      "repo-test-base",
						Tag:       "manual",
					},
					To: api.PipelineImageStreamTagReferenceBase,
				},
			}, {
				PipelineImageCacheStepConfiguration: &api.PipelineImageCacheStepConfiguration{
					From:     api.PipelineImageStreamTagReferenceSource,
					To:       api.PipelineImageStreamTagReferenceBinaries,
					Commands: "hi",
				},
			}},
		},
		{
			name: "binary and rpm build requested",
			input: &api.ReleaseBuildConfiguration{
				TestBaseImage: &api.ImageStreamTagReference{
					Tag: "manual",
				},
				BinaryBuildCommands: "hi",
				RpmBuildCommands:    "hello",
			},
			jobSpec: &JobSpec{
				Refs: Refs{
					Repo: "repo",
				},
			},
			output: []api.StepConfiguration{{
				SourceStepConfiguration: &api.SourceStepConfiguration{
					From: api.PipelineImageStreamTagReferenceBase,
					To:   api.PipelineImageStreamTagReferenceSource,
				},
			}, {
				InputImageTagStepConfiguration: &api.InputImageTagStepConfiguration{
					BaseImage: api.ImageStreamTagReference{
						Namespace: StableImageNamespace,
						Name:      "repo-test-base",
						Tag:       "manual",
					},
					To: api.PipelineImageStreamTagReferenceBase,
				},
			}, {
				PipelineImageCacheStepConfiguration: &api.PipelineImageCacheStepConfiguration{
					From:     api.PipelineImageStreamTagReferenceSource,
					To:       api.PipelineImageStreamTagReferenceBinaries,
					Commands: "hi",
				},
			}, {
				PipelineImageCacheStepConfiguration: &api.PipelineImageCacheStepConfiguration{
					From:     api.PipelineImageStreamTagReferenceBinaries,
					To:       api.PipelineImageStreamTagReferenceRPMs,
					Commands: "hello; ln -s $( pwd )/_output/local/releases/rpms/ /srv/repo",
				},
			}, {
				RPMServeStepConfiguration: &api.RPMServeStepConfiguration{
					From: api.PipelineImageStreamTagReferenceRPMs,
				},
			}},
		},
		{
			name: "rpm but not binary build requested",
			input: &api.ReleaseBuildConfiguration{
				TestBaseImage: &api.ImageStreamTagReference{
					Tag: "manual",
				},
				RpmBuildCommands: "hello",
			},
			jobSpec: &JobSpec{
				Refs: Refs{
					Repo: "repo",
				},
			},
			output: []api.StepConfiguration{{
				SourceStepConfiguration: &api.SourceStepConfiguration{
					From: api.PipelineImageStreamTagReferenceBase,
					To:   api.PipelineImageStreamTagReferenceSource,
				},
			}, {
				InputImageTagStepConfiguration: &api.InputImageTagStepConfiguration{
					BaseImage: api.ImageStreamTagReference{
						Namespace: StableImageNamespace,
						Name:      "repo-test-base",
						Tag:       "manual",
					},
					To: api.PipelineImageStreamTagReferenceBase,
				},
			}, {
				PipelineImageCacheStepConfiguration: &api.PipelineImageCacheStepConfiguration{
					From:     api.PipelineImageStreamTagReferenceSource,
					To:       api.PipelineImageStreamTagReferenceRPMs,
					Commands: "hello; ln -s $( pwd )/_output/local/releases/rpms/ /srv/repo",
				},
			}, {
				RPMServeStepConfiguration: &api.RPMServeStepConfiguration{
					From: api.PipelineImageStreamTagReferenceRPMs,
				},
			}},
		},
		{
			name: "rpm with custom output but not binary build requested",
			input: &api.ReleaseBuildConfiguration{
				TestBaseImage: &api.ImageStreamTagReference{
					Tag: "manual",
				},
				RpmBuildLocation: "testing",
				RpmBuildCommands: "hello",
			},
			jobSpec: &JobSpec{
				Refs: Refs{
					Repo: "repo",
				},
			},
			output: []api.StepConfiguration{{
				SourceStepConfiguration: &api.SourceStepConfiguration{
					From: api.PipelineImageStreamTagReferenceBase,
					To:   api.PipelineImageStreamTagReferenceSource,
				},
			}, {
				InputImageTagStepConfiguration: &api.InputImageTagStepConfiguration{
					BaseImage: api.ImageStreamTagReference{
						Namespace: StableImageNamespace,
						Name:      "repo-test-base",
						Tag:       "manual",
					},
					To: api.PipelineImageStreamTagReferenceBase,
				},
			}, {
				PipelineImageCacheStepConfiguration: &api.PipelineImageCacheStepConfiguration{
					From:     api.PipelineImageStreamTagReferenceSource,
					To:       api.PipelineImageStreamTagReferenceRPMs,
					Commands: "hello; ln -s $( pwd )/testing /srv/repo",
				},
			}, {
				RPMServeStepConfiguration: &api.RPMServeStepConfiguration{
					From: api.PipelineImageStreamTagReferenceRPMs,
				},
			}},
		},
		{
			name: "explicit base image requested",
			input: &api.ReleaseBuildConfiguration{
				TestBaseImage: &api.ImageStreamTagReference{
					Tag: "manual",
				},
				BaseImages: []api.ImageStreamTagReference{{
					Namespace: "namespace",
					Name:      "name",
					Tag:       "tag",
				}},
			},
			jobSpec: &JobSpec{
				Refs: Refs{
					Repo: "repo",
				},
			},
			output: []api.StepConfiguration{{
				SourceStepConfiguration: &api.SourceStepConfiguration{
					From: api.PipelineImageStreamTagReferenceBase,
					To:   api.PipelineImageStreamTagReferenceSource,
				},
			}, {
				InputImageTagStepConfiguration: &api.InputImageTagStepConfiguration{
					BaseImage: api.ImageStreamTagReference{
						Namespace: StableImageNamespace,
						Name:      "repo-test-base",
						Tag:       "manual",
					},
					To: api.PipelineImageStreamTagReferenceBase,
				},
			}, {
				InputImageTagStepConfiguration: &api.InputImageTagStepConfiguration{
					BaseImage: api.ImageStreamTagReference{
						Namespace: "namespace",
						Name:      "name",
						Tag:       "tag",
					},
					To: api.PipelineImageStreamTagReference("name"),
				},
			}},
		},
		{
			name: "rpm base image requested",
			input: &api.ReleaseBuildConfiguration{
				TestBaseImage: &api.ImageStreamTagReference{
					Tag: "manual",
				},
				BaseRPMImages: []api.ImageStreamTagReference{{
					Namespace: "namespace",
					Name:      "name",
					Tag:       "tag",
				}},
			},
			jobSpec: &JobSpec{
				Refs: Refs{
					Repo: "repo",
				},
			},
			output: []api.StepConfiguration{{
				SourceStepConfiguration: &api.SourceStepConfiguration{
					From: api.PipelineImageStreamTagReferenceBase,
					To:   api.PipelineImageStreamTagReferenceSource,
				},
			}, {
				InputImageTagStepConfiguration: &api.InputImageTagStepConfiguration{
					BaseImage: api.ImageStreamTagReference{
						Namespace: StableImageNamespace,
						Name:      "repo-test-base",
						Tag:       "manual",
					},
					To: api.PipelineImageStreamTagReferenceBase,
				},
			}, {
				InputImageTagStepConfiguration: &api.InputImageTagStepConfiguration{
					BaseImage: api.ImageStreamTagReference{
						Namespace: "namespace",
						Name:      "name",
						Tag:       "tag",
					},
					To: api.PipelineImageStreamTagReference("name-without-rpms"),
				},
			}, {
				RPMImageInjectionStepConfiguration: &api.RPMImageInjectionStepConfiguration{
					From: api.PipelineImageStreamTagReference("name-without-rpms"),
					To:   api.PipelineImageStreamTagReference("name"),
				},
			}},
		},
	}

	for _, testCase := range testCases {
		if configs := stepConfigsForBuild(testCase.input, testCase.jobSpec); ! stepListsEqual(configs, testCase.output) {
			t.Errorf("%s: incorrect defaulted step configurations,\n\tgot:\n%s\n\texpected:\n%s", testCase.name, formatSteps(configs), formatSteps(testCase.output))
		}
	}
}

// stepListsEqual determines if the two lists of step configs
// contain the same elements, but is not interested
// in ordering
func stepListsEqual(first, second []api.StepConfiguration) bool {
	if len(first) != len(second) {
		return false
	}

	for _, item := range first {
		otherContains := false
		for _, other := range second {
			if reflect.DeepEqual(item, other) {
				otherContains = true
			}
		}
		if !otherContains {
			return false
		}
	}

	return true
}

func formatSteps(steps []api.StepConfiguration) string {
	output := bytes.Buffer{}
	for _, step := range steps {
		output.WriteString(formatStep(step))
		output.WriteString("\n")
	}
	return output.String()
}

func formatStep(step api.StepConfiguration) string {
	if step.InputImageTagStepConfiguration != nil {
		return fmt.Sprintf("Tag %s to pipeline:%s", formatReference(step.InputImageTagStepConfiguration.BaseImage), step.InputImageTagStepConfiguration.To)
	}

	if step.PipelineImageCacheStepConfiguration != nil {
		return fmt.Sprintf("Run %v in pipeline:%s to cache in pipeline:%s", step.PipelineImageCacheStepConfiguration.Commands, step.PipelineImageCacheStepConfiguration.From, step.PipelineImageCacheStepConfiguration.To)
	}

	if step.SourceStepConfiguration != nil {
		return fmt.Sprintf("Clone source into pipeline:%s to cache in pipline:%s", step.SourceStepConfiguration.From, step.SourceStepConfiguration.To)
	}

	if step.ProjectDirectoryImageBuildStepConfiguration != nil {
		return fmt.Sprintf("Build project image from %s in pipeline:%s to cache in pipline:%s", step.ProjectDirectoryImageBuildStepConfiguration.ContextDir, step.ProjectDirectoryImageBuildStepConfiguration.From, step.ProjectDirectoryImageBuildStepConfiguration.To)
	}

	if step.RPMImageInjectionStepConfiguration != nil {
		return fmt.Sprintf("Inject RPM repos into pipeline:%s to cache in pipline:%s", step.RPMImageInjectionStepConfiguration.From, step.RPMImageInjectionStepConfiguration.To)
	}

	if step.RPMServeStepConfiguration != nil {
		return fmt.Sprintf("Serve RPMs from pipeline:%s", step.RPMServeStepConfiguration.From)
	}

	return ""
}

func formatReference(ref api.ImageStreamTagReference) string {
	return fmt.Sprintf("%s/%s:%s", ref.Namespace, ref.Name, ref.Tag)
}
