package release

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/sirupsen/logrus"

	coreapi "k8s.io/api/core/v1"
	rbacapi "k8s.io/api/rbac/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	imageapi "github.com/openshift/api/image/v1"
	imagev1 "github.com/openshift/api/image/v1"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/kubernetes"
	"github.com/openshift/ci-tools/pkg/results"
	"github.com/openshift/ci-tools/pkg/steps"
	"github.com/openshift/ci-tools/pkg/steps/utils"
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
	config    *api.ReleaseTagConfiguration
	name      string
	nodeName  string
	resources api.ResourceConfiguration
	client    kubernetes.PodClient
	jobSpec   *api.JobSpec
}

func (s *assembleReleaseStep) Inputs() (api.InputDefinition, error) {
	return nil, nil
}

func (*assembleReleaseStep) Validate() error { return nil }

func (s *assembleReleaseStep) Run(ctx context.Context) error {
	return results.ForReason("assembling_release").ForError(s.run(ctx))
}

func setupReleaseImageStream(ctx context.Context, namespace string, client ctrlruntimeclient.Client) (string, error) {
	sa := &coreapi.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ci-operator",
			Namespace: namespace,
		},
		ImagePullSecrets: []coreapi.LocalObjectReference{
			{
				Name: api.RegistryPullCredentialsSecret,
			},
		},
	}

	role := &rbacapi.Role{
		ObjectMeta: metav1.ObjectMeta{
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
			{
				APIGroups: []string{""},
				Resources: []string{"configmaps"},
				Verbs:     []string{"create", "get", "list", "update", "delete"},
			},
		},
	}

	roleBindings := []rbacapi.RoleBinding{{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ci-operator-image",
			Namespace: namespace,
		},
		Subjects: []rbacapi.Subject{{Kind: "ServiceAccount", Name: "ci-operator", Namespace: namespace}},
		RoleRef: rbacapi.RoleRef{
			Kind: "Role",
			Name: "ci-operator-image",
		},
	}}
	if err := util.CreateRBACs(ctx, sa, role, roleBindings, client, 1*time.Second, 1*time.Minute); err != nil {
		return "", err
	}

	// ensure the image stream exists
	release := &imagev1.ImageStream{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      "release",
		},
		Spec: imagev1.ImageStreamSpec{
			LookupPolicy: imagev1.ImageLookupPolicy{
				Local: true,
			},
		},
	}
	if err := client.Create(ctx, release); err != nil {
		if !kerrors.IsAlreadyExists(err) {
			return "", err
		}
		if err := client.Get(ctx, ctrlruntimeclient.ObjectKey{Namespace: namespace, Name: "release"}, release); err != nil {
			return "", results.ForReason("creating_release_stream").ForError(err)
		}
	}
	return release.Status.PublicDockerImageRepository, nil
}

