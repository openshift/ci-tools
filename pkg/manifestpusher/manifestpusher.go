package manifestpusher

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/estesp/manifest-tool/v2/pkg/registry"
	"github.com/estesp/manifest-tool/v2/pkg/types"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/sirupsen/logrus"

	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
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

	if err := m.importManifestList(targetImageRef, digest); err != nil {
		return fmt.Errorf("failed to import manifest list into ImageStreamTag: %w", err)
	}

	return nil
}

// importManifestList forces the ImageStreamTag to point at the manifest list
// we just pushed by creating an ImageStreamImport. The integrated registry's
// async reconciliation does not reliably pick up manifest lists pushed via
// the Docker V2 API (manifest-tool), so without this the IST can stay at a
// stale digest and downstream builds fail to find the expected architecture.
func (m manifestPusher) importManifestList(targetImageRef, digest string) error {
	namespace, imageStreamName, tag, err := parseImageStreamRef(targetImageRef)
	if err != nil {
		return err
	}

	sourcePullSpec := fmt.Sprintf("%s/%s@%s", m.registryURL, targetImageRef, digest)
	m.logger.WithFields(logrus.Fields{
		"namespace":      namespace,
		"imageStream":    imageStreamName,
		"tag":            tag,
		"sourcePullSpec": sourcePullSpec,
	}).Info("Importing manifest list into ImageStreamTag")

	var lastErr error
	if err := wait.ExponentialBackoff(wait.Backoff{Steps: 3, Duration: 1 * time.Second, Factor: 2}, func() (bool, error) {
		streamImport := &imagev1.ImageStreamImport{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: namespace,
				Name:      imageStreamName,
			},
			Spec: imagev1.ImageStreamImportSpec{
				Import: true,
				Images: []imagev1.ImageImportSpec{
					{
						To: &corev1.LocalObjectReference{Name: tag},
						From: corev1.ObjectReference{
							Kind: "DockerImage",
							Name: sourcePullSpec,
						},
						ImportPolicy:    imagev1.TagImportPolicy{ImportMode: imagev1.ImportModePreserveOriginal, Insecure: true},
						ReferencePolicy: imagev1.TagReferencePolicy{Type: imagev1.SourceTagReferencePolicy},
					},
				},
			},
		}
		if err := m.client.Create(context.Background(), streamImport); err != nil {
			if kerrors.IsConflict(err) || kerrors.IsForbidden(err) {
				lastErr = err
				return false, nil
			}
			return false, err
		}
		if len(streamImport.Status.Images) == 0 || streamImport.Status.Images[0].Image == nil {
			lastErr = fmt.Errorf("import returned no image status")
			return false, nil
		}
		return true, nil
	}); err != nil {
		if lastErr != nil {
			return fmt.Errorf("%w: %v", err, lastErr)
		}
		return err
	}
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
	ns, name, tag, err := parseImageStreamRef(targetImageRef)
	if err != nil {
		return "", "", err
	}
	return ns, fmt.Sprintf("%s:%s", name, tag), nil
}

// parseImageStreamRef splits a target image reference of the form
// "<namespace>/<imagestream>:<tag>" into its three components.
// splitImageStreamTagRef returns the colon-joined "name:tag" form needed for
// Kubernetes IST lookups; this function returns them separately for
// ImageStreamImport objects which need the imagestream name and tag apart.
func parseImageStreamRef(targetImageRef string) (namespace, imageStreamName, tag string, err error) {
	parts := strings.SplitN(targetImageRef, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", "", fmt.Errorf("invalid target image reference %q, expected <namespace>/<name>:<tag>", targetImageRef)
	}

	ref, err := util.ParseImageStreamTagReference(parts[1])
	if err != nil {
		return "", "", "", fmt.Errorf("invalid target image stream tag %q: %w", parts[1], err)
	}
	return parts[0], ref.Name, ref.Tag, nil
}
