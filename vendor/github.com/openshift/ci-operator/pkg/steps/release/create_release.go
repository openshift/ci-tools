package release

import (
	"context"
	"fmt"
	"log"

	imageapi "github.com/openshift/api/image/v1"
	imageclientset "github.com/openshift/client-go/image/clientset/versioned/typed/image/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	rbacclientset "k8s.io/client-go/kubernetes/typed/rbac/v1"

	"github.com/openshift/ci-operator/pkg/api"
	"github.com/openshift/ci-operator/pkg/steps"
)

// assembleReleaseStep knows how to build an update payload image for
// an OpenShift release by waiting for the full release image set to be
// created, then invoking the admin command for building a new release.
type assembleReleaseStep struct {
	config      api.ReleaseTagConfiguration
	resources   api.ResourceConfiguration
	imageClient imageclientset.ImageV1Interface
	podClient   steps.PodClient
	rbacClient  rbacclientset.RbacV1Interface
	artifactDir string
	jobSpec     *api.JobSpec
}

func (s *assembleReleaseStep) Inputs(ctx context.Context, dry bool) (api.InputDefinition, error) {
	return nil, nil
}

func (s *assembleReleaseStep) Run(ctx context.Context, dry bool) error {
	stable, err := s.imageClient.ImageStreams(s.jobSpec.Namespace).Get(api.StableImageStream, meta.GetOptions{})
	if err != nil {
		return fmt.Errorf("could not resolve stable imagestream: %v", err)
	}
	cvo, ok := resolvePullSpec(stable, "cluster-version-operator")
	if !ok {
		log.Printf("No release image necessary, stable image stream does not include a cluster-version-operator image")
		return nil
	}
	if _, ok := resolvePullSpec(stable, "cli"); !ok {
		return fmt.Errorf("no 'cli' image was tagged into the stable stream, that image is required for building a release")
	}

	release, err := s.imageClient.ImageStreams(s.jobSpec.Namespace).Create(&imageapi.ImageStream{
		ObjectMeta: meta.ObjectMeta{
			Name: "release",
		},
	})
	if err != nil {
		if !errors.IsAlreadyExists(err) {
			return err
		}
		release, err = s.imageClient.ImageStreams(s.jobSpec.Namespace).Get("release", meta.GetOptions{})
		if err != nil {
			return err
		}
	}

	destination := fmt.Sprintf("%s:%s", release.Status.PublicDockerImageRepository, "latest")
	log.Printf("Create a new update payload image %s", destination)
	podConfig := steps.PodStepConfiguration{
		As: "release-latest",
		From: api.ImageStreamTagReference{
			Name: api.StableImageStream,
			Tag:  "cli",
		},
		ServiceAccountName: "builder",
		ArtifactDir:        "/tmp/artifacts",
		Commands: fmt.Sprintf(`
set -euo pipefail
export HOME=/tmp
oc registry login
oc adm release new --max-per-registry=32 -n %q --from-image-stream %q --to-image-base %q --to-image %q
oc adm release extract --from=%q --to=/tmp/artifacts/release-payload
`, s.jobSpec.Namespace, api.StableImageStream, cvo, destination, destination),
	}

	// set an explicit default for release-latest resources, but allow customization if necessary
	resources := s.resources
	if _, ok := resources[podConfig.As]; !ok {
		copied := make(api.ResourceConfiguration)
		for k, v := range resources {
			copied[k] = v
		}
		// max cpu observed at 0.1 core, most memory ~ 420M
		copied[podConfig.As] = api.ResourceRequirements{Requests: api.ResourceList{"cpu": "50m", "memory": "400Mi"}}
		resources = copied
	}

	step := steps.PodStep("release", podConfig, resources, s.podClient, s.artifactDir, s.jobSpec)

	return step.Run(ctx, dry)
}

func (s *assembleReleaseStep) Done() (bool, error) {
	// TODO: define done
	return true, nil
}

func (s *assembleReleaseStep) Requires() []api.StepLink {
	return []api.StepLink{api.ImagesReadyLink()}
}

func (s *assembleReleaseStep) Creates() []api.StepLink {
	return []api.StepLink{api.ReleasePayloadImageLink(api.PipelineImageStreamTagReference("latest"))}
}

func (s *assembleReleaseStep) Provides() (api.ParameterMap, api.StepLink) {
	return api.ParameterMap{
		"RELEASE_IMAGE_LATEST": func() (string, error) {
			is, err := s.imageClient.ImageStreams(s.jobSpec.Namespace).Get("release", meta.GetOptions{})
			if err != nil {
				return "", fmt.Errorf("could not retrieve output imagestream: %v", err)
			}
			var registry string
			if len(is.Status.PublicDockerImageRepository) > 0 {
				registry = is.Status.PublicDockerImageRepository
			} else if len(is.Status.DockerImageRepository) > 0 {
				registry = is.Status.DockerImageRepository
			} else {
				return "", fmt.Errorf("image stream %s has no accessible image registry value", "release")
			}
			return fmt.Sprintf("%s:%s", registry, "latest"), nil
		},
	}, api.ReleasePayloadImageLink(api.PipelineImageStreamTagReference("latest"))
}

func (s *assembleReleaseStep) Name() string { return "[release:latest]" }

func (s *assembleReleaseStep) Description() string {
	return fmt.Sprintf("Create a release image in the release image stream")
}

// AssembleReleaseStep builds a new update payload image based on the cluster version operator
// and the operators defined in the release configuration.
func AssembleReleaseStep(config api.ReleaseTagConfiguration, resources api.ResourceConfiguration, podClient steps.PodClient, imageClient imageclientset.ImageV1Interface, artifactDir string, jobSpec *api.JobSpec) api.Step {
	return &assembleReleaseStep{
		config:      config,
		resources:   resources,
		podClient:   podClient,
		imageClient: imageClient,
		artifactDir: artifactDir,
		jobSpec:     jobSpec,
	}
}

func resolvePullSpec(is *imageapi.ImageStream, tag string) (string, bool) {
	for _, tags := range is.Status.Tags {
		if tags.Tag != tag {
			continue
		}
		if len(tags.Items) == 0 {
			break
		}
		if image := tags.Items[0].Image; len(image) > 0 {
			if len(is.Status.PublicDockerImageRepository) > 0 {
				return fmt.Sprintf("%s@%s", is.Status.PublicDockerImageRepository, image), true
			}
			if len(is.Status.DockerImageRepository) > 0 {
				return fmt.Sprintf("%s@%s", is.Status.DockerImageRepository, image), true
			}
		}
		break
	}
	return "", false
}
