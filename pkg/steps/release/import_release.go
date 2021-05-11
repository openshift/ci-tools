package release

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/sirupsen/logrus"

	coreapi "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/retry"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	imagev1 "github.com/openshift/api/image/v1"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/results"
	"github.com/openshift/ci-tools/pkg/steps"
	"github.com/openshift/ci-tools/pkg/steps/utils"
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
	append     bool
	resources  api.ResourceConfiguration
	client     steps.PodClient
	jobSpec    *api.JobSpec
	pullSecret *coreapi.Secret
}

func (s *importReleaseStep) Inputs() (api.InputDefinition, error) {
	return api.InputDefinition{s.pullSpec}, nil
}

func (*importReleaseStep) Validate() error { return nil }

func (s *importReleaseStep) Run(ctx context.Context) error {
	return results.ForReason("importing_release").ForError(s.run(ctx))
}

func (s *importReleaseStep) run(ctx context.Context) error {
	_, err := setupReleaseImageStream(ctx, s.jobSpec.Namespace(), s.client)
	if err != nil {
		return err
	}

	streamName := api.ReleaseStreamFor(s.name)

	logrus.Infof("Importing release image %s.", s.name)

	// create the stable image stream with lookup policy so we have a place to put our imported images
	err = s.client.Create(ctx, &imagev1.ImageStream{
		ObjectMeta: meta.ObjectMeta{
			Namespace: s.jobSpec.Namespace(),
			Name:      streamName,
		},
		Spec: imagev1.ImageStreamSpec{
			LookupPolicy: imagev1.ImageLookupPolicy{
				Local: true,
			},
		},
	})
	if err != nil && !kerrors.IsAlreadyExists(err) {
		return fmt.Errorf("could not create stable imagestream: %w", err)
	}

	// tag the release image in and let it import
	var pullSpec string

	// retry importing the image a few times because we might race against establishing credentials/roles
	// and be unable to import images on the same cluster
	streamImport := &imagev1.ImageStreamImport{
		ObjectMeta: meta.ObjectMeta{
			Namespace: s.jobSpec.Namespace(),
			Name:      "release",
		},
		Spec: imagev1.ImageStreamImportSpec{
			Import: true,
			Images: []imagev1.ImageImportSpec{
				{
					To: &coreapi.LocalObjectReference{
						Name: s.name,
					},
					From: coreapi.ObjectReference{
						Kind: "DockerImage",
						Name: s.pullSpec,
					},
					ReferencePolicy: imagev1.TagReferencePolicy{
						Type: imagev1.LocalTagReferencePolicy,
					},
				},
			},
		},
	}
	if err := wait.ExponentialBackoff(wait.Backoff{Steps: 4, Duration: 1 * time.Second, Factor: 2}, func() (bool, error) {
		if err := s.client.Create(ctx, streamImport); err != nil {
			if kerrors.IsConflict(err) {
				return false, nil
			}
			if kerrors.IsForbidden(err) {
				// the ci-operator expects to have POST /imagestreamimports in the namespace of the job
				logrus.Warnf("Unable to lock %s to an image digest pull spec, you don't have permission to access the necessary API.", utils.ReleaseImageEnv(s.name))
				return false, nil
			}
			return false, err
		}
		image := streamImport.Status.Images[0]
		if image.Image == nil {
			return false, nil
		}
		pullSpec = streamImport.Status.Images[0].Image.DockerImageReference
		return true, nil
	}); err != nil {
		return fmt.Errorf("unable to import %s release image: %w", s.name, err)
	}

	// override anything in stable with the contents of the release image
	// TODO: should we allow underride for things we built in pipeline?
	// get the CLI image from the payload (since we need it to run oc adm release extract)
	target := fmt.Sprintf("release-images-%s", s.name)
	targetCLI := fmt.Sprintf("%s-cli", target)
	if _, err := steps.RunPod(context.TODO(), s.client, &coreapi.Pod{
		ObjectMeta: meta.ObjectMeta{
			Name:      targetCLI,
			Namespace: s.jobSpec.Namespace(),
			Labels:    map[string]string{Label: s.name},
		},
		Spec: coreapi.PodSpec{
			RestartPolicy: coreapi.RestartPolicyNever,
			Containers: []coreapi.Container{
				{
					Name:    "release",
					Image:   fmt.Sprintf("release:%s", s.name), // the cluster will resolve this relative ref for us when we create Pods with it
					Command: []string{"/bin/sh", "-c", "cluster-version-operator image cli > /dev/termination-log"},
				},
			},
		},
	}); err != nil {
		return fmt.Errorf("unable to find the 'cli' image in the provided release image: %w", err)
	}
	pod := &coreapi.Pod{}
	if err := s.client.Get(context.TODO(), ctrlruntimeclient.ObjectKey{Namespace: s.jobSpec.Namespace(), Name: targetCLI}, pod); err != nil {
		return fmt.Errorf("unable to extract the 'cli' image from the release image: %w", err)
	}
	if len(pod.Status.ContainerStatuses) == 0 || pod.Status.ContainerStatuses[0].State.Terminated == nil {
		return fmt.Errorf("unable to extract the 'cli' image from the release image: %w", err)
	}
	cliImage := pod.Status.ContainerStatuses[0].State.Terminated.Message

	// tag the cli image into stable so we use the correct pull secrets from the namespace
	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		return s.client.Update(ctx, &imagev1.ImageStreamTag{
			ObjectMeta: meta.ObjectMeta{
				Namespace: s.jobSpec.Namespace(),
				Name:      fmt.Sprintf("%s:cli", streamName),
			},
			Tag: &imagev1.TagReference{
				ReferencePolicy: imagev1.TagReferencePolicy{
					Type: imagev1.LocalTagReferencePolicy,
				},
				From: &coreapi.ObjectReference{
					Kind: "DockerImage",
					Name: cliImage,
				},
			},
		})
	}); err != nil {
		return fmt.Errorf("unable to tag the 'cli' image into the stable stream: %w", err)
	}

	var secrets []*api.Secret
	if s.pullSecret != nil {
		secrets = []*api.Secret{{
			Name:      s.pullSecret.Name,
			MountPath: "/pull",
		}}
	}
	commands := fmt.Sprintf(`
set -euo pipefail
export HOME=/tmp
mkdir -p $HOME/.docker
if [[ -d /pull ]]; then
	cp /pull/.dockerconfigjson $HOME/.docker/config.json
fi
oc registry login
oc adm release extract --from=%q --file=image-references > ${ARTIFACT_DIR}/%s
# while release creation may happen more than once in the lifetime of a test
# namespace, only one release creation Pod will ever run at once. Therefore,
# while actions editing the output ConfigMap may race if done from ci-operator
# itself, these actions cannot race from this Pod, as all active ci-operator
# processes will launch and wait for but one release Pod. Here, we need to
# delete any previously-existing ConfigMap if we're re-importing the release.
if oc get configmap release-%s; then
	oc delete configmap release-%s
fi
oc create configmap release-%s --from-file=%s.yaml=${ARTIFACT_DIR}/%s
`, pullSpec, target, target, target, target, target, target)

	// run adm release extract and grab the raw image-references from the payload
	podConfig := steps.PodStepConfiguration{
		SkipLogs: true,
		As:       target,
		From: api.ImageStreamTagReference{
			Name: streamName,
			Tag:  "cli",
		},
		Labels:             map[string]string{Label: s.name},
		ServiceAccountName: "ci-operator",
		Secrets:            secrets,
		Commands:           commands,
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
	step := steps.PodStep("release", podConfig, resources, s.client, s.jobSpec, nil)
	if err := step.Run(ctx); err != nil {
		return err
	}

	// read the contents from the configmap we created
	var configMap coreapi.ConfigMap
	if err := s.client.Get(ctx, ctrlruntimeclient.ObjectKey{Namespace: s.jobSpec.Namespace(), Name: fmt.Sprintf("release-%s", target)}, &configMap); err != nil {
		return fmt.Errorf("could not fetch extracted release %s: %w", target, err)
	}

	isContents, ok := configMap.Data[fmt.Sprintf("%s.yaml", target)]
	if !ok {
		return fmt.Errorf("no imagestream data found in release configMap for %s: %w", target, err)
	}
	var releaseIS imagev1.ImageStream
	if err := json.Unmarshal([]byte(isContents), &releaseIS); err != nil {
		return fmt.Errorf("unable to decode release image stream: %w", err)
	}
	if releaseIS.Kind != "ImageStream" || releaseIS.APIVersion != "image.openshift.io/v1" {
		return fmt.Errorf("unexpected image stream contents: %w", err)
	}

	// update the stable image stream to have all of the tags from the payload
	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		stable := &imagev1.ImageStream{}
		if err := s.client.Get(ctx, ctrlruntimeclient.ObjectKey{Namespace: s.jobSpec.Namespace(), Name: streamName}, stable); err != nil {
			return fmt.Errorf("could not resolve imagestream %s: %w", streamName, err)
		}

		existing := sets.NewString()
		tags := make([]imagev1.TagReference, 0, len(releaseIS.Spec.Tags)+len(stable.Spec.Tags))
		for _, tag := range releaseIS.Spec.Tags {
			existing.Insert(tag.Name)
			tag.ReferencePolicy.Type = imagev1.LocalTagReferencePolicy
			tags = append(tags, tag)
		}
		for _, tag := range stable.Spec.Tags {
			if existing.Has(tag.Name) {
				continue
			}
			existing.Insert(tag.Name)
			tag.ReferencePolicy.Type = imagev1.LocalTagReferencePolicy
			tags = append(tags, tag)
		}
		stable.Spec.Tags = tags

		return s.client.Update(ctx, stable)
	}); err != nil {
		return fmt.Errorf("unable to update stable image stream with release tags: %w", err)
	}

	// loop until we observe all images have successfully imported, kicking import if a particular
	// tag fails
	var waiting map[string]int64
	stable := &imagev1.ImageStream{}
	if err := wait.Poll(3*time.Second, 15*time.Minute, func() (bool, error) {
		if err := s.client.Get(ctx, ctrlruntimeclient.ObjectKey{Namespace: s.jobSpec.Namespace(), Name: streamName}, stable); err != nil {
			return false, fmt.Errorf("could not resolve imagestream %s: %w", streamName, err)
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
			if err = s.client.Update(ctx, stable); err != nil {
				logrus.WithError(err).Error("Failed requesting re-import of failed release image stream.")
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
			return fmt.Errorf("unable to import image stream %s: %w", streamName, err)
		}
		var tagImportErrorMessages []string
		for tag := range waiting {
			msg := "- " + tag
			if tagRef := findSpecTagReference(stable, tag); tagRef != nil && tagRef.From != nil {
				msg = msg + " from " + tagRef.From.Name
			}
			tagImportErrorMessages = append(tagImportErrorMessages, msg)
		}
		sort.Strings(tagImportErrorMessages)
		return fmt.Errorf("the following tags from the release could not be imported to %s after five minutes:\n%s", streamName, strings.Join(tagImportErrorMessages, "\n"))
	}

	logrus.Infof("Imported release %s created at %s with %d images to tag release:%s", releaseIS.Name, releaseIS.CreationTimestamp, len(releaseIS.Spec.Tags), s.name)
	return nil
}

func findSpecTagReference(is *imagev1.ImageStream, tag string) *imagev1.TagReference {
	for i, t := range is.Spec.Tags {
		if t.Name != tag {
			continue
		}
		return &is.Spec.Tags[i]
	}
	return nil
}

func hasFailedImportCondition(conditions []imagev1.TagEventCondition, generation int64) bool {
	for _, condition := range conditions {
		if condition.Generation >= generation && condition.Type == imagev1.ImportSuccess && condition.Status == coreapi.ConditionFalse {
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
		if s.name == api.LatestReleaseName {
			return []api.StepLink{api.ImagesReadyLink()}
		}
		return []api.StepLink{api.ReleaseImagesLink(api.LatestReleaseName)}
	}
	// we don't depend on anything as we will populate
	// the stable streams with our images.
	return []api.StepLink{}
}

func (s *importReleaseStep) Creates() []api.StepLink {
	creates := []api.StepLink{api.ReleasePayloadImageLink(s.name)}
	// TODO: remove this, we need it for the collision case
	// so that only one step creates this link and we don't
	// create a cyclic graph
	if !s.append {
		creates = append(creates, api.ReleaseImagesLink(s.name))
	}
	return creates
}

func (s *importReleaseStep) Provides() api.ParameterMap {
	return api.ParameterMap{
		utils.ReleaseImageEnv(s.name): utils.ImageDigestFor(s.client, s.jobSpec.Namespace, api.ReleaseImageStream, s.name),
	}
}

func (s *importReleaseStep) Name() string {
	return fmt.Sprintf("[release:%s]", s.name)
}

func (s *importReleaseStep) Description() string {
	return fmt.Sprintf("Import the release payload %q from an external source", s.name)
}

func (s *importReleaseStep) Objects() []ctrlruntimeclient.Object {
	return s.client.Objects()
}

// ImportReleaseStep imports an existing update payload image
func ImportReleaseStep(name, pullSpec string, append bool, resources api.ResourceConfiguration,
	client steps.PodClient,
	jobSpec *api.JobSpec, pullSecret *coreapi.Secret) api.Step {
	return &importReleaseStep{
		name:       name,
		pullSpec:   pullSpec,
		append:     append,
		resources:  resources,
		client:     client,
		jobSpec:    jobSpec,
		pullSecret: pullSecret,
	}
}
