package release

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"path/filepath"
	"sort"
	"strings"
	"time"

	coreapi "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
	rbacclientset "k8s.io/client-go/kubernetes/typed/rbac/v1"
	"k8s.io/client-go/util/retry"

	imageapi "github.com/openshift/api/image/v1"
	imageclientset "github.com/openshift/client-go/image/clientset/versioned/typed/image/v1"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/steps"
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
	config      api.ReleaseTagConfiguration
	latest      bool
	params      api.Parameters
	releaseSpec string
	resources   api.ResourceConfiguration
	imageClient imageclientset.ImageV1Interface
	podClient   steps.PodClient
	rbacClient  rbacclientset.RbacV1Interface
	artifactDir string
	jobSpec     *api.JobSpec
	dryLogger   *steps.DryLogger
}

func (s *assembleReleaseStep) Inputs(ctx context.Context, dry bool) (api.InputDefinition, error) {
	if val, _ := s.params.Get(s.envVar()); len(val) > 0 {
		result, err := s.imageClient.ImageStreamImports(s.config.Namespace).Create(&imageapi.ImageStreamImport{
			ObjectMeta: meta.ObjectMeta{
				Name: "release-import",
			},
			Spec: imageapi.ImageStreamImportSpec{
				Images: []imageapi.ImageImportSpec{
					{
						From: coreapi.ObjectReference{
							Kind: "DockerImage",
							Name: val,
						},
					},
				},
			},
		})
		if err != nil {
			if errors.IsForbidden(err) {
				// the ci-operator expects to have POST /imagestreamimports in the namespace of the tag spec
				log.Printf("warning: Unable to lock %s to an image digest pull spec, you don't have permission to access the necessary API.", s.envVar())
				return api.InputDefinition{val}, nil
			}
			return nil, err
		}
		image := result.Status.Images[0]
		if image.Image == nil {
			log.Printf("warning: Unable to lock %s to an image digest pull spec due to an import error (%s): %s.", s.envVar(), image.Status.Reason, image.Status.Message)
			return api.InputDefinition{val}, nil
		}
		log.Printf("Resolved release:%s %s", s.tag(), image.Image.DockerImageReference)
		s.releaseSpec = image.Image.DockerImageReference
		return api.InputDefinition{image.Image.Name}, nil
	}
	return nil, nil
}

