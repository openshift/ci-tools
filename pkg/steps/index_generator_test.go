package steps

import (
	"fmt"
	"os"
	"testing"

	"github.com/google/go-cmp/cmp"

	coreapi "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	fakectrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/yaml"

	apiimagev1 "github.com/openshift/api/image/v1"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/steps/loggingclient"
	"github.com/openshift/ci-tools/pkg/testhelper"
)

func TestIndexGenDockerfile(t *testing.T) {
	fakeClientSet := fakectrlruntimeclient.NewClientBuilder().WithRuntimeObjects(
		&apiimagev1.ImageStream{
			ObjectMeta: v1.ObjectMeta{
				Namespace: "target-namespace",
				Name:      api.PipelineImageStream,
			},
			Status: apiimagev1.ImageStreamStatus{
				PublicDockerImageRepository: "some-reg/target-namespace/pipeline",
				Tags: []apiimagev1.NamedTagEventList{{
					Tag: "ci-bundle0",
					Items: []apiimagev1.TagEvent{{
						Image: "ci-bundle0",
					}},
				}, {
					Tag: "ci-bundle1",
					Items: []apiimagev1.TagEvent{{
						Image: "ci-bundle1",
					}},
				}, {
					Tag: "the-index",
					Items: []apiimagev1.TagEvent{{
						Image: "the-index",
					}},
				}},
			},
		}).Build()
	testCases := []struct {
		name     string
		step     indexGeneratorStep
		expected string
	}{{
		name: "single bundle",
		step: indexGeneratorStep{
			config: api.IndexGeneratorStepConfiguration{
				OperatorIndex: []string{"ci-bundle0"},
				UpdateGraph:   api.IndexUpdateSemver,
			},
			jobSpec: &api.JobSpec{},
			client:  &buildClient{LoggingClient: loggingclient.New(fakeClientSet, nil)},
		},
		expected: `FROM quay.io/operator-framework/upstream-opm-builder AS builder
RUN ["opm", "index", "add", "--mode", "semver", "--bundles", "some-reg/target-namespace/pipeline@ci-bundle0", "--out-dockerfile", "index.Dockerfile", "--generate"]
FROM pipeline:src
WORKDIR /index-data
COPY --from=builder index.Dockerfile index.Dockerfile
COPY --from=builder /database/ database`,
	}, {
		name: "single bundle with pull secret",
		step: indexGeneratorStep{
			config: api.IndexGeneratorStepConfiguration{
				OperatorIndex: []string{"ci-bundle0"},
				UpdateGraph:   api.IndexUpdateSemver,
			},
			jobSpec:    &api.JobSpec{},
			pullSecret: &coreapi.Secret{},
			client:     &buildClient{LoggingClient: loggingclient.New(fakeClientSet, nil)},
		},
		expected: `FROM quay.io/operator-framework/upstream-opm-builder AS builder
COPY .dockerconfigjson .
RUN mkdir $HOME/.docker && mv .dockerconfigjson $HOME/.docker/config.json
RUN ["opm", "index", "add", "--mode", "semver", "--bundles", "some-reg/target-namespace/pipeline@ci-bundle0", "--out-dockerfile", "index.Dockerfile", "--generate"]
FROM pipeline:src
WORKDIR /index-data
COPY --from=builder index.Dockerfile index.Dockerfile
COPY --from=builder /database/ database`,
	}, {
		name: "multiple bundles",
		step: indexGeneratorStep{
			config: api.IndexGeneratorStepConfiguration{
				OperatorIndex: []string{"ci-bundle0", "ci-bundle1"},
				UpdateGraph:   api.IndexUpdateSemver,
			},
			jobSpec: &api.JobSpec{},
			client:  &buildClient{LoggingClient: loggingclient.New(fakeClientSet, nil)},
		},
		expected: `FROM quay.io/operator-framework/upstream-opm-builder AS builder
RUN ["opm", "index", "add", "--mode", "semver", "--bundles", "some-reg/target-namespace/pipeline@ci-bundle0,some-reg/target-namespace/pipeline@ci-bundle1", "--out-dockerfile", "index.Dockerfile", "--generate"]
FROM pipeline:src
WORKDIR /index-data
COPY --from=builder index.Dockerfile index.Dockerfile
COPY --from=builder /database/ database`,
	}, {
		name: "multiple bundles with pull secret",
		step: indexGeneratorStep{
			config: api.IndexGeneratorStepConfiguration{
				OperatorIndex: []string{"ci-bundle0", "ci-bundle1"},
				UpdateGraph:   api.IndexUpdateSemver,
			},
			jobSpec:    &api.JobSpec{},
			pullSecret: &coreapi.Secret{},
			client:     &buildClient{LoggingClient: loggingclient.New(fakeClientSet, nil)},
		},
		expected: `FROM quay.io/operator-framework/upstream-opm-builder AS builder
COPY .dockerconfigjson .
RUN mkdir $HOME/.docker && mv .dockerconfigjson $HOME/.docker/config.json
RUN ["opm", "index", "add", "--mode", "semver", "--bundles", "some-reg/target-namespace/pipeline@ci-bundle0,some-reg/target-namespace/pipeline@ci-bundle1", "--out-dockerfile", "index.Dockerfile", "--generate"]
FROM pipeline:src
WORKDIR /index-data
COPY --from=builder index.Dockerfile index.Dockerfile
COPY --from=builder /database/ database`,
	}, {
		name: "With base index",
		step: indexGeneratorStep{
			config: api.IndexGeneratorStepConfiguration{
				OperatorIndex: []string{"ci-bundle0"},
				UpdateGraph:   api.IndexUpdateSemver,
				BaseIndex:     "the-index",
			},
			jobSpec: &api.JobSpec{},
			client:  &buildClient{LoggingClient: loggingclient.New(fakeClientSet, nil)},
		},
		expected: `FROM quay.io/operator-framework/upstream-opm-builder AS builder
RUN ["opm", "index", "add", "--mode", "semver", "--bundles", "some-reg/target-namespace/pipeline@ci-bundle0", "--out-dockerfile", "index.Dockerfile", "--generate", "--from-index", "some-reg/target-namespace/pipeline@the-index"]
FROM pipeline:src
WORKDIR /index-data
COPY --from=builder index.Dockerfile index.Dockerfile
COPY --from=builder /database/ database`,
	}, {
		name: "With base index with pull secret",
		step: indexGeneratorStep{
			config: api.IndexGeneratorStepConfiguration{
				OperatorIndex: []string{"ci-bundle0"},
				UpdateGraph:   api.IndexUpdateSemver,
				BaseIndex:     "the-index",
			},
			jobSpec:    &api.JobSpec{},
			pullSecret: &coreapi.Secret{},
			client:     &buildClient{LoggingClient: loggingclient.New(fakeClientSet, nil)},
		},
		expected: `FROM quay.io/operator-framework/upstream-opm-builder AS builder
COPY .dockerconfigjson .
RUN mkdir $HOME/.docker && mv .dockerconfigjson $HOME/.docker/config.json
RUN ["opm", "index", "add", "--mode", "semver", "--bundles", "some-reg/target-namespace/pipeline@ci-bundle0", "--out-dockerfile", "index.Dockerfile", "--generate", "--from-index", "some-reg/target-namespace/pipeline@the-index"]
FROM pipeline:src
WORKDIR /index-data
COPY --from=builder index.Dockerfile index.Dockerfile
COPY --from=builder /database/ database`,
	}}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			testCase.step.jobSpec.SetNamespace("target-namespace")
			generated, err := testCase.step.indexGenDockerfile()
			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}
			if testCase.expected != generated {
				t.Errorf("Generated opm index dockerfile does not equal expected:\n%s", cmp.Diff(testCase.expected, generated))
			}
		})
	}
}

