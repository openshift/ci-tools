package manifestpusher

import (
	"fmt"
	"os"

	"github.com/estesp/manifest-tool/v2/pkg/registry"
	"github.com/estesp/manifest-tool/v2/pkg/types"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/sirupsen/logrus"

	buildv1 "github.com/openshift/api/build/v1"
)

const (
	nodeArchitectureLabel   = "kubernetes.io/arch"
	serviceAccountTokenPath = "/var/run/secrets/kubernetes.io/serviceaccount/token"
)

type ManifestPusher interface {
	PushImageWithManifest(builds []buildv1.Build, targetImageRef string) error
}

func NewManifestPusher(logger *logrus.Entry, registryURL string) ManifestPusher {
	return &manifestPusher{logger: logger, registryURL: registryURL}
}

type manifestPusher struct {
	logger      *logrus.Entry
	registryURL string
}

func (m manifestPusher) PushImageWithManifest(builds []buildv1.Build, targetImageRef string) error {
	srcImages := []types.ManifestEntry{}

	token, err := resolveServiceAccountToken()
	if err != nil {
		return fmt.Errorf("couldn't get the service account token: %w", err)
	}

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
		"",
		token,
		types.YAMLInput{Image: fmt.Sprintf("%s/%s", m.registryURL, targetImageRef), Manifests: srcImages},
		false,        // --ignore-missing. We don't want to ignore missing images.
		true,         // --insecure to allow pushing to the local registry.
		false,        // --plain-http is false by default in manifest-tool. False for the OpenShift registry.
		types.Docker, // we only need docker type manifest.
		"",
	)
	if err != nil {
		return err
	}
	m.logger.WithField("digest", digest).Infof("Image %s created", targetImageRef)

	return nil
}

func resolveServiceAccountToken() (string, error) {
	data, err := os.ReadFile(serviceAccountTokenPath)
	if err != nil {
		return "", fmt.Errorf("failed to read token file: %w", err)
	}

	return string(data), nil
}
