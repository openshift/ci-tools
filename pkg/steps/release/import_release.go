package release

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/blang/semver"
	"github.com/sirupsen/logrus"

	coreapi "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/retry"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	imagev1 "github.com/openshift/api/image/v1"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/kubernetes"
	"github.com/openshift/ci-tools/pkg/results"
	"github.com/openshift/ci-tools/pkg/steps"
	"github.com/openshift/ci-tools/pkg/steps/utils"
	"github.com/openshift/ci-tools/pkg/util"
)

// importReleaseStep is responsible for importing release images from
// external image streams for use with tests that need to install or
// upgrade a cluster. It uses the `cli` image within the image stream to
// create the image and pushes it to a `release` image stream. As output
// it provides the environment variable RELEASE_IMAGE_<name> which can be
// used by tests that invoke the installer.
//
// Since release images describe a set of images, when we import a release
// image, we treat its contents as inputs we must expand into the `stable-<name>`
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
	name            string
	nodeName        string
	target          string
	referencePolicy imagev1.TagReferencePolicyType
	source          ReleaseSource
	// append determines if we wait for other processes to create images first
	append     bool
	resources  api.ResourceConfiguration
	client     kubernetes.PodClient
	jobSpec    *api.JobSpec
	pullSecret *coreapi.Secret
	// overrideCLIReleaseExtractImage is given for non-amd64 releases
	overrideCLIReleaseExtractImage *coreapi.ObjectReference

	// originalPullSpec stores the original value before resolving the release to pass as an env var for multi-stage steps to utilize
	originalPullSpec string
}

func (s *importReleaseStep) Inputs() (api.InputDefinition, error) {
	input, err := s.source.Input(context.Background())
	return api.InputDefinition{input}, err
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
	pullSpec, err := s.source.PullSpec(ctx)
	if err != nil {
		return err
	}
	logrus.WithField("name", s.name).Debugf("setting originalPullSpec to: %s for multi-stage steps to reference", pullSpec)
	s.originalPullSpec = pullSpec
	// retry importing the image a few times because we might race against establishing credentials/roles
	// and be unable to import images on the same cluster
	if newPullSpec, err := utils.ImportTagWithRetries(ctx, s.client, s.jobSpec.Namespace(), "release", s.name, pullSpec, api.ImageStreamImportRetries); err != nil {
		return fmt.Errorf("unable to import %s release image: %w", s.name, err)
	} else {
		logrus.WithField("pullSpec", pullSpec).WithField("newPullSpec", newPullSpec).WithField("name", s.name).
			Debugf("Got the pull spec for release image")
		pullSpec = newPullSpec
	}

	// override anything in stable with the contents of the release image
	// TODO: should we allow underride for things we built in pipeline?
	// get the CLI image from the payload (since we need it to run oc adm release extract)
	target := fmt.Sprintf("release-images-%s", s.name)

	cliImage, err := s.getCLIImage(ctx, target, streamName)
	if err != nil {
		return fmt.Errorf("failed to get CLI image: %w", err)
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
export XDG_RUNTIME_DIR=/tmp/run
mkdir -p $HOME/.docker "${XDG_RUNTIME_DIR}"
if [[ -d /pull ]]; then
	cp /pull/.dockerconfigjson $HOME/.docker/config.json
fi
oc registry login --to $HOME/.docker/config.json
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
		WaitFlags:          util.SkipLogs,
		As:                 target,
		From:               *cliImage,
		Labels:             map[string]string{Label: s.name},
		NodeName:           s.nodeName,
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

	var prefix string
	version, err := semver.Parse(releaseIS.ObjectMeta.Name)
	if err != nil {
		logrus.WithError(err).Errorf("Failed to parse release %s ImageStream name %s as a semantic version.", pullSpec, releaseIS.ObjectMeta.Name)
	} else {
		prefix = fmt.Sprintf("%d.%d.%d-0", version.Major, version.Minor, version.Patch)
	}

	// update the stable image stream to have all of the tags from the payload
	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		stable := &imagev1.ImageStream{}
		if err := s.client.Get(ctx, ctrlruntimeclient.ObjectKey{Namespace: s.jobSpec.Namespace(), Name: streamName}, stable); err != nil {
			return fmt.Errorf("could not resolve imagestream %s: %w", streamName, err)
		}

		if prefix != "" {
			if stable.ObjectMeta.Annotations == nil {
				stable.ObjectMeta.Annotations = make(map[string]string, 1)
			}
			if _, ok := stable.ObjectMeta.Annotations[api.ReleaseConfigAnnotation]; !ok {
				stable.ObjectMeta.Annotations[api.ReleaseConfigAnnotation] = fmt.Sprintf(`{"name": "%s"}`, prefix)
			}
		}

		referencePolicy := imagev1.SourceTagReferencePolicy
		if s.referencePolicy == imagev1.LocalTagReferencePolicy {
			referencePolicy = s.referencePolicy
		}
		existing := sets.New[string]()
		tags := make([]imagev1.TagReference, 0, len(releaseIS.Spec.Tags)+len(stable.Spec.Tags))
		for _, tag := range releaseIS.Spec.Tags {
			existing.Insert(tag.Name)
			tag.ReferencePolicy.Type = referencePolicy
			tag.ImportPolicy.ImportMode = imagev1.ImportModePreserveOriginal
			tags = append(tags, tag)
		}
		for _, tag := range stable.Spec.Tags {
			if existing.Has(tag.Name) {
				continue
			}
			existing.Insert(tag.Name)
			tag.ReferencePolicy.Type = referencePolicy
			tag.ImportPolicy.ImportMode = imagev1.ImportModePreserveOriginal
			tags = append(tags, tag)
		}
		stable.Spec.Tags = tags

		return s.client.Update(ctx, stable)
	}); err != nil {
		return fmt.Errorf("unable to update stable image stream with release tags: %w", err)
	}

	// loop until we observe all images have successfully imported, kicking import if a particular
	// tag fails
	logrus.Infof("Importing release %s created at %s with %d images to tag release:%s ...", releaseIS.Name, releaseIS.CreationTimestamp, len(releaseIS.Spec.Tags), s.name)
	if err := utils.WaitForImportingISTag(ctx, s.client, s.jobSpec.Namespace(), streamName, nil, sets.New[string](), utils.DefaultImageImportTimeout); err != nil {
		return fmt.Errorf("failed to import release %s to tag release:%s: %w", releaseIS.Name, s.name, err)
	}
	logrus.Infof("Imported release %s created at %s with %d images to tag release:%s", releaseIS.Name, releaseIS.CreationTimestamp, len(releaseIS.Spec.Tags), s.name)
	return nil
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
	env := utils.ReleaseImageEnv(s.name)
	return api.ParameterMap{
		env: utils.ImageDigestFor(s.client, s.jobSpec.Namespace, api.ReleaseImageStream, s.name),
		// Disable unparam lint as we need to confirm to this interface, but there will never be an error
		//nolint:unparam
		fmt.Sprintf("ORIGINAL_%s", env): func() (string, error) {
			return s.originalPullSpec, nil
		},
	}
}

