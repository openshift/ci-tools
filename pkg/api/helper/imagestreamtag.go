package helper

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/sirupsen/logrus"

	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openshift/api/image/docker10"
	imagev1 "github.com/openshift/api/image/v1"

	"github.com/openshift/ci-tools/pkg/api"
)

// LabelsOnISTagImage the labels of the image underlying the given ImageStreamTag for the given arch
func LabelsOnISTagImage(ctx context.Context, client ctrlruntimeclient.Client, isTag *imagev1.ImageStreamTag, arch api.ReleaseArchitecture) (map[string]string, error) {
	dockerImageMetadata := isTag.Image.DockerImageMetadata
	for _, imageManifest := range isTag.Image.DockerImageManifests {
		if imageManifest.Architecture == string(arch) {
			image := &imagev1.Image{}
			// image is a cluster level CRD and thus no namespace should be provided to get the object
			if err := client.Get(ctx, ctrlruntimeclient.ObjectKey{Name: imageManifest.Digest}, image); err != nil {
				return nil, fmt.Errorf("failed to get Image %s: %w", imageManifest.Digest, err)
			}
			dockerImageMetadata = image.DockerImageMetadata
			logrus.WithField("namespace", isTag.Namespace).WithField("name", isTag.Name).WithField("arch", string(arch)).Debug("Found the image in manifests")
			break
		}
	}
	metadata := &docker10.DockerImage{}
	if len(dockerImageMetadata.Raw) == 0 {
		return nil, fmt.Errorf("found no Docker image metadata for ImageStreamTag %s in %s", isTag.Name, isTag.Namespace)
	}
	if err := json.Unmarshal(dockerImageMetadata.Raw, metadata); err != nil {
		return nil, fmt.Errorf("malformed Docker image metadata for ImageStreamTag %s in %s: %w", isTag.Name, isTag.Namespace, err)
	}
	if metadata.Config == nil {
		logrus.WithField("namespace", isTag.Namespace).WithField("name", isTag.Name).WithField("arch", string(arch)).Debug("Found no config in Docker image metadata")
		return nil, nil
	}
	return metadata.Config.Labels, nil
}
