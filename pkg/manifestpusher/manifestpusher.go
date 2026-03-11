package manifestpusher

import (
	"context"
	"fmt"
	"strings"

	"github.com/estesp/manifest-tool/v2/pkg/registry"
	"github.com/estesp/manifest-tool/v2/pkg/types"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/sirupsen/logrus"

	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	buildv1 "github.com/openshift/api/build/v1"
	imagev1 "github.com/openshift/api/image/v1"

	"github.com/openshift/ci-tools/pkg/util"
)

const (
	nodeArchitectureLabel = "kubernetes.io/arch"
)

type ManifestPusher interface {
	PushImageWithManifest(builds []buildv1.Build, targetImageRef string) error
}

func NewManifestPusher(logger *logrus.Entry, registryURL string, dockercfgPath string, client ctrlruntimeclient.Client) ManifestPusher {
	return &manifestPusher{
		logger:        logger,
		registryURL:   registryURL,
		dockercfgPath: dockercfgPath,
		client:        client,
	}
}

type manifestPusher struct {
	logger        *logrus.Entry
	registryURL   string
	dockercfgPath string
	client        ctrlruntimeclient.Client
}

func (m manifestPusher) PushImageWithManifest(builds []buildv1.Build, targetImageRef string) error {
	srcImages, err := m.manifestEntries(builds, targetImageRef)
	if err != nil {
		return err
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

func (m manifestPusher) manifestEntries(builds []buildv1.Build, targetImageRef string) ([]types.ManifestEntry, error) {
	srcImages := []types.ManifestEntry{}
	newArchitectures := sets.New[string]()
	for _, build := range builds {
		entry := types.ManifestEntry{
			Image: fmt.Sprintf("%s/%s/%s", m.registryURL, build.Spec.Output.To.Namespace, build.Spec.Output.To.Name),
			Platform: ocispec.Platform{
				OS:           "linux",
				Architecture: build.Spec.NodeSelector[nodeArchitectureLabel],
			},
		}
		srcImages = append(srcImages, entry)
		newArchitectures.Insert(entry.Platform.Architecture)
	}

	namespace, imageStreamTagName, err := splitImageStreamTagRef(targetImageRef)
	if err != nil {
		return nil, err
	}

	ist := &imagev1.ImageStreamTag{}
	if err := m.client.Get(context.Background(), ctrlruntimeclient.ObjectKey{Namespace: namespace, Name: imageStreamTagName}, ist); err != nil {
		if kerrors.IsNotFound(err) {
			return srcImages, nil
		}
		return nil, fmt.Errorf("failed to get ImageStreamTag %s/%s: %w", namespace, imageStreamTagName, err)
	}

	if len(ist.Image.DockerImageManifests) == 0 {
		return srcImages, nil
	}

	targetImage := fmt.Sprintf("%s/%s", m.registryURL, targetImageRef)
	mergedEntries := make([]types.ManifestEntry, 0, len(srcImages)+len(ist.Image.DockerImageManifests))
	mergedEntries = append(mergedEntries, srcImages...)
	for _, manifest := range ist.Image.DockerImageManifests {
		if manifest.OS == "" || manifest.Architecture == "" || manifest.Digest == "" {
			continue
		}
		if newArchitectures.Has(manifest.Architecture) {
			// This should not happen, but it's a sanity check.
			m.logger.WithFields(logrus.Fields{"target": targetImageRef, "architecture": manifest.Architecture}).Warn("Skipping existing manifest due to duplicate architecture, this should not happen.")
			continue
		}
		mergedEntries = append(mergedEntries, types.ManifestEntry{
			Image: fmt.Sprintf("%s@%s", targetImage, manifest.Digest),
			Platform: ocispec.Platform{
				OS:           manifest.OS,
				Architecture: manifest.Architecture,
				Variant:      manifest.Variant,
			},
		})
	}

	return mergedEntries, nil
}

func splitImageStreamTagRef(targetImageRef string) (string, string, error) {
	parts := strings.SplitN(targetImageRef, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("invalid target image reference %q, expected <namespace>/<name>:<tag>", targetImageRef)
	}

	ref, err := util.ParseImageStreamTagReference(parts[1])
	if err != nil {
		return "", "", fmt.Errorf("invalid target image stream tag %q: %w", parts[1], err)
	}
	return parts[0], fmt.Sprintf("%s:%s", ref.Name, ref.Tag), nil
}
