package release

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/retry"

	imageapi "github.com/openshift/api/image/v1"
	imageclientset "github.com/openshift/client-go/image/clientset/versioned/typed/image/v1"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/results"
	"github.com/openshift/ci-tools/pkg/steps/utils"
)

// promotionStep will tag a full release suite
// of images out to the configured namespace.
type promotionStep struct {
	config         api.PromotionConfiguration
	images         []api.ProjectDirectoryImageBuildStepConfiguration
	requiredImages sets.String
	srcClient      imageclientset.ImageV1Interface
	dstClient      imageclientset.ImageV1Interface
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

var promotionRetry = wait.Backoff{
	Steps:    20,
	Duration: 10 * time.Millisecond,
	Factor:   1.2,
	Jitter:   0.1,
}

func (s *promotionStep) Run(_ context.Context) error {
	return results.ForReason("promoting_images").ForError(s.run())
}

func (s *promotionStep) run() error {
	tags, names := toPromote(s.config, s.images, s.requiredImages)
	if len(names) == 0 {
		log.Println("Nothing to promote, skipping...")
		return nil
	}

	log.Printf("Promoting tags to %s: %s", targetName(s.config), strings.Join(names.List(), ", "))

	pipeline, err := s.srcClient.ImageStreams(s.jobSpec.Namespace()).Get(api.PipelineImageStream, meta.GetOptions{})
	if err != nil {
		return fmt.Errorf("could not resolve pipeline imagestream: %w", err)
	}

	if len(s.config.Name) > 0 {
		return retry.RetryOnConflict(promotionRetry, func() error {
			is, err := s.dstClient.ImageStreams(s.config.Namespace).Get(s.config.Name, meta.GetOptions{})
			if errors.IsNotFound(err) {
				is, err = s.dstClient.ImageStreams(s.config.Namespace).Create(&imageapi.ImageStream{
					ObjectMeta: meta.ObjectMeta{
						Name:      s.config.Name,
						Namespace: s.config.Namespace,
					},
				})
			}
			if err != nil {
				return fmt.Errorf("could not retrieve target imagestream: %w", err)
			}

			for dst, src := range tags {
				if valid, _ := utils.FindStatusTag(pipeline, src); valid != nil {
					is.Spec.Tags = append(is.Spec.Tags, imageapi.TagReference{
						Name: dst,
						From: valid,
					})
				}
			}

			if _, err := s.dstClient.ImageStreams(s.config.Namespace).Update(is); err != nil {
				if errors.IsConflict(err) {
					return err
				}
				return fmt.Errorf("could not promote image streams: %w", err)
			}
			return nil
		})
	}

	client := s.dstClient.ImageStreamTags(s.config.Namespace)
	for dst, src := range tags {
		valid, _ := utils.FindStatusTag(pipeline, src)
		if valid == nil {
			continue
		}

		err := retry.RetryOnConflict(promotionRetry, func() error {
			_, err := s.dstClient.ImageStreams(s.config.Namespace).Get(dst, meta.GetOptions{})
			if errors.IsNotFound(err) {
				_, err = s.dstClient.ImageStreams(s.config.Namespace).Create(&imageapi.ImageStream{
					ObjectMeta: meta.ObjectMeta{
						Name:      dst,
						Namespace: s.config.Namespace,
					},
					Spec: imageapi.ImageStreamSpec{
						LookupPolicy: imageapi.ImageLookupPolicy{
							Local: true,
						},
					},
				})
			}
			if err != nil {
				return fmt.Errorf("could not ensure target imagestream: %w", err)
			}

			ist := &imageapi.ImageStreamTag{
				ObjectMeta: meta.ObjectMeta{
					Name:      fmt.Sprintf("%s:%s", dst, s.config.Tag),
					Namespace: s.config.Namespace,
				},
				Tag: &imageapi.TagReference{
					Name: s.config.Tag,
					From: valid,
				},
			}
			if _, err := client.Update(ist); err != nil {
				if errors.IsConflict(err) {
					return err
				}
				return fmt.Errorf("could not promote imagestreamtag %s: %w", dst, err)
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
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
func PromotionStep(config api.PromotionConfiguration, images []api.ProjectDirectoryImageBuildStepConfiguration, requiredImages sets.String, srcClient, dstClient imageclientset.ImageV1Interface, jobSpec *api.JobSpec) api.Step {
	return &promotionStep{
		config:         config,
		images:         images,
		requiredImages: requiredImages,
		srcClient:      srcClient,
		dstClient:      dstClient,
		jobSpec:        jobSpec,
	}
}
