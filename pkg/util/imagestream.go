package util

import (
	"fmt"

	imageapi "github.com/openshift/api/image/v1"
)

// ResolvePullSpec if a tag of an imagestream is resolved
func ResolvePullSpec(is *imageapi.ImageStream, tag string, requireExact bool) (string, bool) {
	for _, tags := range is.Status.Tags {
		if tags.Tag != tag {
			continue
		}
		if len(tags.Items) == 0 {
			break
		}
		if image := tags.Items[0].Image; len(image) > 0 {
			if len(is.Status.PublicDockerImageRepository) > 0 {
				return fmt.Sprintf("%s@%s", is.Status.PublicDockerImageRepository, image), true
			}
			if len(is.Status.DockerImageRepository) > 0 {
				return fmt.Sprintf("%s@%s", is.Status.DockerImageRepository, image), true
			}
		}
		break
	}
	if requireExact {
		return "", false
	}
	if len(is.Status.PublicDockerImageRepository) > 0 {
		return fmt.Sprintf("%s:%s", is.Status.PublicDockerImageRepository, tag), true
	}
	if len(is.Status.DockerImageRepository) > 0 {
		return fmt.Sprintf("%s:%s", is.Status.DockerImageRepository, tag), true
	}
	return "", false
}
