package quay_io_ci_images_distributor

import (
	"errors"
	"fmt"
	"strings"

	"github.com/sirupsen/logrus"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"sigs.k8s.io/yaml"

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
	if len(errs) > 0 {
		return nil, utilerrors.NewAggregate(errs)
	}
	remained := map[string]Source{}
	for k, v := range c.SupplementalCIImages {
		var ignored bool
		for _, ignoredSource := range c.IgnoredSources {
			if ignoredSource.Image == v.Image {
				logrus.WithField("image", v.Image).WithField("reason", ignoredSource.Reason).Info("Removed ignored source from supplemental CI images")
				ignored = true
				break
			}
			if ignoredSource.Namespace != "" && ignoredSource.ISTagName() == v.ISTagName() {
				logrus.WithField("ISTagName", ignoredSource.ISTagName()).WithField("reason", ignoredSource.Reason).Info("Removed ignored source from supplemental CI images")
				ignored = true
				break
			}
		}
		if !ignored {
			remained[k] = v
		}
	}
	c.SupplementalCIImages = remained
	return c, nil
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
