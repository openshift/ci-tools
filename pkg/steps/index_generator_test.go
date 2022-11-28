package steps

import (
	"fmt"
	"io/ioutil"
	"testing"

	"github.com/google/go-cmp/cmp"

	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	fakectrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/yaml"

	apiimagev1 "github.com/openshift/api/image/v1"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/steps/loggingclient"
	"github.com/openshift/ci-tools/pkg/testhelper"
)

func TestIndexGenDockerfile(t *testing.T) {
	fakeClientSet := fakectrlruntimeclient.NewFakeClient(
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
		})
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
			client:  &buildClient{LoggingClient: loggingclient.New(fakeClientSet)},
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
			client:  &buildClient{LoggingClient: loggingclient.New(fakeClientSet)},
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
			client:  &buildClient{LoggingClient: loggingclient.New(fakeClientSet)},
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

func TestValidateIndexGeneratorStep(t *testing.T) {
	testCases := []struct {
		name      string
		step      indexGeneratorStep
		istFile   string
		baseIndex string
		expected  error
	}{{
		name:      "base case",
		istFile:   "testdata/isTags/pipeline_v4.10_istag.yaml",
		baseIndex: "v4.10",
	}, {
		name:      "not found",
		istFile:   "testdata/isTags/pipeline_v4.10_istag.yaml",
		baseIndex: "ghost",
		expected:  fmt.Errorf(`failed to determine if the image ns/pipeline:ghost is sqlite based index: could not fetch source ImageStreamTag: imagestreamtags.image.openshift.io "pipeline:ghost" not found`),
	}, {
		name:      "no metadata",
		istFile:   "testdata/isTags/pipeline_no_bytes_istag.yaml",
		baseIndex: "v4.10",
		expected:  fmt.Errorf(`failed to determine if the image ns/pipeline:v4.10 is sqlite based index: could not fetch Docker image metadata for ImageStreamTag pipeline:v4.10`),
	}, {
		name:      "malformed json",
		istFile:   "testdata/isTags/pipeline_malformed_istag.yaml",
		baseIndex: "v4.10",
		expected:  fmt.Errorf(`failed to determine if the image ns/pipeline:v4.10 is sqlite based index: malformed Docker image metadata on ImageStreamTag: json: cannot unmarshal string into Go value of type docker10.DockerImage`),
	}, {
		name:      "no labels",
		istFile:   "testdata/isTags/pipeline_no_label_istag.yaml",
		baseIndex: "v4.10",
		expected:  fmt.Errorf(`opm index commands, which are used by the ci-operator, interact only with a database index, but the base index is not one. Please refer to the FBC docs here: https://olm.operatorframework.io/docs/reference/file-based-catalogs/`),
	}, {
		name:      "v4.11",
		istFile:   "testdata/isTags/pipeline_v4.11_istag.yaml",
		baseIndex: "v4.11",
		expected:  fmt.Errorf(`opm index commands, which are used by the ci-operator, interact only with a database index, but the base index is not one. Please refer to the FBC docs here: https://olm.operatorframework.io/docs/reference/file-based-catalogs/`),
	}}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			rawImageStreamTag, err := ioutil.ReadFile(testCase.istFile)
			if err != nil {
				t.Fatalf("failed to read imagestreamtag fixture: %v", err)
			}
			ist := &apiimagev1.ImageStreamTag{}
			if err := yaml.Unmarshal(rawImageStreamTag, ist); err != nil {
				t.Fatalf("failed to unmarshal imagestreamTag: %v", err)
			}
			testCase.step = indexGeneratorStep{
				client: NewBuildClient(loggingclient.New(fakectrlruntimeclient.NewClientBuilder().WithObjects(ist).Build()), nil),
				config: api.IndexGeneratorStepConfiguration{
					BaseIndex: testCase.baseIndex,
				},
				jobSpec: &api.JobSpec{},
			}
			testCase.step.jobSpec.SetNamespace("ns")
			if diff := cmp.Diff(testCase.expected, testCase.step.Validate(), testhelper.EquateErrorMessage); diff != "" {
				t.Fatalf("error did not match expected, diff: %s", diff)
			}
		})
	}
}
