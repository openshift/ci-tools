package quay_io_ci_images_distributor

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/sirupsen/logrus"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	imagev1 "github.com/openshift/api/image/v1"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/util/gzip"
)

func LoadConfig(file string) (*CIImagesMirrorConfig, error) {
	bytes, err := gzip.ReadFileMaybeGZIP(file)
	if err != nil {
		return nil, err
	}
	c := &CIImagesMirrorConfig{}
	if err := yaml.UnmarshalStrict(bytes, c); err != nil {
		return nil, err
	}
	var errs []error
	for k, v := range c.SupplementalCIImages {
		splits := strings.Split(k, "/")
		if len(splits) != 2 || splits[0] == "" || splits[1] == "" {
			errs = append(errs, fmt.Errorf("invalid target: %s", k))
		} else {
			splits = strings.Split(splits[1], ":")
			if len(splits) != 2 || splits[0] == "" || splits[1] == "" {
				errs = append(errs, fmt.Errorf("invalid target: %s", k))
			}
		}
		if err := validateSource(v); err != nil {
			errs = append(errs, err)
		}
	}

	for _, ignoredSource := range c.IgnoredSources {
		if err := validateSource(ignoredSource.Source); err != nil {
			errs = append(errs, err)
		}
	}

	var artImages []ArtImage
	for i, artImage := range c.ArtImages {
		if artImage.Namespace == "" {
			errs = append(errs, fmt.Errorf("namespace for ArtImages[%d] must be set", i))
		}
		if artImage.NameRaw == "" {
			errs = append(errs, fmt.Errorf("name's regex for ArtImages[%d] must be set", i))
		} else {
			re, err := regexp.Compile(artImage.NameRaw)
			if err != nil {
				errs = append(errs, fmt.Errorf("name's regex for ArtImages[%d] cannot be compiled", i))
			}
			artImage.Name = re
			if artImage.TagRaw != "" {
				re, err = regexp.Compile(artImage.TagRaw)
				if err != nil {
					errs = append(errs, fmt.Errorf("tag's regex for ArtImages[%d] cannot be compiled", i))
				}
				artImage.Tag = re
			}
			artImages = append(artImages, artImage)
		}
	}
	if len(errs) > 0 {
		return nil, utilerrors.NewAggregate(errs)
	}
	c.ArtImages = artImages
	remained := map[string]Source{}
	for k, v := range c.SupplementalCIImages {
		if !ignored(c.IgnoredSources, v, "SupplementalCIImages") {
			remained[k] = v
		}
	}
	c.SupplementalCIImages = remained
	return c, nil
}

func ignored(ignoredSources []IgnoredSource, s Source, section string) bool {
	for _, ignoredSource := range ignoredSources {
		if ignoredSource.Image != "" && ignoredSource.Image == s.Image {
			logrus.WithField("section", section).WithField("image", s.Image).WithField("reason", ignoredSource.Reason).Info("Ignored source")
			return true
		}
		if ignoredSource.Image != "" && ignoredSource.Image == fmt.Sprintf("%s/%s", api.ServiceDomainAPPCIRegistry, s.ISTagName()) {
			logrus.WithField("section", section).WithField("image", s.Image).WithField("reason", ignoredSource.Reason).Info("Ignored source")
			return true
		}
		if ignoredSource.Namespace != "" && ignoredSource.ISTagName() == s.ISTagName() {
			logrus.WithField("section", section).WithField("ISTagName", ignoredSource.ISTagName()).WithField("reason", ignoredSource.Reason).Info("Ignored source")
			return true
		}
		if ignoredSource.Namespace != "" && s.Image == fmt.Sprintf("%s/%s", api.ServiceDomainAPPCIRegistry, ignoredSource.ISTagName()) {
			logrus.WithField("section", section).WithField("ISTagName", ignoredSource.ISTagName()).WithField("reason", ignoredSource.Reason).Info("Ignored source")
			return true
		}
	}
	return false
}

func validateSource(v Source) error {
	if v.As != "" {
		return errors.New("as cannot be set")
	}
	if v.Image == "" {
		if v.Namespace == "" {
			return errors.New("namespace for the source must be set")
		}
		if v.Name == "" {
			return errors.New("name for the source must be set")
		}
		if v.Tag == "" {
			return errors.New("tag for the source must be set")
		}
	}
	return nil
}

type CIImagesMirrorConfig struct {
	SupplementalCIImages map[string]Source `json:"supplementalCIImages"`
	IgnoredSources       []IgnoredSource   `json:"ignoredSources"`
	ArtImages            []ArtImage        `json:"artImages,omitempty"`
}

type ArtImage struct {
	Namespace string         `json:"namespace"`
	NameRaw   string         `json:"Name"`
	Name      *regexp.Regexp `json:"-"`
	TagRaw    string         `json:"Tag"`
	Tag       *regexp.Regexp `json:"-"`
}

type IgnoredSource struct {
	Source `json:",inline"`
	Reason string `json:"reason"`
}

type Source struct {
	api.ImageStreamTagReference `json:",inline"`
	// Image is an image that can be pulled in either form of tag or digest.
	// When image is set, Tag will be ignored.
	Image string `json:"image"`
}

func ARTImages(ctx context.Context, client ctrlruntimeclient.Client, artImages []ArtImage, ignoredSources []IgnoredSource) (map[string]Source, error) {
	var ret map[string]Source
	for _, artImage := range artImages {
		imageStreams := &imagev1.ImageStreamList{}
		if err := client.List(ctx, imageStreams, ctrlruntimeclient.InNamespace(artImage.Namespace)); err != nil {
			return nil, fmt.Errorf("failed to list imagestreams namespace %s: %w", artImage.Namespace, err)
		}
		for _, is := range imageStreams.Items {
			if !artImage.Name.MatchString(is.Name) {
				logrus.WithField("namespace", artImage.Namespace).WithField("name", is.Name).Debug("Ignored image stream")
				continue
			}
			for _, tag := range is.Status.Tags {
				if artImage.Tag != nil && !artImage.Tag.MatchString(tag.Tag) {
					logrus.WithField("namespace", artImage.Namespace).WithField("name", is.Name).WithField("tag", tag.Tag).Debug("Ignored image stream tag")
					continue
				}
				if ret == nil {
					ret = map[string]Source{}
				}
				ref := api.ImageStreamTagReference{Namespace: artImage.Namespace, Name: is.Name, Tag: tag.Tag}
				source := Source{ImageStreamTagReference: ref}
				key := ref.ISTagName()
				if !ignored(ignoredSources, source, "artImages") {
					ret[key] = source
				}
			}
		}
	}
	return ret, nil
}
