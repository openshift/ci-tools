package release

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

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
	"github.com/openshift/ci-tools/pkg/results"
	"github.com/openshift/ci-tools/pkg/steps"
	"github.com/openshift/ci-tools/pkg/util"
)

// promotionStep will tag a full release suite
// of images out to the configured namespace.
type promotionStep struct {
	configuration  *api.ReleaseBuildConfiguration
	requiredImages sets.String
	jobSpec        *api.JobSpec
	client         kubernetes.PodClient
	pushSecret     *coreapi.Secret
}

func targetName(config api.PromotionConfiguration) string {
	if len(config.Name) > 0 {
		return fmt.Sprintf("%s/%s:${component}", config.Namespace, config.Name)
	}
	return fmt.Sprintf("%s/${component}:%s", config.Namespace, config.Tag)
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

	if refs := mainRefs(s.jobSpec.Refs, s.jobSpec.ExtraRefs); refs != nil {
		opts = append(opts, WithCommitSha(refs.BaseSHA))
	}
	tags, names := PromotedTagsWithRequiredImages(s.configuration, opts...)
	if len(names) == 0 {
		logrus.Info("Nothing to promote, skipping...")
		return nil
	}

	logrus.Infof("Promoting tags to %s: %s", targetName(*s.configuration.PromotionConfiguration), strings.Join(names.List(), ", "))
	pipeline := &imagev1.ImageStream{}
	if err := s.client.Get(ctx, ctrlruntimeclient.ObjectKey{
		Namespace: s.jobSpec.Namespace(),
		Name:      api.PipelineImageStream,
	}, pipeline); err != nil {
		return fmt.Errorf("could not resolve pipeline imagestream: %w", err)
	}

	imageMirrorTarget, namespaces := getImageMirrorTarget(tags, pipeline, registryDomain(s.configuration.PromotionConfiguration))
	if len(imageMirrorTarget) == 0 {
		logrus.Info("Nothing to promote, skipping...")
		return nil
	}

	// in some cases like when we are called by the ci-chat-bot we may need to create namespaces
	// in general, we do not expect to be able to do this, so we only do it best-effort
	if err := s.ensureNamespaces(ctx, namespaces); err != nil {
		logrus.WithError(err).Warn("Failed to ensure namespaces to promote to in central registry.")
	}

	if _, err := steps.RunPod(ctx, s.client, getPromotionPod(imageMirrorTarget, s.jobSpec.Namespace())); err != nil {
		return fmt.Errorf("unable to run promotion pod: %w", err)
	}
	return nil
}

