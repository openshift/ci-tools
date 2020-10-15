package release

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
	"sort"
	"strings"

	coreapi "k8s.io/api/core/v1"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	coreclientset "k8s.io/client-go/kubernetes/typed/core/v1"

	imageapi "github.com/openshift/api/image/v1"
	imageclientset "github.com/openshift/client-go/image/clientset/versioned/typed/image/v1"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/results"
	"github.com/openshift/ci-tools/pkg/steps"
)

// promotionStep will tag a full release suite
// of images out to the configured namespace.
type promotionStep struct {
	config         api.PromotionConfiguration
	images         []api.ProjectDirectoryImageBuildStepConfiguration
	requiredImages sets.String
	srcClient      imageclientset.ImageV1Interface
	podClient      steps.PodClient
	eventClient    coreclientset.EventsGetter
	jobSpec        *api.JobSpec
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

func (s *promotionStep) run(ctx context.Context) error {
	tags, names := toPromote(s.config, s.images, s.requiredImages)
	if len(names) == 0 {
		log.Println("Nothing to promote, skipping...")
		return nil
	}

	log.Printf("Promoting tags to %s: %s", targetName(s.config), strings.Join(names.List(), ", "))

	pipeline, err := s.srcClient.ImageStreams(s.jobSpec.Namespace()).Get(ctx, api.PipelineImageStream, meta.GetOptions{})
	if err != nil {
		return fmt.Errorf("could not resolve pipeline imagestream: %w", err)
	}

	imageMirrorTarget := getImageMirrorTarget(s.config, tags, pipeline)
	if len(imageMirrorTarget) == 0 {
		log.Println("Nothing to promote, skipping...")
		return nil
	}

	if _, err := steps.RunPod(ctx, s.podClient, s.eventClient, getPromotionPod(imageMirrorTarget, s.jobSpec.Namespace())); err != nil {
		return fmt.Errorf("unable to run promotion pod: %w", err)
	}
	return nil
}

func getImageMirrorTarget(config api.PromotionConfiguration, tags map[string]string, pipeline *imageapi.ImageStream) map[string]string {
	if pipeline == nil {
		return nil
	}
	imageMirror := map[string]string{}
	if len(config.Name) > 0 {
		for dst, src := range tags {
			dockerImageReference := findDockerImageReference(pipeline, src)
			if dockerImageReference == "" {
				continue
			}
			dockerImageReference = getPublicImageReference(dockerImageReference, pipeline.Status.PublicDockerImageRepository)
			imageMirror[dockerImageReference] = fmt.Sprintf("%s/%s/%s:%s", api.DomainForService(api.ServiceRegistry), config.Namespace, config.Name, dst)
		}
	} else {
		for dst, src := range tags {
			dockerImageReference := findDockerImageReference(pipeline, src)
			if dockerImageReference == "" {
				continue
			}
			dockerImageReference = getPublicImageReference(dockerImageReference, pipeline.Status.PublicDockerImageRepository)
			imageMirror[dockerImageReference] = fmt.Sprintf("%s/%s/%s:%s", api.DomainForService(api.ServiceRegistry), config.Namespace, dst, config.Tag)
		}
	}
	if len(imageMirror) == 0 {
		return nil
	}
	return imageMirror
}

func getPublicImageReference(dockerImageReference, publicDockerImageRepository string) string {
	if !strings.Contains(dockerImageReference, ":5000") {
		return dockerImageReference
	}
	splits := strings.Split(publicDockerImageRepository, "/")
	if len(splits) < 2 {
		// This should never happen
		log.Println(fmt.Sprintf("Failed to get hostname from publicDockerImageRepository: %s.", publicDockerImageRepository))
		return dockerImageReference
	}
	publicHost := splits[0]
	splits = strings.Split(dockerImageReference, "/")
	if len(splits) < 2 {
		// This should never happen
		log.Println(fmt.Sprintf("Failed to get hostname from dockerImageReference: %s.", dockerImageReference))
		return dockerImageReference
	}
	return strings.Replace(dockerImageReference, splits[0], publicHost, 1)
}

func getPromotionPod(imageMirrorTarget map[string]string, namespace string) *coreapi.Pod {
	var ocCommands []string
	keys := make([]string, 0, len(imageMirrorTarget))
	for k := range imageMirrorTarget {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		ocCommands = append(ocCommands, fmt.Sprintf("oc image mirror --registry-config=%s %s %s", filepath.Join(api.RegistryPushCredentialsCICentralSecretMountPath, coreapi.DockerConfigJsonKey), k, imageMirrorTarget[k]))
	}
	command := []string{"/bin/sh", "-c"}
	args := []string{strings.Join(ocCommands, " && ")}
	return &coreapi.Pod{
		ObjectMeta: meta.ObjectMeta{
			Name:      "promotion",
			Namespace: namespace,
		},
		Spec: coreapi.PodSpec{
			RestartPolicy: coreapi.RestartPolicyNever,
			Containers: []coreapi.Container{
				{
					Name: "promotion",
					// TODO use local image image-registry.openshift-image-registry.svc:5000/ocp/4.6:cli after migrating promotion jobs to OCP4 clusters
					Image:   fmt.Sprintf("%s/ocp/4.6:cli", api.DomainForService(api.ServiceRegistry)),
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
func findDockerImageReference(is *imageapi.ImageStream, tag string) string {
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

	if config.NamePrefix == "" {
		return tagsByDst, names
	}

	namesByDst := map[string]string{}
	names = sets.NewString()
	for dst, src := range tagsByDst {
		name := fmt.Sprintf("%s%s", config.NamePrefix, dst)
		namesByDst[name] = src
		names.Insert(name)
	}

	return namesByDst, names
}

// PromotedTags returns the tags that are being promoted for the given ReleaseBuildConfiguration
func PromotedTags(configuration *api.ReleaseBuildConfiguration) []api.ImageStreamTagReference {
	if configuration.PromotionConfiguration == nil {
		return nil
	}
	tags, _ := toPromote(*configuration.PromotionConfiguration, configuration.Images, sets.NewString())
	var promotedTags []api.ImageStreamTagReference
	for dst := range tags {
		var tag api.ImageStreamTagReference
		if configuration.PromotionConfiguration.Name != "" {
			tag = api.ImageStreamTagReference{
				Namespace: configuration.PromotionConfiguration.Namespace,
				Name:      configuration.PromotionConfiguration.Name,
				Tag:       dst,
			}
		} else { // promotion.Tag must be set
			tag = api.ImageStreamTagReference{
				Namespace: configuration.PromotionConfiguration.Namespace,
				Name:      dst,
				Tag:       configuration.PromotionConfiguration.Tag,
			}
		}
		promotedTags = append(promotedTags, tag)
	}
	return promotedTags
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

func (s *promotionStep) Name() string { return "" }

func (s *promotionStep) Description() string {
	return fmt.Sprintf("Promote built images into the release image stream %s", targetName(s.config))
}

// PromotionStep copies tags from the pipeline image stream to the destination defined in the promotion config.
// If the source tag does not exist it is silently skipped.
func PromotionStep(config api.PromotionConfiguration, images []api.ProjectDirectoryImageBuildStepConfiguration, requiredImages sets.String, srcClient imageclientset.ImageV1Interface, podClient steps.PodClient, eventClient coreclientset.EventsGetter, jobSpec *api.JobSpec) api.Step {
	return &promotionStep{
		config:         config,
		images:         images,
		requiredImages: requiredImages,
		srcClient:      srcClient,
		podClient:      podClient,
		eventClient:    eventClient,
		jobSpec:        jobSpec,
	}
}
