package quay_io_ci_images_distributor

import (
	"fmt"

	"github.com/sirupsen/logrus"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"

	"github.com/openshift/ci-tools/pkg/api"
)

// MirrorQCIToAppCI enqueues syncs for m (key: app.ci ISTag namespace/name:tag; value.image: QCI pullspec).
// Empty image derives quay-proxy float from the key. Config load already restricts sources to QCI.
// Per-entry failures are collected so one bad entry does not block the rest.
func MirrorQCIToAppCI(mirrorStore MirrorStore, logger *logrus.Entry, quayIOImageHelper QuayIOImageHelper, ocImageInfoOptions OCImageInfoOptions, m map[string]Source) error {
	if len(m) == 0 {
		return nil
	}
	logger.WithField("count", len(m)).Info("Mirroring QCI images to app.ci ...")
	var errs []error
	for target, v := range m {
		source := v.Image
		if source == "" {
			ref, err := parseISTagName(target)
			if err != nil {
				errs = append(errs, fmt.Errorf("invalid target %s: %w", target, err))
				continue
			}
			source = api.QuayImageReference(ref)
		}
		destination := fmt.Sprintf("%s/%s", api.ServiceDomainAPPCIRegistry, target)

		sourceInfo, err := quayIOImageHelper.ImageInfo(source, ocImageInfoOptions)
		if err != nil {
			// ImageInfo returns a zero value on error, so Digest is empty; do not enqueue.
			logger.WithError(err).WithField("source", source).Warning("Failed to get QCI image info, skipping")
			continue
		}
		if sourceInfo.Digest == "" {
			logger.WithField("source", source).Debug("Source image does not exist, skipping mirror")
			continue
		}

		targetInfo, err := quayIOImageHelper.ImageInfo(destination, ocImageInfoOptions)
		if err != nil {
			logger.WithError(err).WithField("target", destination).Warning("Failed to get app.ci image info, will attempt mirror")
		} else if targetInfo.Digest != "" && sourceInfo.Digest == targetInfo.Digest {
			logger.WithField("source", source).WithField("target", destination).
				WithField("digest", sourceInfo.Digest).Debug("Image already in sync, skipping")
			continue
		}
		logger.WithField("source", source).WithField("target", destination).
			WithField("sourceDigest", sourceInfo.Digest).WithField("targetDigest", targetInfo.Digest).
			Info("Image needs sync")
		if err := mirrorStore.Put(MirrorTask{
			Source:      source,
			Destination: destination,
			Owner:       "qciToAppCIImages",
		}); err != nil {
			errs = append(errs, fmt.Errorf("failed to put mirror task for %s: %w", target, err))
		}
	}
	return utilerrors.NewAggregate(errs)
}
