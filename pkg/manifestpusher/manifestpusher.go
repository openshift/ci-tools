package manifestpusher

import (
	"fmt"
	"strings"
	"time"

	"github.com/estesp/manifest-tool/v2/pkg/registry"
	"github.com/estesp/manifest-tool/v2/pkg/types"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/sirupsen/logrus"

	"k8s.io/apimachinery/pkg/util/wait"

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
		arch := build.Spec.NodeSelector[nodeArchitectureLabel]
		if arch == "" {
			return fmt.Errorf("build %s has no architecture label in nodeSelector", build.Name)
		}
		imageRef := fmt.Sprintf("%s/%s/%s", m.registryURL, build.Spec.Output.To.Namespace, build.Spec.Output.To.Name)
		m.logger.Infof("Adding architecture %s: %s", arch, imageRef)
		srcImages = append(srcImages, types.ManifestEntry{
			Image: imageRef,
			Platform: ocispec.Platform{
				OS:           "linux",
				Architecture: arch,
			},
		})
	}

	if len(srcImages) == 0 {
		return fmt.Errorf("no source images to create manifest list for %s", targetImageRef)
	}

	targetImage := fmt.Sprintf("%s/%s", m.registryURL, targetImageRef)
	m.logger.Infof("Creating manifest list for %s with %d architectures", targetImage, len(srcImages))

	// Wait for all images to be available in the registry before creating the manifest list.
	// There's a race condition where builds are marked complete but images aren't fully
	// available in the registry yet. We retry with exponential backoff to handle this.
	backoff := wait.Backoff{
		Duration: 5 * time.Second,
		Factor:   1.5,
		Steps:    10, // Max ~2 minutes total wait time
	}

	var digest string
	var length int
	var err error

	err = wait.ExponentialBackoff(backoff, func() (bool, error) {
		digest, length, err = registry.PushManifestList(
			"", // username: we don't we use basic auth
			"", // password:             "
			types.YAMLInput{Image: targetImage, Manifests: srcImages},
			false,        // --ignore-missing. We don't want to ignore missing images.
			true,         // --insecure to allow pushing to the local registry.
			false,        // --plain-http is false by default in manifest-tool. False for the OpenShift registry.
			types.Docker, // we only need docker type manifest.
			m.dockercfgPath,
		)
		if err != nil {
			// Check if the error indicates missing images (common race condition)
			errStr := err.Error()
			if strings.Contains(errStr, "no image found in manifest list") ||
				strings.Contains(errStr, "inspect of image") ||
				strings.Contains(errStr, "failed to pull image") ||
				strings.Contains(errStr, "choosing an image from manifest list") ||
				strings.Contains(errStr, "PullBuilderImageFailed") {
				m.logger.Warnf("Images not yet available in registry, retrying: %v", err)
				return false, nil // Retry
			}
			// For other errors, fail immediately
			return false, err
		}
		return true, nil
	})

	if err != nil {
		return fmt.Errorf("failed to push manifest list for %s after retries: %w", targetImageRef, err)
	}
	m.logger.WithField("digest", digest).WithField("length", length).Infof("Successfully created manifest list for %s", targetImageRef)

	return nil
}
