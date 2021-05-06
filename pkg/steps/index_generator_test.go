package steps

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	fakectrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"

	apiimagev1 "github.com/openshift/api/image/v1"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/steps/loggingclient"
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
