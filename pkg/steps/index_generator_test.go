package steps

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	apiimagev1 "github.com/openshift/api/image/v1"
	fakeimageclientset "github.com/openshift/client-go/image/clientset/versioned/fake"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/openshift/ci-tools/pkg/api"
)

func TestIndexGenDockerfile(t *testing.T) {
	fakeClientSet := ciopTestingClient{
		imagecs: fakeimageclientset.NewSimpleClientset(&apiimagev1.ImageStream{
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
				}},
			},
		}),
		t: t,
	}

	var expectedDockerfileSingleBundle = `FROM quay.io/operator-framework/upstream-opm-builder AS builder
COPY .dockerconfigjson .
RUN mkdir $HOME/.docker && mv .dockerconfigjson $HOME/.docker/config.json
RUN ["opm", "index", "add", "--mode", "semver", "--bundles", "some-reg/target-namespace/pipeline@ci-bundle0", "--out-dockerfile", "index.Dockerfile", "--generate"]
FROM pipeline:src
WORKDIR /index-data
COPY --from=builder index.Dockerfile index.Dockerfile
COPY --from=builder /database/ database`
	stepSingleBundle := indexGeneratorStep{
		config: api.IndexGeneratorStepConfiguration{
			OperatorIndex: []string{"ci-bundle0"},
		},
		jobSpec:     &api.JobSpec{},
		imageClient: fakeClientSet.ImageV1(),
	}
	stepSingleBundle.jobSpec.SetNamespace("target-namespace")
	generatedDockerfile, err := stepSingleBundle.indexGenDockerfile()
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if expectedDockerfileSingleBundle != generatedDockerfile {
		t.Errorf("Generated opm index dockerfile does not equal expected:\n%s", cmp.Diff(expectedDockerfileSingleBundle, generatedDockerfile))
	}

	var expectedDockerfileMultiBundle = `FROM quay.io/operator-framework/upstream-opm-builder AS builder
COPY .dockerconfigjson .
RUN mkdir $HOME/.docker && mv .dockerconfigjson $HOME/.docker/config.json
RUN ["opm", "index", "add", "--mode", "semver", "--bundles", "some-reg/target-namespace/pipeline@ci-bundle0,some-reg/target-namespace/pipeline@ci-bundle1", "--out-dockerfile", "index.Dockerfile", "--generate"]
FROM pipeline:src
WORKDIR /index-data
COPY --from=builder index.Dockerfile index.Dockerfile
COPY --from=builder /database/ database`
	stepMultiBundle := indexGeneratorStep{
		config: api.IndexGeneratorStepConfiguration{
			OperatorIndex: []string{"ci-bundle0", "ci-bundle1"},
		},
		jobSpec:     &api.JobSpec{},
		imageClient: fakeClientSet.ImageV1(),
	}
	stepMultiBundle.jobSpec.SetNamespace("target-namespace")
	generatedDockerfile, err = stepMultiBundle.indexGenDockerfile()
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if expectedDockerfileMultiBundle != generatedDockerfile {
		t.Errorf("Generated opm index dockerfile does not equal expected:\n%s", cmp.Diff(expectedDockerfileMultiBundle, generatedDockerfile))
	}
}