func (s *promotionStep) ensureNamespaces(ctx context.Context, namespaces sets.String) error {
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

// registryDomain determines the domain of the registry we promote to
func registryDomain(configuration *api.PromotionConfiguration) string {
	registry := api.DomainForService(api.ServiceRegistry)
	if configuration.RegistryOverride != "" {
		registry = configuration.RegistryOverride
	}
	return registry
}

func getImageMirrorTarget(tags map[string][]api.MultiArchImageStreamTagReference, pipeline *imagev1.ImageStream, registry string) (srcTargetMap map[string]string, namespaces sets.String) {
	if pipeline == nil {
		return nil, nil
	}
	imageMirror := map[string]string{}
	// Will this ever include more than one?
	namespaces = sets.String{}
	for src, dsts := range tags {
		dockerImageReference := findDockerImageReference(pipeline, src)
		if dockerImageReference == "" {
			continue
		}
		dockerImageReference = getPublicImageReference(dockerImageReference, pipeline.Status.PublicDockerImageRepository)
		for _, dst := range dsts {
			imageMirror[fmt.Sprintf("%s/%s", registry, dst.ISTagName())] = dockerImageReference
			namespaces.Insert(dst.ResolveNamespace())
		}
	}
	if len(imageMirror) == 0 {
		return nil, nil
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

func getPromotionPod(imageMirrorTarget map[string]string, namespace string) *coreapi.Pod {
	keys := make([]string, 0, len(imageMirrorTarget))
	for k := range imageMirrorTarget {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var images []string
	for _, k := range keys {
		images = append(images, fmt.Sprintf("%s=%s", imageMirrorTarget[k], k))
	}
	command := []string{"/bin/sh", "-c"}
	args := []string{fmt.Sprintf("oc image mirror --registry-config=%s --continue-on-error=true --max-per-registry=20 %s", filepath.Join(api.RegistryPushCredentialsCICentralSecretMountPath, coreapi.DockerConfigJsonKey), strings.Join(images, " "))}
	return &coreapi.Pod{
		ObjectMeta: meta.ObjectMeta{
			Name:      "promotion",
			Namespace: namespace,
		},
		Spec: coreapi.PodSpec{
			RestartPolicy: coreapi.RestartPolicyNever,
			Containers: []coreapi.Container{
				{
					Name:    "promotion",
					Image:   fmt.Sprintf("%s/%s/4.8:cli", api.DomainForService(api.ServiceRegistry), util.ResolveMultiArchNamespaceFor("ocp")),
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
func toPromote(config api.PromotionConfiguration, images []api.ProjectDirectoryImageBuildStepConfiguration, requiredImages sets.String) (map[string]string, sets.String) {
	tagsByDst := map[string]string{}
	names := sets.NewString()

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
func PromotedTags(configuration *api.ReleaseBuildConfiguration) []api.MultiArchImageStreamTagReference {
	var tags []api.MultiArchImageStreamTagReference
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
	requiredImages sets.String
	commitSha      string
}

type PromotedTagsOption func(options *PromotedTagsOptions)

// WithRequiredImages ensures that the images are promoted, even if they would otherwise be skipped.
func WithRequiredImages(images sets.String) PromotedTagsOption {
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
func PromotedTagsWithRequiredImages(configuration *api.ReleaseBuildConfiguration, options ...PromotedTagsOption) (map[string][]api.MultiArchImageStreamTagReference, sets.String) {
	opts := &PromotedTagsOptions{
		requiredImages: sets.NewString(),
	}
	for _, opt := range options {
		opt(opts)
	}

	if configuration == nil || configuration.PromotionConfiguration == nil || configuration.PromotionConfiguration.Disabled {
		return nil, nil
	}
	tags, names := toPromote(*configuration.PromotionConfiguration, configuration.Images, opts.requiredImages)
	promotedTags := map[string][]api.MultiArchImageStreamTagReference{}
	for dst, src := range tags {
		var tag api.MultiArchImageStreamTagReference
		if configuration.PromotionConfiguration.Name != "" {
			tag = api.MultiArchImageStreamTagReference{
				ImageStreamTagReference: api.ImageStreamTagReference{
					Namespace: configuration.PromotionConfiguration.Namespace,
					Name:      configuration.PromotionConfiguration.Name,
					Tag:       dst,
				},
			}
		} else { // promotion.Tag must be set
			tag = api.MultiArchImageStreamTagReference{
				ImageStreamTagReference: api.ImageStreamTagReference{
					Namespace: configuration.PromotionConfiguration.Namespace,
					Name:      dst,
					Tag:       configuration.PromotionConfiguration.Tag,
				},
			}
		}
		promotedTags[src] = append(promotedTags[src], tag)
		if configuration.PromotionConfiguration.TagByCommit && opts.commitSha != "" {
			promotedTags[src] = append(promotedTags[src], api.MultiArchImageStreamTagReference{
				ImageStreamTagReference: api.ImageStreamTagReference{
					Namespace: configuration.PromotionConfiguration.Namespace,
					Name:      dst,
					Tag:       opts.commitSha,
				},
			})
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
	return promotedTags, names
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

func (s *promotionStep) Name() string { return "[promotion]" }

func (s *promotionStep) Description() string {
	return fmt.Sprintf("Promote built images into the release image stream %s", targetName(*s.configuration.PromotionConfiguration))
}

func (s *promotionStep) Objects() []ctrlruntimeclient.Object {
	return s.client.Objects()
}

// PromotionStep copies tags from the pipeline image stream to the destination defined in the promotion config.
// If the source tag does not exist it is silently skipped.
func PromotionStep(configuration *api.ReleaseBuildConfiguration, requiredImages sets.String, jobSpec *api.JobSpec, client kubernetes.PodClient, pushSecret *coreapi.Secret) api.Step {
	return &promotionStep{
		configuration:  configuration,
		requiredImages: requiredImages,
		jobSpec:        jobSpec,
		client:         client,
		pushSecret:     pushSecret,
	}
}