func TestDatabaseIndex(t *testing.T) {
	rawImageStreamTag, err := os.ReadFile("testdata/isTags/59c70bff_image.yaml")
	if err != nil {
		t.Fatalf("failed to read image fixture: %v", err)
	}
	image := &apiimagev1.Image{}
	if err := yaml.Unmarshal(rawImageStreamTag, image); err != nil {
		t.Fatalf("failed to unmarshal image: %v", err)
	}
	testCases := []struct {
		name        string
		istFile     string
		isTagName   string
		expected    bool
		expectedErr error
	}{{
		name:      "base case",
		istFile:   "testdata/isTags/pipeline_v4.10_istag.yaml",
		isTagName: "pipeline:v4.10",
		expected:  true,
	}, {
		name:        "not found",
		istFile:     "testdata/isTags/pipeline_v4.10_istag.yaml",
		isTagName:   "ghost:ghost",
		expectedErr: fmt.Errorf(`could not fetch source ImageStreamTag: imagestreamtags.image.openshift.io "ghost:ghost" not found`),
	}, {
		name:        "no metadata",
		istFile:     "testdata/isTags/pipeline_no_bytes_istag.yaml",
		isTagName:   "pipeline:v4.10",
		expectedErr: fmt.Errorf(`failed to get value of the image label: found no Docker image metadata for ImageStreamTag pipeline:v4.10 in ns`),
	}, {
		name:        "malformed json",
		istFile:     "testdata/isTags/pipeline_malformed_istag.yaml",
		isTagName:   "pipeline:v4.10",
		expectedErr: fmt.Errorf(`failed to get value of the image label: malformed Docker image metadata for ImageStreamTag pipeline:v4.10 in ns: json: cannot unmarshal string into Go value of type docker10.DockerImage`),
	}, {
		name:      "no labels",
		istFile:   "testdata/isTags/pipeline_no_label_istag.yaml",
		isTagName: "pipeline:v4.10",
	}, {
		name:      "v4.11",
		istFile:   "testdata/isTags/pipeline_v4.11_istag.yaml",
		isTagName: "pipeline:v4.11",
	}, {
		name:      "dockerImageManifests",
		istFile:   "testdata/isTags/redhat-operator-index_v4.10_istag_manifests.yaml",
		isTagName: "redhat-operator-index:v4.10",
		expected:  true,
	}}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			rawImageStreamTag, err := os.ReadFile(testCase.istFile)
			if err != nil {
				t.Fatalf("failed to read imagestreamtag fixture: %v", err)
			}
			ist := &apiimagev1.ImageStreamTag{}
			if err := yaml.Unmarshal(rawImageStreamTag, ist); err != nil {
				t.Fatalf("failed to unmarshal imagestreamTag: %v", err)
			}
			actual, actualErr := databaseIndex(NewBuildClient(loggingclient.New(fakectrlruntimeclient.NewClientBuilder().WithObjects(ist, image).Build(), nil), nil, nil, "", "", nil),
				testCase.isTagName, "ns")
			if diff := cmp.Diff(testCase.expectedErr, actualErr, testhelper.EquateErrorMessage); diff != "" {
				t.Fatalf("actual did not match expected, diff: %s", diff)
			}
			if diff := cmp.Diff(testCase.expected, actual); actualErr == nil && diff != "" {
				t.Fatalf("error did not match expected, diff: %s", diff)
			}
		})
	}
}
