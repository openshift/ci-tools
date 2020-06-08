package release

import (
	"context"
	"fmt"
	"github.com/openshift/ci-tools/pkg/results"
	"log"
	"strings"
	"time"

	imageapi "github.com/openshift/api/image/v1"
	imageclientset "github.com/openshift/client-go/image/clientset/versioned/typed/image/v1"
	coreapi "k8s.io/api/core/v1"
	rbacapi "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	coreclientset "k8s.io/client-go/kubernetes/typed/core/v1"
	rbacclientset "k8s.io/client-go/kubernetes/typed/rbac/v1"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/steps"
	"github.com/openshift/ci-tools/pkg/util"
)

// assembleReleaseStep is responsible for creating release images from
// the stable or stable-initial image streams for use with tests that need
// to install or upgrade a cluster. It uses the `cli` image within the
// image stream to create the image and pushes it to a `release` image stream
// at the `latest` or `initial` tags. As output it provides the environment
// variables RELEASE_IMAGE_(LATEST|INITIAL) which can be used by templates
// that invoke the installer.
//
// Since release images describe a set of images, when a user provides
// RELEASE_IMAGE_INITIAL or RELEASE_IMAGE_LATEST as inputs to the ci-operator
// job we treat those as inputs we must expand into the `stable-initial` or
// `stable` image streams. This is because our test scenarios need access not
// just to the release image, but also to the images in that release image
// like installer, cli, or tests. To make it easy for a CI job to install from
// an older release image, we need to extract the 'installer' image into the
// same location that we would expect if it came from a tag_specification.
// The images inside of a release image override any images built or imported
// into the job, which allows you to have an empty tag_specification and
// inject the images from a known historic release for the purposes of building
// branches of those releases.
type assembleReleaseStep struct {
	config      *api.ReleaseTagConfiguration
	name        string
	resources   api.ResourceConfiguration
	imageClient imageclientset.ImageV1Interface
	podClient   steps.PodClient
	saGetter    coreclientset.ServiceAccountsGetter
	rbacClient  rbacclientset.RbacV1Interface
	artifactDir string
	jobSpec     *api.JobSpec
	dryLogger   *steps.DryLogger
}

func (s *assembleReleaseStep) Inputs(dry bool) (api.InputDefinition, error) {
	return nil, nil
}

func (s *assembleReleaseStep) Run(ctx context.Context, dry bool) error {
	return results.ForReason("assembling_release").ForError(s.run(ctx, dry))
}

func setupReleaseImageStream(namespace string, saGetter coreclientset.ServiceAccountsGetter, rbacClient rbacclientset.RbacV1Interface, imageClient imageclientset.ImageV1Interface, dryLogger *steps.DryLogger, dry bool) (string, error) {
	sa := &coreapi.ServiceAccount{
		ObjectMeta: meta.ObjectMeta{
			Name:      "ci-operator",
			Namespace: namespace,
		},
	}

	role := &rbacapi.Role{
		ObjectMeta: meta.ObjectMeta{
			Name:      "ci-operator-image",
			Namespace: namespace,
		},
		Rules: []rbacapi.PolicyRule{
			{
				APIGroups: []string{"", "image.openshift.io"},
				Resources: []string{"imagestreams/layers"},
				Verbs:     []string{"get", "update"},
			},
			{
				APIGroups: []string{"", "image.openshift.io"},
				Resources: []string{"imagestreams", "imagestreamtags"},
				Verbs:     []string{"create", "get", "list", "update", "watch"},
			},
		},
	}

	roleBinding := &rbacapi.RoleBinding{
		ObjectMeta: meta.ObjectMeta{
			Name:      "ci-operator-image",
			Namespace: namespace,
		},
		Subjects: []rbacapi.Subject{{Kind: "ServiceAccount", Name: "ci-operator", Namespace: namespace}},
		RoleRef: rbacapi.RoleRef{
			Kind: "Role",
			Name: "ci-operator-image",
		},
	}

	if dry {
		dryLogger.AddObject(sa.DeepCopyObject())
		dryLogger.AddObject(role.DeepCopyObject())
		dryLogger.AddObject(roleBinding.DeepCopyObject())
		return "", nil
	}

	if _, err := saGetter.ServiceAccounts(namespace).Create(sa); err != nil && !errors.IsAlreadyExists(err) {
		return "", results.ForReason("creating_service_account").WithError(err).Errorf("could not create service account 'ci-operator' for: %v", err)
	}

	if _, err := rbacClient.Roles(namespace).Create(role); err != nil && !errors.IsAlreadyExists(err) {
		return "", results.ForReason("creating_roles").WithError(err).Errorf("could not create role 'ci-operator-image' for: %v", err)
	}

	if _, err := rbacClient.RoleBindings(namespace).Create(roleBinding); err != nil && !errors.IsAlreadyExists(err) {
		return "", results.ForReason("binding_roles").WithError(err).Errorf("could not create role binding 'ci-operator-image' for: %v", err)
	}

	// ensure the image stream exists
	release, err := imageClient.ImageStreams(namespace).Create(&imageapi.ImageStream{
		ObjectMeta: meta.ObjectMeta{
			Name: "release",
		},
	})
	if err != nil {
		if !errors.IsAlreadyExists(err) {
			return "", err
		}
		release, err = imageClient.ImageStreams(namespace).Get("release", meta.GetOptions{})
		if err != nil {
			return "", results.ForReason("creating_release_stream").ForError(err)
		}
	}
	return release.Status.PublicDockerImageRepository, nil
}

