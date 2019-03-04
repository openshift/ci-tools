package release

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	coreapi "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/wait"

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
	latest      bool
	resources   api.ResourceConfiguration
	imageClient imageclientset.ImageV1Interface
	podClient   steps.PodClient
	rbacClient  rbacclientset.RbacV1Interface
	artifactDir string
	jobSpec     *api.JobSpec
}

func (s *assembleReleaseStep) Inputs(ctx context.Context, dry bool) (api.InputDefinition, error) {
	if val := os.Getenv(s.envVar()); len(val) > 0 {
		return api.InputDefinition{val}, nil
	}
	return nil, nil
}

func (s *assembleReleaseStep) Run(ctx context.Context, dry bool) error {
	// if we receive an input, we tag it in instead of generating it
	providedImage := os.Getenv(s.envVar())
	if len(providedImage) > 0 {
		log.Printf("Setting release image %s to %s", s.tag(), providedImage)
		if _, err := s.imageClient.ImageStreamTags(s.jobSpec.Namespace).Update(&imageapi.ImageStreamTag{
			ObjectMeta: meta.ObjectMeta{
				Name: fmt.Sprintf("release:%s", s.tag()),
			},
			Tag: &imageapi.TagReference{
				From: &coreapi.ObjectReference{
					Kind: "DockerImage",
					Name: providedImage,
				},
			},
		}); err != nil {
			return err
		}
		if err := wait.PollImmediate(time.Second, time.Minute, func() (bool, error) {
			is, err := s.imageClient.ImageStreams(s.jobSpec.Namespace).Get("release", meta.GetOptions{})
			if err != nil {
				return false, err
			}
			ref, _ := findStatusTag(is, s.tag())
			return ref != nil, nil
		}); err != nil {
			return fmt.Errorf("unable to import %s release image: %v", s.tag(), err)
		}
		return nil
	}

	tag := s.tag()
	var streamName string
	if s.latest {
		streamName = api.StableImageStream
	} else {
		streamName = fmt.Sprintf("%s-initial", api.StableImageStream)
	}

	stable, err := s.imageClient.ImageStreams(s.jobSpec.Namespace).Get(streamName, meta.GetOptions{})
	if err != nil {
		return fmt.Errorf("could not resolve imagestream %s: %v", streamName, err)
	}
	cvo, ok := resolvePullSpec(stable, "cluster-version-operator", true)
	if !ok {
		log.Printf("No release image necessary, stable image stream does not include a cluster-version-operator image")
		return nil
	}
	if _, ok := resolvePullSpec(stable, "cli", true); !ok {
		return fmt.Errorf("no 'cli' image was tagged into the %s stream, that image is required for building a release", streamName)
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

	destination := fmt.Sprintf("%s:%s", release.Status.PublicDockerImageRepository, tag)
	log.Printf("Create release image %s", destination)
	podConfig := steps.PodStepConfiguration{
		As: fmt.Sprintf("release-%s", tag),
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
oc adm release extract --from=%q --to=/tmp/artifacts/release-payload-%s
`, s.jobSpec.Namespace, api.StableImageStream, cvo, destination, destination, tag),
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
	// if our prereq is provided, we don't need any prereqs
	if len(os.Getenv(s.envVar())) > 0 {
		return nil
	}
	if s.latest {
		return []api.StepLink{api.ImagesReadyLink()}
	}
	return []api.StepLink{api.ReleaseImagesLink()}
}

func (s *assembleReleaseStep) Creates() []api.StepLink {
	return []api.StepLink{api.ReleasePayloadImageLink(api.PipelineImageStreamTagReference(s.tag()))}
}

func (s *assembleReleaseStep) tag() string {
	if s.latest {
		return "latest"
	}
	return "initial"
}

func (s *assembleReleaseStep) envVar() string {
	return fmt.Sprintf("RELEASE_IMAGE_%s", strings.ToUpper(s.tag()))
}

func (s *assembleReleaseStep) Provides() (api.ParameterMap, api.StepLink) {
	tag := s.tag()
	return api.ParameterMap{
		s.envVar(): func() (string, error) {
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
			return fmt.Sprintf("%s:%s", registry, tag), nil
		},
	}, api.ReleasePayloadImageLink(api.PipelineImageStreamTagReference(tag))
}

func (s *assembleReleaseStep) Name() string {
	return fmt.Sprintf("[release:%s]", strings.ToUpper(s.tag()))
}

func (s *assembleReleaseStep) Description() string {
	if s.latest {
		return "Create the release image containing all images built by this job"
	}
	return "Create initial release image from the images that were in the input tag_specification"
}

// AssembleReleaseStep builds a new update payload image based on the cluster version operator
// and the operators defined in the release configuration.
func AssembleReleaseStep(latest bool, config api.ReleaseTagConfiguration, resources api.ResourceConfiguration, podClient steps.PodClient, imageClient imageclientset.ImageV1Interface, artifactDir string, jobSpec *api.JobSpec) api.Step {
	return &assembleReleaseStep{
		config:      config,
		latest:      latest,
		resources:   resources,
		podClient:   podClient,
		imageClient: imageClient,
		artifactDir: artifactDir,
		jobSpec:     jobSpec,
	}
}
