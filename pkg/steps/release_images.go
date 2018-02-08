package steps

import (
	"encoding/json"
	"fmt"
	"log"

	imageapi "github.com/openshift/api/image/v1"
	"github.com/openshift/ci-operator/pkg/api"
	imageclientset "github.com/openshift/client-go/image/clientset/versioned/typed/image/v1"
	routeclientset "github.com/openshift/client-go/route/clientset/versioned/typed/route/v1"
	coreapi "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	coreclientset "k8s.io/client-go/kubernetes/typed/core/v1"
)

const (
	ConfigMapName = "release"
)

// releaseImagesTagStep will tag a full release suite
// of images in from the configured namespace. It is
// expected that builds will overwrite these tags at
// a later point, selectively
type releaseImagesTagStep struct {
	config          api.ReleaseTagConfiguration
	istClient       imageclientset.ImageStreamTagInterface
	isGetter        imageclientset.ImageStreamsGetter
	routeClient     routeclientset.RoutesGetter
	configMapClient coreclientset.ConfigMapInterface
	jobSpec         *JobSpec
}

func (s *releaseImagesTagStep) Run(dry bool) error {
	log.Printf("Tagging release images into %s\n", s.jobSpec.Identifier())
	stableImageStreams, err := s.isGetter.ImageStreams(s.config.Namespace).List(meta.ListOptions{})
	if err != nil {
		return fmt.Errorf("could not resolve stable imagestreams: %v", err)
	}

	for _, stableImageStream := range stableImageStreams.Items {
		log.Printf("Considering stable image stream %s", stableImageStream.Name)
		targetTag := s.config.Tag
		if override, ok := s.config.TagOverrides[stableImageStream.Name]; ok {
			targetTag = override
		}

		for _, tag := range stableImageStream.Spec.Tags {
			if tag.Name == targetTag {
				log.Printf("Cross-tagging %s/%s:%s from %s/%s:%s", s.jobSpec.Identifier(), stableImageStream.Name, targetTag, stableImageStream.Namespace, stableImageStream.Name, targetTag)
				var id string
				for _, tagStatus := range stableImageStream.Status.Tags {
					if tagStatus.Tag == targetTag {
						id = tagStatus.Items[0].Image
					}
				}
				if len(id) == 0 {
					return fmt.Errorf("no image found backing %s/%s:%s", stableImageStream.Namespace, stableImageStream.Name, targetTag)
				}
				ist := &imageapi.ImageStreamTag{
					ObjectMeta: meta.ObjectMeta{
						Namespace: s.jobSpec.Identifier(),
						Name:      fmt.Sprintf("%s:%s", stableImageStream.Name, targetTag),
					},
					Tag: &imageapi.TagReference{
						Name: targetTag,
						From: &coreapi.ObjectReference{
							Kind:      "ImageStreamImage",
							Name:      fmt.Sprintf("%s@%s", stableImageStream.Name, id),
							Namespace: s.config.Namespace,
						},
					},
				}

				if dry {
					istJSON, err := json.Marshal(ist)
					if err != nil {
						return fmt.Errorf("failed to marshal imagestreamtag: %v", err)
					}
					fmt.Printf("%s", istJSON)
					continue
				}
				_, err := s.istClient.Create(ist)
				if err != nil && !errors.IsAlreadyExists(err) {
					return fmt.Errorf("could not copy stable imagestreamtag: %v", err)
				}
			}
		}
	}

	return nil
	//return s.createReleaseConfigMap(dry)
}

func (s *releaseImagesTagStep) createReleaseConfigMap(dry bool) error {
	imageBase := "dry-fake"
	rpmRepo := "dry-fake"
	if !dry {
		originImageStream, err := s.isGetter.ImageStreams(s.jobSpec.Identifier()).Get("origin", meta.GetOptions{})
		if err != nil {
			return fmt.Errorf("could not resolve main release ImageStream: %v", err)
		}
		if len(originImageStream.Status.PublicDockerImageRepository) == 0 {
			return fmt.Errorf("release ImageStream %s/%s is not exposed externally", originImageStream.Namespace, originImageStream.Name)
		}
		imageBase = originImageStream.Status.PublicDockerImageRepository

		rpmRepoServer, err := s.routeClient.Routes(s.jobSpec.Identifier()).Get(RPMRepoName, meta.GetOptions{})
		if ! errors.IsNotFound(err) {
			return err
		} else {
			rpmRepoServer, err = s.routeClient.Routes(s.config.Namespace).Get(RPMRepoName, meta.GetOptions{})
			if err != nil {
				return err
			}
		}
		rpmRepo = rpmRepoServer.Spec.Host
	}

	cm := &coreapi.ConfigMap{
		ObjectMeta: meta.ObjectMeta{
			Name:      ConfigMapName,
			Namespace: s.jobSpec.Identifier(),
		},
		Data: map[string]string{
			"image-base": imageBase,
			"rpm-repo":   rpmRepo,
		},
	}
	if dry {
		cmJSON, err := json.Marshal(cm)
		if err != nil {
			return fmt.Errorf("failed to marshal configmap: %v", err)
		}
		fmt.Printf("%s", cmJSON)
		return nil
	}
	if _, err := s.configMapClient.Create(cm); err != nil && ! errors.IsAlreadyExists(err) {
		return err
	}
	return nil
}

func (s *releaseImagesTagStep) Done() (bool, error) {
	log.Printf("Checking for existence of %s ConfigMap\n", ConfigMapName)
	if _, err := s.configMapClient.Get(ConfigMapName, meta.GetOptions{}); err != nil {
		if errors.IsNotFound(err) {
			return false, nil
		} else {
			return false, err
		}
	} else {
		return true, nil
	}
}

func (s *releaseImagesTagStep) Requires() []api.StepLink {
	return []api.StepLink{}
}

func (s *releaseImagesTagStep) Creates() []api.StepLink {
	return []api.StepLink{api.ReleaseImagesLink()}
}

func ReleaseImagesTagStep(config api.ReleaseTagConfiguration, istClient imageclientset.ImageStreamTagInterface, isGetter imageclientset.ImageStreamsGetter, routeClient routeclientset.RoutesGetter, configMapClient coreclientset.ConfigMapInterface, jobSpec *JobSpec) api.Step {
	return &releaseImagesTagStep{
		config:          config,
		istClient:       istClient,
		isGetter:        isGetter,
		routeClient:     routeClient,
		configMapClient: configMapClient,
		jobSpec:         jobSpec,
	}
}
