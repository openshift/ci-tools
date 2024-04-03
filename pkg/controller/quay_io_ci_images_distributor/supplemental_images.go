package quay_io_ci_images_distributor

import (
	"errors"
	"fmt"
	"strings"

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
		if v.As != "" {
			errs = append(errs, errors.New("as cannot be set"))
		}
		if v.Image == "" {
			if v.Namespace == "" {
				errs = append(errs, errors.New("namespace for the source must be set"))
			}
			if v.Name == "" {
				errs = append(errs, errors.New("name for the source must be set"))
			}
			if v.Tag == "" {
				errs = append(errs, errors.New("tag for the source must be set"))
			}
		}
	}
	if len(errs) > 0 {
		return nil, utilerrors.NewAggregate(errs)
	}
	return c, nil
}

type CIImagesMirrorConfig struct {
	SupplementalCIImages map[string]Source `json:"supplementalCIImages"`
}

type Source struct {
	api.ImageStreamTagReference `json:",inline"`
	// Image is an image that can be pulled in either form of tag or digest.
	// When image is set, Tag will be ignored.
	Image string `json:"image"`
}