func (s *importReleaseStep) Name() string { return s.target }

func (s *importReleaseStep) Description() string {
	return fmt.Sprintf("Import the release payload %q from an external source", s.name)
}

func (s *importReleaseStep) Objects() []ctrlruntimeclient.Object {
	return s.client.Objects()
}

// ImportReleaseStep imports an existing update payload image
func ImportReleaseStep(
	name, nodeName, target string,
	referencePolicy imagev1.TagReferencePolicyType,
	source ReleaseSource,
	append bool,
	resources api.ResourceConfiguration,
	client kubernetes.PodClient,
	jobSpec *api.JobSpec,
	pullSecret *coreapi.Secret,
	overrideCLIReleaseExtractImage *coreapi.ObjectReference) api.Step {
	return &importReleaseStep{
		name:                           name,
		nodeName:                       nodeName,
		target:                         target,
		referencePolicy:                referencePolicy,
		source:                         source,
		append:                         append,
		resources:                      resources,
		client:                         client,
		jobSpec:                        jobSpec,
		pullSecret:                     pullSecret,
		overrideCLIReleaseExtractImage: overrideCLIReleaseExtractImage,
	}
}

const overrideCLIStreamName = "amd64-cli"

func (s *importReleaseStep) getCLIImage(ctx context.Context, target, streamName string) (*api.ImageStreamTagReference, error) {
	if s.overrideCLIReleaseExtractImage != nil {

		// Setting the lookup policy on the imagestreamtag doesn't do anything, it gets happily reset to false so we have to
		// create the imagestream to be able to set it there.
		if err := s.client.Create(ctx, &imagev1.ImageStream{
			ObjectMeta: metav1.ObjectMeta{Name: overrideCLIStreamName, Namespace: s.jobSpec.Namespace()},
			Spec:       imagev1.ImageStreamSpec{LookupPolicy: imagev1.ImageLookupPolicy{Local: true}, Tags: []imagev1.TagReference{{ReferencePolicy: imagev1.TagReferencePolicy{Type: s.referencePolicy}}}},
		}); err != nil && !kerrors.IsAlreadyExists(err) {
			return nil, fmt.Errorf("failed to create %s imagestream: %w", overrideCLIStreamName, err)
		}

		referencePolicy := imagev1.SourceTagReferencePolicy
		if s.referencePolicy == imagev1.LocalTagReferencePolicy {
			referencePolicy = s.referencePolicy
		}
		streamTag := &imagev1.ImageStreamTag{
			ObjectMeta: meta.ObjectMeta{
				Namespace: s.jobSpec.Namespace(),
				Name:      overrideCLIStreamName + ":latest",
			},
			Tag: &imagev1.TagReference{
				ReferencePolicy: imagev1.TagReferencePolicy{
					Type: referencePolicy,
				},
				From: s.overrideCLIReleaseExtractImage,
			},
		}
		key := ctrlruntimeclient.ObjectKeyFromObject(streamTag)
		if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			return s.client.Update(ctx, streamTag)
		}); err != nil {
			return nil, fmt.Errorf("unable to tag the override 'cli' image into the %s:latest: %w", overrideCLIStreamName, err)
		}
		if err := wait.ExponentialBackoff(wait.Backoff{Steps: 4, Duration: 1 * time.Second, Factor: 2}, func() (bool, error) {
			if err := s.client.Get(ctx, key, streamTag); err != nil {
				if kerrors.IsNotFound(err) {
					return false, nil
				}
				return false, err
			}
			return streamTag.Tag != nil && streamTag.Tag.Generation != nil && *streamTag.Tag.Generation == streamTag.Generation, nil
		}); err != nil {
			return nil, fmt.Errorf("failed to import override CLI image into %s:latest: %w", overrideCLIStreamName, err)
		}
		return &api.ImageStreamTagReference{Name: overrideCLIStreamName, Tag: "latest"}, nil
	}

	targetCLI := fmt.Sprintf("%s-cli", target)
	if _, err := steps.RunPod(ctx, s.client, &coreapi.Pod{
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
	}, true); err != nil {
		return nil, fmt.Errorf("unable to find the 'cli' image in the provided release image: %w", err)
	}
	pod := &coreapi.Pod{}
	if err := s.client.Get(ctx, ctrlruntimeclient.ObjectKey{Namespace: s.jobSpec.Namespace(), Name: targetCLI}, pod); err != nil {
		return nil, fmt.Errorf("unable to extract the 'cli' image from the release image: %w", err)
	}
	if len(pod.Status.ContainerStatuses) == 0 || pod.Status.ContainerStatuses[0].State.Terminated == nil {
		return nil, errors.New("unable to extract the 'cli' image from the release image, pod produced no output")
	}
	cliImage := pod.Status.ContainerStatuses[0].State.Terminated.Message
	// See https://issues.redhat.com/browse/DPTP-2448 for why this is an
	// explicit URL and not simply the `:cli` tag.
	cliImageRef, err := util.ParseImageStreamTagReference(cliImage)
	if err != nil {
		return nil, err
	}
	// tag the cli image into stable so we use the correct pull secrets from the namespace
	referencePolicy := imagev1.LocalTagReferencePolicy
	if s.referencePolicy == imagev1.SourceTagReferencePolicy {
		referencePolicy = s.referencePolicy
	}
	streamTag := &imagev1.ImageStreamTag{
		ObjectMeta: meta.ObjectMeta{
			Namespace: s.jobSpec.Namespace(),
			Name:      fmt.Sprintf("%s:cli", streamName),
		},
		Tag: &imagev1.TagReference{
			ReferencePolicy: imagev1.TagReferencePolicy{
				Type: referencePolicy,
			},
			From: &coreapi.ObjectReference{
				Kind: "DockerImage",
				Name: cliImage,
			},
		},
	}
	key := ctrlruntimeclient.ObjectKeyFromObject(streamTag)
	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		return s.client.Update(ctx, streamTag)
	}); err != nil {
		return nil, fmt.Errorf("unable to tag the 'cli' image into the stable stream: %w", err)
	}

	startedWaiting := time.Now()
	if err := wait.PollImmediate(5*time.Second, 5*time.Minute+5*time.Second, func() (bool, error) {
		if err := s.client.Get(ctx, key, streamTag); err != nil {
			if kerrors.IsNotFound(err) {
				return false, nil
			}
			return false, err
		}
		populated := streamTag.Tag != nil && streamTag.Tag.Generation != nil && *streamTag.Tag.Generation == streamTag.Generation
		if streamTag.Tag == nil {
			logrus.Debug("The 'cli' image in the stable stream is not populated: streamTag.Tag is nil")
			return false, nil
		}
		if !populated {
			logrus.WithField("streamTag.Tag.Generation", streamTag.Tag.Generation).
				WithField("streamTag.Generation", streamTag.Generation).
				Debug("The 'cli' image in the stable stream is not populated")
		}
		return populated, nil
	}); err != nil {
		duration := time.Since(startedWaiting)
		return nil, fmt.Errorf("unable to wait for the 'cli' image in the stable stream to populate (waited for %s): %w", duration, err)
	}

	return &cliImageRef, nil
}
