package steps

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	fakectrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"

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
		name        string
		step        indexGeneratorStep
		expected    string
		expectedErr error
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
RUN mkdir index && mkdir index/ci-bundle0 && opm render some-reg/target-namespace/pipeline@ci-bundle0 > index/ci-bundle0/index.yaml && opm generate dockerfile index
FROM pipeline:src
WORKDIR /index-data
COPY --from=builder index.Dockerfile index.Dockerfile
COPY --from=builder index index`,
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
RUN mkdir index && mkdir index/ci-bundle0 && opm render some-reg/target-namespace/pipeline@ci-bundle0 > index/ci-bundle0/index.yaml && mkdir index/ci-bundle1 && opm render some-reg/target-namespace/pipeline@ci-bundle1 > index/ci-bundle1/index.yaml && opm generate dockerfile index
FROM pipeline:src
WORKDIR /index-data
COPY --from=builder index.Dockerfile index.Dockerfile
COPY --from=builder index index`,
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
RUN mkdir index && mkdir index/ci-bundle0 && opm render some-reg/target-namespace/pipeline@ci-bundle0 > index/ci-bundle0/index.yaml && mkdir index/the-index && opm render some-reg/target-namespace/pipeline@the-index > index/the-index/index.yaml && opm generate dockerfile index
FROM pipeline:src
WORKDIR /index-data
COPY --from=builder index.Dockerfile index.Dockerfile
COPY --from=builder index index`,
	}}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			testCase.step.jobSpec.SetNamespace("target-namespace")
			actual, actualErr := testCase.step.indexGenDockerfile()
			if diff := cmp.Diff(testCase.expectedErr, actualErr, testhelper.EquateErrorMessage); diff != "" {
				t.Fatalf("error did not match expectedError, diff: %s", diff)
			}
			if diff := cmp.Diff(testCase.expected, actual); actualErr == nil && diff != "" {
				t.Fatalf("actual did not match expected, diff: %s", diff)
			}
		})
	}
}