func (s *assembleReleaseStep) run(ctx context.Context) error {
	releaseImageStreamRepo, err := setupReleaseImageStream(ctx, s.jobSpec.Namespace(), s.client)
	if err != nil {
		return err
	}

	streamName := api.ReleaseStreamFor(s.name)
	stable := &imageapi.ImageStream{}
	var cvo string
	cvoExists := false
	cliExists := false
	// waiting for importing the images
	// 2~3 mins: build01 on aws imports images from api.ci on gcp
	importCtx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	if err := wait.PollImmediateUntil(10*time.Second, func() (bool, error) {
		if err := s.client.Get(ctx, ctrlruntimeclient.ObjectKey{Namespace: s.jobSpec.Namespace(), Name: streamName}, stable); err != nil {
			return false, err
		}
		cvo, cvoExists, _ = util.ResolvePullSpec(stable, "cluster-version-operator", true)
		_, cliExists, _ = util.ResolvePullSpec(stable, "cli", true)
		ret := cvoExists && cliExists
		if !ret {
			logrus.Infof("Waiting to import cluster-version-operator and cli ...")
		}
		return ret, nil
	}, importCtx.Done()); err != nil {
		if wait.ErrWaitTimeout == err {
			if !cliExists {
				return results.ForReason("missing_cli").WithError(err).Errorf("no 'cli' image was tagged into the %s stream, that image is required for building a release", streamName)
			}
			logrus.Infof("No %s release image necessary, %s image stream does not include a cluster-version-operator image", s.name, streamName)
			return nil
		} else if kerrors.IsNotFound(err) {
			// if a user sets IMAGE_FORMAT=... we skip importing the image stream contents, which prevents us from
			// generating a release image.
			logrus.Infof("No %s release image can be generated when the %s image stream was skipped", s.name, streamName)
			return nil
		}
		return results.ForReason("missing_release").WithError(err).Errorf("could not resolve imagestream %s: %v", streamName, err)
	}

	// we want to expose the release payload as a CI version that looks just like
	// the release versions for nightlies and CI release candidates
	prefix := "0.0.1-0"
	if raw, ok := stable.ObjectMeta.Annotations[releaseConfigAnnotation]; ok {
		var releaseConfig struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal([]byte(raw), &releaseConfig); err != nil {
			return results.ForReason("invalid_release").WithError(err).Errorf("could not resolve release configuration on imagestream %s: %v", streamName, err)
		}
		prefix = releaseConfig.Name
	}
	now := time.Now().UTC().Truncate(time.Second)
	version := fmt.Sprintf("%s.test-%s-%s-%s", prefix, now.Format("2006-01-02-150405"), s.jobSpec.Namespace(), s.name)

	destination := fmt.Sprintf("%s:%s", releaseImageStreamRepo, s.name)
	logrus.Infof("Creating release image %s.", destination)
	podConfig := steps.PodStepConfiguration{
		WaitFlags: util.SkipLogs,
		As:        fmt.Sprintf("release-%s", s.name),
		From: api.ImageStreamTagReference{
			Name: streamName,
			Tag:  "cli",
		},
		Labels:             map[string]string{Label: s.name},
		NodeName:           s.nodeName,
		ServiceAccountName: "ci-operator",
		Commands: fmt.Sprintf(`
set -xeuo pipefail
export HOME=/tmp
export XDG_RUNTIME_DIR=/tmp/run
mkdir -p "${XDG_RUNTIME_DIR}"
oc registry login
oc adm release new --max-per-registry=32 -n %q --from-image-stream %q --to-image-base %q --to-image %q --name %q
oc adm release extract --from=%q --to=${ARTIFACT_DIR}/release-payload-%s
`, s.jobSpec.Namespace(), streamName, cvo, destination, version, destination, s.name),
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
		return results.ForReason("creating_release").ForError(err)
	}
	logrus.Infof("Snapshot integration stream into release %s to tag %s:%s ", version, api.ReleaseImageStream, s.name)
	return nil
}

func (s *assembleReleaseStep) Requires() []api.StepLink {
	if s.config.IncludeBuiltImages {
		return []api.StepLink{api.ImagesReadyLink()}
	}
	return []api.StepLink{api.ReleaseImagesLink(s.name)}
}

func (s *assembleReleaseStep) Creates() []api.StepLink {
	return []api.StepLink{api.ReleasePayloadImageLink(s.name)}
}

func (s *assembleReleaseStep) Provides() api.ParameterMap {
	return api.ParameterMap{
		utils.ReleaseImageEnv(s.name): utils.ImageDigestFor(s.client, s.jobSpec.Namespace, api.ReleaseImageStream, s.name),
	}
}

func (s *assembleReleaseStep) Name() string { return s.config.TargetName(s.name) }

func (s *assembleReleaseStep) Description() string {
	return fmt.Sprintf("Create the release image %q containing all images built by this job", s.name)
}

func (s *assembleReleaseStep) Objects() []ctrlruntimeclient.Object {
	return s.client.Objects()
}

// AssembleReleaseStep builds a new update payload image based on the cluster version operator
// and the operators defined in the release configuration.
func AssembleReleaseStep(name, nodeName string, config *api.ReleaseTagConfiguration, resources api.ResourceConfiguration,
	client kubernetes.PodClient, jobSpec *api.JobSpec) api.Step {
	return &assembleReleaseStep{
		config:    config,
		name:      name,
		nodeName:  nodeName,
		resources: resources,
		client:    client,
		jobSpec:   jobSpec,
	}
}
