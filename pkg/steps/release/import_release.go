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
	coreclientset "k8s.io/client-go/kubernetes/typed/core/v1"
	rbacclientset "k8s.io/client-go/kubernetes/typed/rbac/v1"
	"k8s.io/client-go/util/retry"

	imageapi "github.com/openshift/api/image/v1"
	imageclientset "github.com/openshift/client-go/image/clientset/versioned/typed/image/v1"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/results"
	"github.com/openshift/ci-tools/pkg/steps"
)

// importReleaseStep is responsible for importing release images from
// external image streams for use with tests that need to install or
// upgrade a cluster. It uses the `cli` image within the image stream to
// create the image and pushes it to a `release` image stream. As output
// it provides the environment variable RELEASE_IMAGE_<name> which can be
// used by tests that invoke the installer.
//
// Since release images describe a set of images, when we import a release
// image, we treat it's contents as inputs we must expand into the `stable-<name>`
// image stream. This is because our test scenarios need access not
// just to the release image, but also to the images in that release image
// like installer, cli, or tests. To make it easy for a CI job to install from
// an older release image, we need to extract the 'installer' image into the
// same location that we would expect if it came from a tag_specification.
// The images inside of a release image override any images built or imported
// into the job, which allows you to have an empty tag_specification and
// inject the images from a known historic release for the purposes of building
// branches of those releases.
type importReleaseStep struct {
	// name is the name of the release we're importing, like 'latest'
	name string
	// pullSpec is the fully-resolved pull spec of the release payload image we are importing
	pullSpec string
	// append determines if we wait for other processes to create images first
	append      bool
	resources   api.ResourceConfiguration
	imageClient imageclientset.ImageV1Interface
	podClient   steps.PodClient
	saGetter    coreclientset.ServiceAccountsGetter
	rbacClient  rbacclientset.RbacV1Interface
	artifactDir string
	jobSpec     *api.JobSpec
	dryLogger   *steps.DryLogger
}

func (s *importReleaseStep) Inputs(dry bool) (api.InputDefinition, error) {
	return nil, nil
}

func (s *importReleaseStep) Run(ctx context.Context, dry bool) error {
	return results.ForReason("importing_release").ForError(s.run(ctx, dry))
}

func (s *importReleaseStep) run(ctx context.Context, dry bool) error {
	_, err := setupReleaseImageStream(s.jobSpec.Namespace(), s.saGetter, s.rbacClient, s.imageClient, s.dryLogger, dry)
	if err != nil {
		return err
	}

	streamName := api.StableStreamFor(s.name)

	if dry {
		return nil
	}

	log.Printf("Importing release image %s", s.name)

	// create the stable image stream with lookup policy so we have a place to put our imported images
	_, err = s.imageClient.ImageStreams(s.jobSpec.Namespace()).Create(&imageapi.ImageStream{
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
		result, err := s.imageClient.ImageStreamImports(s.jobSpec.Namespace()).Create(&imageapi.ImageStreamImport{
			ObjectMeta: meta.ObjectMeta{
				Name: "release",
			},
			Spec: imageapi.ImageStreamImportSpec{
				Import: true,
				Images: []imageapi.ImageImportSpec{
					{
						To: &coreapi.LocalObjectReference{
							Name: s.name,
						},
						From: coreapi.ObjectReference{
							Kind: "DockerImage",
							Name: s.pullSpec,
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
				// the ci-operator expects to have POST /imagestreamimports in the namespace of the job
				log.Printf("warning: Unable to lock %s to an image digest pull spec, you don't have permission to access the necessary API.", EnvVarFor(s.name))
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
		return fmt.Errorf("unable to import %s release image: %v", s.name, err)
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
	target := fmt.Sprintf("release-images-%s", s.name)
	targetCLI := fmt.Sprintf("%s-cli", target)
	if err := steps.RunPod(context.TODO(), s.podClient, &coreapi.Pod{
		ObjectMeta: meta.ObjectMeta{
			Name:      targetCLI,
			Namespace: s.jobSpec.Namespace(),
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
	pod, err := s.podClient.Pods(s.jobSpec.Namespace()).Get(targetCLI, meta.GetOptions{})
	if err != nil {
		return fmt.Errorf("unable to extract the 'cli' image from the release image: %v", err)
	}
	if len(pod.Status.ContainerStatuses) == 0 || pod.Status.ContainerStatuses[0].State.Terminated == nil {
		return fmt.Errorf("unable to extract the 'cli' image from the release image: %v", err)
	}
	cliImage := pod.Status.ContainerStatuses[0].State.Terminated.Message

	// tag the cli image into stable so we use the correct pull secrets from the namespace
	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		_, err := s.imageClient.ImageStreamTags(s.jobSpec.Namespace()).Update(&imageapi.ImageStreamTag{
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
		ServiceAccountName: "ci-operator",
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
		stable, err := s.imageClient.ImageStreams(s.jobSpec.Namespace()).Get(streamName, meta.GetOptions{})
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

		_, err = s.imageClient.ImageStreams(s.jobSpec.Namespace()).Update(stable)
		return err
	}); err != nil {
		return fmt.Errorf("unable to update stable image stream with release tags: %v", err)
	}

	// loop until we observe all images have successfully imported, kicking import if a particular
	// tag fails
	var waiting map[string]int64
	if err := wait.Poll(3*time.Second, 15*time.Minute, func() (bool, error) {
		stable, err := s.imageClient.ImageStreams(s.jobSpec.Namespace()).Get(streamName, meta.GetOptions{})
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
			_, err = s.imageClient.ImageStreams(s.jobSpec.Namespace()).Update(stable)
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

	log.Printf("Imported release %s created at %s with %d images to tag release:%s", releaseIS.Name, releaseIS.CreationTimestamp, len(releaseIS.Spec.Tags), s.name)
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

func (s *importReleaseStep) Requires() []api.StepLink {
	// TODO: remove this, we need it for backwards compat
	// but should provide a separate, direct means for
	// users to import images they care about rather than
	// having two steps overwrite each other on import
	if s.append {
		if s.name == api.LatestStableName {
			return []api.StepLink{api.ImagesReadyLink()}
		}
		return []api.StepLink{api.StableImagesLink(api.LatestStableName)}
	}
	// we don't depend on anything as we will populate
	// the stable streams with our images.
	return []api.StepLink{}
}

func (s *importReleaseStep) Creates() []api.StepLink {
	return []api.StepLink{api.ReleasePayloadImageLink(s.name)}
}

func (s *importReleaseStep) Provides() (api.ParameterMap, api.StepLink) {
	return providesFor(s.name, s.imageClient, s.jobSpec)
}

func (s *importReleaseStep) Name() string {
	return fmt.Sprintf("[release:%s]", s.name)
}

func (s *importReleaseStep) Description() string {
	return "Import a release payload from an external source"
}

// ImportReleaseStep imports an existing update payload image
func ImportReleaseStep(name, pullSpec string, append bool, resources api.ResourceConfiguration,
	podClient steps.PodClient, imageClient imageclientset.ImageV1Interface, saGetter coreclientset.ServiceAccountsGetter,
	rbacClient rbacclientset.RbacV1Interface, artifactDir string, jobSpec *api.JobSpec, dryLogger *steps.DryLogger) api.Step {
	return &importReleaseStep{
		name:        name,
		pullSpec:    pullSpec,
		append:      append,
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
