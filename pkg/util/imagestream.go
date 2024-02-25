package util

import (
	"fmt"
	"strings"

	imageapi "github.com/openshift/api/image/v1"

	"github.com/openshift/ci-tools/pkg/api"
)

// ParseImageStreamTagReference creates a reference from an "is:tag" string.
func ParseImageStreamTagReference(s string) (api.ImageStreamTagReference, error) {
	var ret api.ImageStreamTagReference
	i := strings.LastIndex(s, ":")
	if i == -1 {
		return ret, fmt.Errorf("invalid ImageStreamTagReference: %s", s)
	}
	ret.Name = s[:i]
	ret.Tag = s[i+1:]
	if strings.Contains(ret.Name, ":") {
		return ret, fmt.Errorf("invalid ImageStreamTagReference: %s", s)
	}
	return ret, nil
}

// ResolvePullSpec if a tag of an imagestream is resolved
func ResolvePullSpec(is *imageapi.ImageStream, tag string, requireExact bool) (string, bool, imageapi.TagEventCondition) {
	var condition imageapi.TagEventCondition
	for _, tags := range is.Status.Tags {
		if tags.Tag != tag {
			continue
		}
		if conditions := tags.Conditions; len(conditions) > 0 {
			condition = conditions[0]
		}
		if len(tags.Items) == 0 {
			break
		}
		if image := tags.Items[0].Image; len(image) > 0 {
			if len(is.Status.PublicDockerImageRepository) > 0 {
				return fmt.Sprintf("%s@%s", is.Status.PublicDockerImageRepository, image), true, condition
			}
			if len(is.Status.DockerImageRepository) > 0 {
				return fmt.Sprintf("%s@%s", is.Status.DockerImageRepository, image), true, condition
			}
		}
		break
	}
	if requireExact {
		return "", false, condition
	}
	if len(is.Status.PublicDockerImageRepository) > 0 {
		return fmt.Sprintf("%s:%s", is.Status.PublicDockerImageRepository, tag), true, condition
	}
	if len(is.Status.DockerImageRepository) > 0 {
		return fmt.Sprintf("%s:%s", is.Status.DockerImageRepository, tag), true, condition
	}
	return "", false, condition
}
