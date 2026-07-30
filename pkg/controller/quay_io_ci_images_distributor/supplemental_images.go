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

func LoadConfigFromFile(path string) (*CIImagesMirrorConfig, error) {
	bytes, err := gzip.ReadFileMaybeGZIP(path)
	if err != nil {
		return nil, err
	}
	c, err := LoadConfig(bytes)
	if err != nil {
		return nil, err
	}
	return c, nil
}

func LoadConfig(bytes []byte) (*CIImagesMirrorConfig, error) {
	c := &CIImagesMirrorConfig{}
	if err := yaml.UnmarshalStrict(bytes, c); err != nil {
		return nil, err
	}
	var errs []error
	for k, v := range c.SupplementalCIImages {
		if err := validateTargetISTag(k); err != nil {
			errs = append(errs, err)
		}
		if err := validateSource(v); err != nil {
			errs = append(errs, err)
		}
	}

	for k, v := range c.QCIToAppCIImages {
		if err := validateTargetISTag(k); err != nil {
			errs = append(errs, fmt.Errorf("qciToAppCIImages: %w", err))
			continue
		}
		if v.Image == "" {
			ref, err := parseISTagName(k)
			if err != nil {
				errs = append(errs, fmt.Errorf("qciToAppCIImages: %w", err))
				continue
			}
			v.Image = api.QuayImageReference(ref)
			c.QCIToAppCIImages[k] = v
		}
		if err := validateQCISource(v); err != nil {
			errs = append(errs, fmt.Errorf("qciToAppCIImages[%s]: %w", k, err))
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

	qciRemained := map[string]Source{}
	for k, v := range c.QCIToAppCIImages {
		if !ignored(c.IgnoredSources, v, "QCIToAppCIImages") {
			qciRemained[k] = v
		}
	}
	c.QCIToAppCIImages = qciRemained
	return c, nil
}

func validateTargetISTag(k string) error {
	splits := strings.Split(k, "/")
	if len(splits) != 2 || splits[0] == "" || splits[1] == "" {
		return fmt.Errorf("invalid target: %s", k)
	}
	nameTag := strings.Split(splits[1], ":")
	if len(nameTag) != 2 || nameTag[0] == "" || nameTag[1] == "" {
		return fmt.Errorf("invalid target: %s", k)
	}
	return nil
}

func parseISTagName(k string) (api.ImageStreamTagReference, error) {
	if err := validateTargetISTag(k); err != nil {
		return api.ImageStreamTagReference{}, err
	}
	ns, rest, _ := strings.Cut(k, "/")
	name, tag, _ := strings.Cut(rest, ":")
	return api.ImageStreamTagReference{Namespace: ns, Name: name, Tag: tag}, nil
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

// validateQCISource requires an explicit pullspec under quay.io/openshift/ci or the QCI proxy.
// Namespace/Name/Tag on Source are not used for reverse mirrors; reject them so config is not silently ignored.
func validateQCISource(v Source) error {
	if v.As != "" {
		return errors.New("as cannot be set")
	}
	if v.Namespace != "" || v.Name != "" || v.Tag != "" {
		return errors.New("namespace/name/tag must not be set; use image or omit to derive from the key")
	}
	if v.Image == "" {
		return errors.New("image must resolve to a QCI pullspec")
	}
	if !isQCIPullspec(v.Image) {
		return fmt.Errorf("image %q must be under %s or %s/openshift/ci", v.Image, api.QuayOpenShiftCIRepo, api.QCIAPPCIDomain)
	}
	return nil
}

func isQCIPullspec(image string) bool {
	for _, prefix := range []string{
		api.QuayOpenShiftCIRepo + ":",
		api.QuayOpenShiftCIRepo + "@",
		api.QCIAPPCIDomain + "/openshift/ci:",
		api.QCIAPPCIDomain + "/openshift/ci@",
	} {
		if strings.HasPrefix(image, prefix) {
			return true
		}
	}
	return false
}

type CIImagesMirrorConfig struct {
	SupplementalCIImages map[string]Source `json:"supplementalCIImages"`
	// QCIToAppCIImages backfills app.ci ISTags from QCI. Key is namespace/name:tag.
	// Only Source.Image is honored (or derived from the key); do not set namespace/name/tag.
	QCIToAppCIImages map[string]Source `json:"qciToAppCIImages,omitempty"`
	IgnoredSources   []IgnoredSource   `json:"ignoredSources"`
	ArtImages        []ArtImage        `json:"artImages,omitempty"`
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
