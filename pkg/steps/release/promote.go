package release

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/sirupsen/logrus"

	coreapi "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
	prowapi "k8s.io/test-infra/prow/apis/prowjobs/v1"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	imagev1 "github.com/openshift/api/image/v1"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/kubernetes"
	"github.com/openshift/ci-tools/pkg/kubernetes/pkg/credentialprovider"
	"github.com/openshift/ci-tools/pkg/release/prerelease"
	"github.com/openshift/ci-tools/pkg/results"
	"github.com/openshift/ci-tools/pkg/steps"
)

// promotionStep will tag a full release suite
// of images out to the configured namespace.
type promotionStep struct {
	name              string
	configuration     *api.ReleaseBuildConfiguration
	requiredImages    sets.Set[string]
	jobSpec           *api.JobSpec
	client            kubernetes.PodClient
	pushSecret        *coreapi.Secret
	registry          string
	mirrorFunc        func(source, target string, tag api.ImageStreamTagReference, date string, imageMirror map[string]string)
	targetNameFunc    func(string, api.PromotionTarget) string
	nodeArchitectures []string
}

func (s *promotionStep) Inputs() (api.InputDefinition, error) {
	return nil, nil
}

func (*promotionStep) Validate() error { return nil }

func (s *promotionStep) Run(ctx context.Context) error {
	return results.ForReason("promoting_images").ForError(s.run(ctx))
}

func mainRefs(refs *prowapi.Refs, extra []prowapi.Refs) *prowapi.Refs {
	if refs != nil {
		return refs
	}
	if len(extra) > 0 {
		return &extra[0]
	}
	return nil
}

func (s *promotionStep) run(ctx context.Context) error {
	opts := []PromotedTagsOption{
		WithRequiredImages(s.requiredImages),
	}
	logger := logrus.WithField("name", s.name)

	if refs := mainRefs(s.jobSpec.Refs, s.jobSpec.ExtraRefs); refs != nil {
		opts = append(opts, WithCommitSha(refs.BaseSHA))
	}

	tags, names := PromotedTagsWithRequiredImages(s.configuration, opts...)
	if len(names) == 0 {
		logger.Info("Nothing to promote, skipping...")
		return nil
	}

	logger.Infof("Promoting tags to %s: %s", s.targets(), strings.Join(sets.List(names), ", "))
	pipeline := &imagev1.ImageStream{}
	if err := s.client.Get(ctx, ctrlruntimeclient.ObjectKey{
		Namespace: s.jobSpec.Namespace(),
		Name:      api.PipelineImageStream,
	}, pipeline); err != nil {
		return fmt.Errorf("could not resolve pipeline imagestream: %w", err)
	}

	timeStr := time.Now().Format("20060102150405")
	imageMirrorTarget, namespaces := getImageMirrorTarget(tags, pipeline, s.registry, timeStr, s.mirrorFunc)
	if len(imageMirrorTarget) == 0 {
		logger.Info("Nothing to promote, skipping...")
		return nil
	}

	// in some cases like when we are called by the ci-chat-bot we may need to create namespaces
	// in general, we do not expect to be able to do this, so we only do it best-effort
	if err := s.ensureNamespaces(ctx, namespaces); err != nil {
		logger.WithError(err).Warn("Failed to ensure namespaces to promote to in central registry.")
	}

	version, err := prerelease.Stable4LatestMajorMinor(&http.Client{})
	if err != nil {
		logrus.WithError(err).Warn("Failed to determine the sable release version, using 4.14 instead")
		version = "4.14"
	}

	if _, err := steps.RunPod(ctx, s.client, getPromotionPod(imageMirrorTarget, timeStr, s.jobSpec.Namespace(), s.name, version, s.nodeArchitectures)); err != nil {
		return fmt.Errorf("unable to run promotion pod: %w", err)
	}
	return nil
}