func (s *assembleReleaseStep) Run(ctx context.Context, dry bool) error {
	if dry {
		return nil
	}

	tag := s.tag()
	streamName := s.streamName()

	// ensure the image stream exists
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

	// if the user specified an input env var, we tag it in instead of generating it
	if s.params.HasInput(s.envVar()) {
		providedImage, err := s.params.Get(s.envVar())
		if err != nil {
			return fmt.Errorf("cannot retrieve %s: %v", s.envVar(), err)
		}
		if len(providedImage) == 0 {
			log.Printf("No %s release image necessary because empty input variable provided", tag)
			return nil
		}
		if len(s.releaseSpec) > 0 {
			providedImage = s.releaseSpec
		}
		return s.importFromReleaseImage(ctx, dry, providedImage)
	}

	stable, err := s.imageClient.ImageStreams(s.jobSpec.Namespace).Get(streamName, meta.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			// if a user sets IMAGE_FORMAT=... we skip importing the image stream contents, which prevents us from
			// generating a release image.
			log.Printf("No %s release image can be generated when the %s image stream was skipped", tag, streamName)
			return nil
		}
		return fmt.Errorf("could not resolve imagestream %s: %v", streamName, err)
	}
	cvo, ok := resolvePullSpec(stable, "cluster-version-operator", true)
	if !ok {
		log.Printf("No %s release image necessary, %s image stream does not include a cluster-version-operator image", tag, streamName)
		return nil
	}
	if _, ok := resolvePullSpec(stable, "cli", true); !ok {
		return fmt.Errorf("no 'cli' image was tagged into the %s stream, that image is required for building a release", streamName)
	}

	destination := fmt.Sprintf("%s:%s", release.Status.PublicDockerImageRepository, tag)
	log.Printf("Create release image %s", destination)
	podConfig := steps.PodStepConfiguration{
		SkipLogs: true,
		As:       fmt.Sprintf("release-%s", tag),
		From: api.ImageStreamTagReference{
			Name: streamName,
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
`, s.jobSpec.Namespace, streamName, cvo, destination, destination, tag),
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

	return step.Run(ctx, dry)
}

// importFromReleaseImage uses the provided release image and updates the stable / release streams as
// appropriate with the contents of the payload so that downstream components are using the correct images.
// The most common case is to use the correct installer image, tests, and cli commands.
func (s *assembleReleaseStep) importFromReleaseImage(ctx context.Context, dry bool, providedImage string) error {
	tag := s.tag()
	streamName := s.streamName()

	if dry {
		return nil
	}

	log.Printf("Importing release image %s", tag)

	// create the stable image stream with lookup policy so we have a place to put our imported images
	_, err := s.imageClient.ImageStreams(s.jobSpec.Namespace).Create(&imageapi.ImageStream{
		ObjectMeta: meta.ObjectMeta{
			Name: streamName,
		},
		Spec: imageapi.ImageStreamSpec{
			LookupPolicy: imageapi.ImageLookupPolicy{
				Local: true,
			},
		},
	})
	if err != nil && !errors.IsAlreadyExists(err) {
		return fmt.Errorf("could not create stable imagestreamtag: %v", err)
	}
	// tag the release image in and let it import
	var pullSpec string

	// retry importing the image a few times because we might race against establishing credentials/roles
	// and be unable to import images on the same cluster
	if err := wait.ExponentialBackoff(wait.Backoff{Steps: 4, Duration: 1 * time.Second, Factor: 2}, func() (bool, error) {
		result, err := s.imageClient.ImageStreamImports(s.jobSpec.Namespace).Create(&imageapi.ImageStreamImport{
			ObjectMeta: meta.ObjectMeta{
				Name: "release",
			},
			Spec: imageapi.ImageStreamImportSpec{
				Import: true,
				Images: []imageapi.ImageImportSpec{
					{
						To: &coreapi.LocalObjectReference{
							Name: tag,
						},
						From: coreapi.ObjectReference{
							Kind: "DockerImage",
							Name: providedImage,
						},
					},
				},
			},
		})
		if err != nil {
			if errors.IsConflict(err) {
				return false, nil
			}
			if errors.IsForbidden(err) {
				// the ci-operator expects to have POST /imagestreamimports in the namespace of the tag spec
				log.Printf("warning: Unable to lock %s to an image digest pull spec, you don't have permission to access the necessary API.", s.envVar())
				return false, nil
			}
			return false, err
		}
		image := result.Status.Images[0]
		if image.Image == nil {
			return false, nil
		}
		pullSpec = result.Status.Images[0].Image.DockerImageReference
		return true, nil
	}); err != nil {
		return fmt.Errorf("unable to import %s release image: %v", tag, err)
	}

	// override anything in stable with the contents of the release image
	// TODO: should we allow underride for things we built in pipeline?
	artifactDir := s.artifactDir
	if len(artifactDir) == 0 {
		var err error
		artifactDir, err = ioutil.TempDir("", "payload-images")
		if err != nil {
			return fmt.Errorf("unable to create temporary artifact dir for payload extraction")
		}
	}

	// get the CLI image from the payload (since we need it to run oc adm release extract)
	target := fmt.Sprintf("release-images-%s", tag)
	targetCLI := fmt.Sprintf("%s-cli", target)
	if err := steps.RunPod(s.podClient, &coreapi.Pod{
		ObjectMeta: meta.ObjectMeta{
			Name:      targetCLI,
			Namespace: s.jobSpec.Namespace,
		},
		Spec: coreapi.PodSpec{
			RestartPolicy: coreapi.RestartPolicyNever,
			Containers: []coreapi.Container{
				{
					Name:    "release",
					Image:   pullSpec,
					Command: []string{"/bin/sh", "-c", "cluster-version-operator image cli > /dev/termination-log"},
				},
			},
		},
	}); err != nil {
		return fmt.Errorf("unable to find the 'cli' image in the provided release image: %v", err)
	}
	pod, err := s.podClient.Pods(s.jobSpec.Namespace).Get(targetCLI, meta.GetOptions{})
	if err != nil {
		return fmt.Errorf("unable to extract the 'cli' image from the release image: %v", err)
	}
	if len(pod.Status.ContainerStatuses) == 0 || pod.Status.ContainerStatuses[0].State.Terminated == nil {
		return fmt.Errorf("unable to extract the 'cli' image from the release image: %v", err)
	}
	cliImage := pod.Status.ContainerStatuses[0].State.Terminated.Message

	// tag the cli image into stable so we use the correct pull secrets from the namespace
	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		_, err := s.imageClient.ImageStreamTags(s.jobSpec.Namespace).Update(&imageapi.ImageStreamTag{
			ObjectMeta: meta.ObjectMeta{
				Name: fmt.Sprintf("%s:cli", streamName),
			},
			Tag: &imageapi.TagReference{
				ReferencePolicy: imageapi.TagReferencePolicy{
					Type: imageapi.LocalTagReferencePolicy,
				},
				From: &coreapi.ObjectReference{
					Kind: "DockerImage",
					Name: cliImage,
				},
			},
		})
		return err
	}); err != nil {
		return fmt.Errorf("unable to tag the 'cli' image into the stable stream: %v", err)
	}

	// run adm release extract and grab the raw image-references from the payload
	podConfig := steps.PodStepConfiguration{
		SkipLogs: true,
		As:       target,
		From: api.ImageStreamTagReference{
			Name: streamName,
			Tag:  "cli",
		},
		ServiceAccountName: "builder",
		ArtifactDir:        "/tmp/artifacts",
		Commands: fmt.Sprintf(`
set -euo pipefail
export HOME=/tmp
oc registry login
oc adm release extract --from=%q --file=image-references > /tmp/artifacts/%s
`, pullSpec, target),
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
	step := steps.PodStep("release", podConfig, resources, s.podClient, artifactDir, s.jobSpec, s.dryLogger)
	if err := step.Run(ctx, false); err != nil {
		return err
	}

	// read the contents from the artifacts directory
	isContents, err := ioutil.ReadFile(filepath.Join(artifactDir, podConfig.As, target))
	if err != nil {
		return fmt.Errorf("unable to read release image stream: %v", err)
	}
	var releaseIS imageapi.ImageStream
	if err := json.Unmarshal(isContents, &releaseIS); err != nil {
		return fmt.Errorf("unable to decode release image stream: %v", err)
	}
	if releaseIS.Kind != "ImageStream" || releaseIS.APIVersion != "image.openshift.io/v1" {
		return fmt.Errorf("unexpected image stream contents: %v", err)
	}

	// update the stable image stream to have all of the tags from the payload
	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		stable, err := s.imageClient.ImageStreams(s.jobSpec.Namespace).Get(streamName, meta.GetOptions{})
		if err != nil {
			return fmt.Errorf("could not resolve imagestream %s: %v", streamName, err)
		}

		existing := sets.NewString()
		tags := make([]imageapi.TagReference, 0, len(releaseIS.Spec.Tags)+len(stable.Spec.Tags))
		for _, tag := range releaseIS.Spec.Tags {
			existing.Insert(tag.Name)
			tag.ReferencePolicy.Type = imageapi.LocalTagReferencePolicy
			tags = append(tags, tag)
		}
		for _, tag := range stable.Spec.Tags {
			if existing.Has(tag.Name) {
				continue
			}
			existing.Insert(tag.Name)
			tag.ReferencePolicy.Type = imageapi.LocalTagReferencePolicy
			tags = append(tags, tag)
		}
		stable.Spec.Tags = tags

		_, err = s.imageClient.ImageStreams(s.jobSpec.Namespace).Update(stable)
		return err
	}); err != nil {
		return fmt.Errorf("unable to update stable image stream with release tags: %v", err)
	}

	// loop until we observe all images have successfully imported, kicking import if a particular
	// tag fails
	var waiting map[string]int64
	if err := wait.Poll(3*time.Second, 5*time.Minute, func() (bool, error) {
		stable, err := s.imageClient.ImageStreams(s.jobSpec.Namespace).Get(streamName, meta.GetOptions{})
		if err != nil {
			return false, fmt.Errorf("could not resolve imagestream %s: %v", streamName, err)
		}
		generations := make(map[string]int64)
		for _, tag := range stable.Spec.Tags {
			if tag.Generation == nil || *tag.Generation == 0 {
				continue
			}
			generations[tag.Name] = *tag.Generation
		}
		updates := false
		for _, event := range stable.Status.Tags {
			gen, ok := generations[event.Tag]
			if !ok {
				continue
			}
			if len(event.Items) > 0 && event.Items[0].Generation >= gen {
				delete(generations, event.Tag)
				continue
			}
			if hasFailedImportCondition(event.Conditions, gen) {
				zero := int64(0)
				findSpecTagReference(stable, event.Tag).Generation = &zero
				updates = true
			}
		}
		if updates {
			_, err = s.imageClient.ImageStreams(s.jobSpec.Namespace).Update(stable)
			if err != nil {
				log.Printf("error requesting re-import of failed release image stream: %v", err)
			}
			return false, nil
		}
		if len(generations) == 0 {
			return true, nil
		}
		waiting = generations
		return false, nil
	}); err != nil {
		if len(waiting) == 0 || err != wait.ErrWaitTimeout {
			return fmt.Errorf("unable to import image stream %s: %v", streamName, err)
		}
		var tags []string
		for tag := range waiting {
			tags = append(tags, tag)
		}
		sort.Strings(tags)
		return fmt.Errorf("the following tags from the release could not be imported to %s after five minutes: %s", streamName, strings.Join(tags, ", "))
	}

	log.Printf("Imported release %s created at %s with %d images to tag release:%s", releaseIS.Name, releaseIS.CreationTimestamp, len(releaseIS.Spec.Tags), tag)
	return nil
}

func findSpecTagReference(is *imageapi.ImageStream, tag string) *imageapi.TagReference {
	for i, t := range is.Spec.Tags {
		if t.Name != tag {
			continue
		}
		return &is.Spec.Tags[i]
	}
	return nil
}

func hasFailedImportCondition(conditions []imageapi.TagEventCondition, generation int64) bool {
	for _, condition := range conditions {
		if condition.Generation >= generation && condition.Type == imageapi.ImportSuccess && condition.Status == coreapi.ConditionFalse {
			return true
		}
	}
	return false
}

func (s *assembleReleaseStep) Done() (bool, error) {
	return false, nil
}

func (s *assembleReleaseStep) Requires() []api.StepLink {
	// if our prereq is provided, we only depend on the stable and stable-initial
	// image streams to be populated
	if s.params.HasInput(s.envVar()) {
		return []api.StepLink{api.ReleaseImagesLink()}
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

func (s *assembleReleaseStep) streamName() string {
	if s.latest {
		return api.StableImageStream
	}
	return fmt.Sprintf("%s-initial", api.StableImageStream)
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
			ref, image := findStatusTag(is, tag)
			if len(image) > 0 {
				return fmt.Sprintf("%s@%s", registry, image), nil
			}
			if ref == nil && findSpecTag(is, tag) == nil {
				return "", nil
			}
			return fmt.Sprintf("%s:%s", registry, tag), nil
		},
	}, api.ReleasePayloadImageLink(api.PipelineImageStreamTagReference(tag))
}

func (s *assembleReleaseStep) Name() string {
	return fmt.Sprintf("[release:%s]", s.tag())
}

func (s *assembleReleaseStep) Description() string {
	if s.latest {
		return "Create the release image containing all images built by this job"
	}
	return "Create initial release image from the images that were in the input tag_specification"
}

// AssembleReleaseStep builds a new update payload image based on the cluster version operator
// and the operators defined in the release configuration.
func AssembleReleaseStep(latest bool, config api.ReleaseTagConfiguration, params api.Parameters, resources api.ResourceConfiguration, podClient steps.PodClient, imageClient imageclientset.ImageV1Interface, artifactDir string, jobSpec *api.JobSpec, dryLogger *steps.DryLogger) api.Step {
	return &assembleReleaseStep{
		config:      config,
		latest:      latest,
		params:      params,
		resources:   resources,
		podClient:   podClient,
		imageClient: imageClient,
		artifactDir: artifactDir,
		jobSpec:     jobSpec,
		dryLogger:   dryLogger,
	}
}