func (s *assembleReleaseStep) run(ctx context.Context, dry bool) error {
	releaseImageStreamRepo, err := setupReleaseImageStream(s.jobSpec.Namespace(), s.saGetter, s.rbacClient, s.imageClient, s.dryLogger, dry)
	if err != nil {
		return err
	}
	if dry {
		return nil
	}

	streamName := api.StableStreamFor(s.name)
	var stable *imageapi.ImageStream
	var cvo string
	cvoExists := false
	cliExists := false
	// waiting for importing the images
	// 2~3 mins: build01 on aws imports images from api.ci on gcp
	importCtx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	if err := wait.PollImmediateUntil(10*time.Second, func() (bool, error) {
		stable, err = s.imageClient.ImageStreams(s.jobSpec.Namespace()).Get(streamName, meta.GetOptions{})
		if err != nil {
			return false, err
		}
		cvo, cvoExists = util.ResolvePullSpec(stable, "cluster-version-operator", true)
		_, cliExists = util.ResolvePullSpec(stable, "cli", true)
		ret := cvoExists && cliExists
		if !ret {
			log.Printf("waiting for importing cluster-version-operator and cli ...")
		}
		return ret, nil
	}, importCtx.Done()); err != nil {
		if wait.ErrWaitTimeout == err {
			if !cliExists {
				return results.ForReason("missing_cli").WithError(err).Errorf("no 'cli' image was tagged into the %s stream, that image is required for building a release", streamName)
			}
			log.Printf("No %s release image necessary, %s image stream does not include a cluster-version-operator image", s.name, streamName)
			return nil
		} else if errors.IsNotFound(err) {
			// if a user sets IMAGE_FORMAT=... we skip importing the image stream contents, which prevents us from
			// generating a release image.
			log.Printf("No %s release image can be generated when the %s image stream was skipped", s.name, streamName)
			return nil
		}
		return results.ForReason("missing_release").WithError(err).Errorf("could not resolve imagestream %s: %v", streamName, err)
	}

	destination := fmt.Sprintf("%s:%s", releaseImageStreamRepo, s.name)
	log.Printf("Create release image %s", destination)
	podConfig := steps.PodStepConfiguration{
		SkipLogs: true,
		As:       fmt.Sprintf("release-%s", s.name),
		From: api.ImageStreamTagReference{
			Name: streamName,
			Tag:  "cli",
		},
		ServiceAccountName: "ci-operator",
		ArtifactDir:        "/tmp/artifacts",
		Commands: fmt.Sprintf(`
set -euo pipefail
export HOME=/tmp
oc registry login
oc adm release new --max-per-registry=32 -n %q --from-image-stream %q --to-image-base %q --to-image %q
oc adm release extract --from=%q --to=/tmp/artifacts/release-payload-%s
`, s.jobSpec.Namespace(), streamName, cvo, destination, destination, s.name),
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

	step := steps.PodStep("release", podConfig, resources, s.podClient, s.artifactDir, s.jobSpec, s.dryLogger)

	return results.ForReason("creating_release").ForError(step.Run(ctx, dry))
}

func (s *assembleReleaseStep) Requires() []api.StepLink {
	if s.name == api.LatestStableName {
		return []api.StepLink{api.ImagesReadyLink()}
	}
	return []api.StepLink{api.StableImagesLink(s.name)}
}

func (s *assembleReleaseStep) Creates() []api.StepLink {
	return []api.StepLink{api.ReleasePayloadImageLink(s.name)}
}

func EnvVarFor(name string) string {
	return fmt.Sprintf("RELEASE_IMAGE_%s", strings.ToUpper(name))
}

func (s *assembleReleaseStep) Provides() (api.ParameterMap, api.StepLink) {
	return providesFor(s.name, s.imageClient, s.jobSpec)
}

func providesFor(name string, imageClient imageclientset.ImageV1Interface, spec *api.JobSpec) (api.ParameterMap, api.StepLink) {
	return api.ParameterMap{
		EnvVarFor(name): func() (string, error) {
			is, err := imageClient.ImageStreams(spec.Namespace()).Get("release", meta.GetOptions{})
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
			ref, image := findStatusTag(is, name)
			if len(image) > 0 {
				return fmt.Sprintf("%s@%s", registry, image), nil
			}
			if ref == nil && findSpecTag(is, name) == nil {
				return "", nil
			}
			return fmt.Sprintf("%s:%s", registry, name), nil
		},
	}, api.ReleasePayloadImageLink(name)
}

func (s *assembleReleaseStep) Name() string {
	return fmt.Sprintf("[release:%s]", s.name)
}

func (s *assembleReleaseStep) Description() string {
	return "Create the release image containing all images built by this job"
}

// AssembleReleaseStep builds a new update payload image based on the cluster version operator
// and the operators defined in the release configuration.
func AssembleReleaseStep(name string, config *api.ReleaseTagConfiguration, resources api.ResourceConfiguration,
	podClient steps.PodClient, imageClient imageclientset.ImageV1Interface, saGetter coreclientset.ServiceAccountsGetter,
	rbacClient rbacclientset.RbacV1Interface, artifactDir string, jobSpec *api.JobSpec, dryLogger *steps.DryLogger) api.Step {
	return &assembleReleaseStep{
		config:      config,
		name:        name,
		resources:   resources,
		podClient:   podClient,
		imageClient: imageClient,
		saGetter:    saGetter,
		rbacClient:  rbacClient,
		artifactDir: artifactDir,
		jobSpec:     jobSpec,
		dryLogger:   dryLogger,
	}
}