func (s *promotionStep) ensureNamespaces(ctx context.Context, namespaces sets.Set[string]) error {
	if len(namespaces) == 0 {
		return nil
	}
	// Used primarily (only?) by the chatbot and we likely do not have the permission to create
	// namespaces (nor are we expected to).
	if s.configuration.PromotionConfiguration.RegistryOverride != "" {
		return nil
	}
	var dockercfg credentialprovider.DockerConfigJSON
	if err := json.Unmarshal(s.pushSecret.Data[coreapi.DockerConfigJsonKey], &dockercfg); err != nil {
		return fmt.Errorf("failed to deserialize push secret: %w", err)
	}

	appCIDockercfg, hasAppCIDockercfg := dockercfg.Auths[api.ServiceDomainAPPCIRegistry]
	if !hasAppCIDockercfg {
		return fmt.Errorf("push secret has no entry for %s", api.ServiceDomainAPPCIRegistry)
	}

	appCIKubeconfig := &rest.Config{Host: api.APPCIKubeAPIURL, BearerToken: appCIDockercfg.Password}
	client, err := corev1client.NewForConfig(appCIKubeconfig)
	if err != nil {
		return fmt.Errorf("failed to construct kubeconfig: %w", err)
	}

	for namespace := range namespaces {
		var success bool
		var errs []error
		for i := 0; i < 3; i++ {
			if _, err := client.Namespaces().Create(ctx, &coreapi.Namespace{ObjectMeta: meta.ObjectMeta{Name: namespace}}, meta.CreateOptions{}); err == nil || apierrors.IsAlreadyExists(err) {
				success = true
				break
			} else {
				errs = append(errs, err)
			}
		}
		if !success {
			return fmt.Errorf("failed to create namespace %s with retries: %w", namespace, utilerrors.NewAggregate(errs))
		}
	}

	return nil
}

func getImageMirrorTarget(tags map[string][]api.ImageStreamTagReference, pipeline *imagev1.ImageStream, registry string, time string, mirrorFunc func(source, target string, tag api.ImageStreamTagReference, time string, imageMirror map[string]string)) (map[string]string, sets.Set[string]) {
	if pipeline == nil {
		return nil, nil
	}
	imageMirror := map[string]string{}
	// Will this ever include more than one?
	namespaces := sets.Set[string]{}
	for src, dsts := range tags {
		dockerImageReference := findDockerImageReference(pipeline, src)
		if dockerImageReference == "" {
			continue
		}
		dockerImageReference = getPublicImageReference(dockerImageReference, pipeline.Status.PublicDockerImageRepository)
		for _, dst := range dsts {
			mirrorFunc(dockerImageReference, fmt.Sprintf("%s/%s", registry, dst.ISTagName()), dst, time, imageMirror)
			namespaces.Insert(dst.Namespace)
		}
	}
	if len(imageMirror) == 0 {
		return nil, nil
	}
	if registry == api.QuayOpenShiftCIRepo {
		namespaces = nil
	}
	return imageMirror, namespaces
}

func getPublicImageReference(dockerImageReference, publicDockerImageRepository string) string {
	if !strings.Contains(dockerImageReference, ":5000") {
		return dockerImageReference
	}
	splits := strings.Split(publicDockerImageRepository, "/")
	if len(splits) < 2 {
		// This should never happen
		logrus.Warnf("Failed to get hostname from publicDockerImageRepository: %s.", publicDockerImageRepository)
		return dockerImageReference
	}
	publicHost := splits[0]
	splits = strings.Split(dockerImageReference, "/")
	if len(splits) < 2 {
		// This should never happen
		logrus.Warnf("Failed to get hostname from dockerImageReference: %s.", dockerImageReference)
		return dockerImageReference
	}
	return strings.Replace(dockerImageReference, splits[0], publicHost, 1)
}

