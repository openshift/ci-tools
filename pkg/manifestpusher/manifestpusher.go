package manifestpusher

import (
	"fmt"

	"github.com/estesp/manifest-tool/v2/pkg/registry"
	"github.com/estesp/manifest-tool/v2/pkg/types"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/sirupsen/logrus"

	buildv1 "github.com/openshift/api/build/v1"
)

const (
	nodeArchitectureLabel = "kubernetes.io/arch"
)

type ManifestPusher interface {
	PushImageWithManifest(builds []buildv1.Build, targetImageRef string) error
}

func NewManifestPusher(logger *logrus.Entry, registryURL string, dockercfgPath string) ManifestPusher {
	return &manifestPusher{
		logger:        logger,
		registryURL:   registryURL,
		dockercfgPath: dockercfgPath,
	}
}

type manifestPusher struct {
	logger        *logrus.Entry
	registryURL   string
	dockercfgPath string
}

func (m manifestPusher) PushImageWithManifest(builds []buildv1.Build, targetImageRef string) error {
	srcImages := []types.ManifestEntry{}

	for _, build := range builds {
		srcImages = append(srcImages, types.ManifestEntry{
			Image: fmt.Sprintf("%s/%s/%s", m.registryURL, build.Spec.Output.To.Namespace, build.Spec.Output.To.Name),
			Platform: ocispec.Platform{
				OS:           "linux",
				Architecture: build.Spec.NodeSelector[nodeArchitectureLabel],
			},
		})
	}

	digest, _, err := registry.PushManifestList(
		"", // username: we don't we use basic auth
		"", // password:             "
		types.YAMLInput{Image: fmt.Sprintf("%s/%s", m.registryURL, targetImageRef), Manifests: srcImages},
		false,        // --ignore-missing. We don't want to ignore missing images.
		true,         // --insecure to allow pushing to the local registry.
		false,        // --plain-http is false by default in manifest-tool. False for the OpenShift registry.
		types.Docker, // we only need docker type manifest.
		m.dockercfgPath,
	)
	if err != nil {
		return err
	}
	m.logger.WithField("digest", digest).Infof("Image %s created", targetImageRef)

	return nil
}