func getPromotionPod(imageMirrorTarget map[string]string, timeStr string, namespace string, name string, cliVersion string, nodeArchitectures []string) *coreapi.Pod {
	keys := make([]string, 0, len(imageMirrorTarget))
	for k := range imageMirrorTarget {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var images []string
	var pruneImages []string
	for _, k := range keys {
		if strings.Contains(k, fmt.Sprintf("%s_prune_", timeStr)) {
			pruneImages = append(pruneImages, fmt.Sprintf("%s=%s", imageMirrorTarget[k], k))
		} else {
			images = append(images, fmt.Sprintf("%s=%s", imageMirrorTarget[k], k))
		}
	}
	command := []string{"/bin/sh", "-c"}
	mirrorTagsCommand := fmt.Sprintf("oc image mirror --keep-manifest-list --registry-config=%s --continue-on-error=true --max-per-registry=20 %s", filepath.Join(api.RegistryPushCredentialsCICentralSecretMountPath, coreapi.DockerConfigJsonKey), strings.Join(images, " "))
	var args []string
	if len(pruneImages) > 0 {
		mirrorPruneTagsCommand := fmt.Sprintf("oc image mirror --keep-manifest-list --registry-config=%s --continue-on-error=true --max-per-registry=20 %s", filepath.Join(api.RegistryPushCredentialsCICentralSecretMountPath, coreapi.DockerConfigJsonKey), strings.Join(pruneImages, " "))
		args = append(args, fmt.Sprintf("%s || true", mirrorPruneTagsCommand))
	}
	args = append(args, mirrorTagsCommand)
	args = []string{strings.Join(args, "\n")}

	image := fmt.Sprintf("%s/%s/%s:cli", api.DomainForService(api.ServiceRegistry), "ocp", cliVersion)
	nodeSelector := map[string]string{"kubernetes.io/arch": "amd64"}

	archs := sets.New[string](nodeArchitectures...)
	if !archs.Has("amd64") && archs.Has("arm64") {
		image = fmt.Sprintf("%s/%s/4.14:cli", api.DomainForService(api.ServiceRegistry), "ocp-arm64")
		nodeSelector = map[string]string{"kubernetes.io/arch": "arm64"}
	}

	return &coreapi.Pod{
		ObjectMeta: meta.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: coreapi.PodSpec{
			NodeSelector:  nodeSelector,
			RestartPolicy: coreapi.RestartPolicyNever,
			Containers: []coreapi.Container{
				{
					Name:    "promotion",
					Image:   image,
					Command: command,
					Args:    args,
					VolumeMounts: []coreapi.VolumeMount{
						{
							Name:      "push-secret",
							MountPath: "/etc/push-secret",
							ReadOnly:  true,
						},
					},
				},
			},
			Volumes: []coreapi.Volume{
				{
					Name: "push-secret",
					VolumeSource: coreapi.VolumeSource{
						Secret: &coreapi.SecretVolumeSource{SecretName: api.RegistryPushCredentialsCICentralSecret},
					},
				},
			},
		},
	}
}

// findDockerImageReference returns DockerImageReference, the string that can be used to pull this image,
// to a tag if it exists in the ImageStream's Spec
func findDockerImageReference(is *imagev1.ImageStream, tag string) string {
	for _, t := range is.Status.Tags {
		if t.Tag != tag {
			continue
		}
		if len(t.Items) == 0 {
			return ""
		}
		return t.Items[0].DockerImageReference
	}
	return ""
}

// toPromote determines the mapping of local tag to external tag which should be promoted
func toPromote(config api.PromotionTarget, images []api.ProjectDirectoryImageBuildStepConfiguration, requiredImages sets.Set[string]) (map[string]string, sets.Set[string]) {
	tagsByDst := map[string]string{}
	names := sets.New[string]()

	if config.Disabled {
		return tagsByDst, names
	}

	for _, image := range images {
		// if the image is required or non-optional, include it in promotion
		tag := string(image.To)
		if requiredImages.Has(tag) || !image.Optional {
			tagsByDst[tag] = tag
			names.Insert(tag)
		}
	}
	for _, tag := range config.ExcludedImages {
		delete(tagsByDst, tag)
		names.Delete(tag)
	}
	for dst, src := range config.AdditionalImages {
		tagsByDst[dst] = src
		names.Insert(dst)
	}

	return tagsByDst, names
}

// PromotedTags returns the tags that are being promoted for the given ReleaseBuildConfiguration
func PromotedTags(configuration *api.ReleaseBuildConfiguration) []api.ImageStreamTagReference {
	var tags []api.ImageStreamTagReference
	mapping, _ := PromotedTagsWithRequiredImages(configuration)
	for _, dest := range mapping {
		tags = append(tags, dest...)
	}
	sort.Slice(tags, func(i, j int) bool {
		return tags[i].ISTagName() < tags[j].ISTagName()
	})
	return tags
}

type PromotedTagsOptions struct {
	requiredImages sets.Set[string]
	commitSha      string
}

type PromotedTagsOption func(options *PromotedTagsOptions)

// WithRequiredImages ensures that the images are promoted, even if they would otherwise be skipped.
func WithRequiredImages(images sets.Set[string]) PromotedTagsOption {
	return func(options *PromotedTagsOptions) {
		options.requiredImages = images
	}
}

// WithCommitSha ensures that images are tagged by the commit SHA as well as any other options in the configuration.
func WithCommitSha(commitSha string) PromotedTagsOption {
	return func(options *PromotedTagsOptions) {
		options.commitSha = commitSha
	}
}

// PromotedTagsWithRequiredImages returns the tags that are being promoted for the given ReleaseBuildConfiguration
// accounting for the list of required images. Promoted tags are mapped by the source tag in the pipeline ImageStream
// we will promote to the output.
func PromotedTagsWithRequiredImages(configuration *api.ReleaseBuildConfiguration, options ...PromotedTagsOption) (map[string][]api.ImageStreamTagReference, sets.Set[string]) {
	opts := &PromotedTagsOptions{
		requiredImages: sets.New[string](),
	}
	for _, opt := range options {
		opt(opts)
	}

	promotedTags := map[string][]api.ImageStreamTagReference{}
	requiredImages := sets.Set[string]{}

	if configuration == nil || configuration.PromotionConfiguration == nil {
		return promotedTags, requiredImages
	}

	for _, target := range api.PromotionTargets(configuration.PromotionConfiguration) {
		tags, names := toPromote(target, configuration.Images, opts.requiredImages)
		requiredImages.Insert(names.UnsortedList()...)
		for dst, src := range tags {
			var tag api.ImageStreamTagReference
			if target.Name != "" {
				tag = api.ImageStreamTagReference{
					Namespace: target.Namespace,
					Name:      target.Name,
					Tag:       dst,
				}
			} else { // promotion.Tag must be set
				tag = api.ImageStreamTagReference{
					Namespace: target.Namespace,
					Name:      dst,
					Tag:       target.Tag,
				}
			}
			promotedTags[src] = append(promotedTags[src], tag)
			if target.TagByCommit && opts.commitSha != "" {
				promotedTags[src] = append(promotedTags[src], api.ImageStreamTagReference{
					Namespace: target.Namespace,
					Name:      dst,
					Tag:       opts.commitSha,
				})
			}
		}
	}
	// promote the binary build if one exists and this isn't disabled
	if configuration.BinaryBuildCommands != "" && !configuration.PromotionConfiguration.DisableBuildCache {
		promotedTags[string(api.PipelineImageStreamTagReferenceBinaries)] = append(promotedTags[string(api.PipelineImageStreamTagReferenceBinaries)], api.BuildCacheFor(configuration.Metadata))
	}
	for _, tags := range promotedTags {
		sort.Slice(tags, func(i, j int) bool {
			return tags[i].ISTagName() < tags[j].ISTagName()
		})
	}
	return promotedTags, requiredImages
}

func (s *promotionStep) Requires() []api.StepLink {
	return []api.StepLink{api.AllStepsLink()}
}

func (s *promotionStep) Creates() []api.StepLink {
	return []api.StepLink{}
}

func (s *promotionStep) Provides() api.ParameterMap {
	return nil
}

func (s *promotionStep) Name() string { return fmt.Sprintf("[%s]", s.name) }

func (s *promotionStep) targets() string {
	var targets []string
	for _, target := range api.PromotionTargets(s.configuration.PromotionConfiguration) {
		targets = append(targets, s.targetNameFunc(s.registry, target))
	}
	return strings.Join(targets, ", ")
}

func (s *promotionStep) Description() string {
	return fmt.Sprintf("Promote built images into the release image streams: %s", s.targets())
}

func (s *promotionStep) Objects() []ctrlruntimeclient.Object {
	return s.client.Objects()
}

// PromotionStep copies tags from the pipeline image stream to the destination defined in the promotion config.
// If the source tag does not exist it is silently skipped.
func PromotionStep(
	name string,
	configuration *api.ReleaseBuildConfiguration,
	requiredImages sets.Set[string],
	jobSpec *api.JobSpec,
	client kubernetes.PodClient,
	pushSecret *coreapi.Secret,
	registry string,
	mirrorFunc func(source, target string, tag api.ImageStreamTagReference, date string, imageMirror map[string]string),
	targetNameFunc func(string, api.PromotionTarget) string,
	nodeArchitectures []string,
) api.Step {
	return &promotionStep{
		name:              name,
		configuration:     configuration,
		requiredImages:    requiredImages,
		jobSpec:           jobSpec,
		client:            client,
		pushSecret:        pushSecret,
		registry:          registry,
		mirrorFunc:        mirrorFunc,
		targetNameFunc:    targetNameFunc,
		nodeArchitectures: nodeArchitectures,
	}
}
