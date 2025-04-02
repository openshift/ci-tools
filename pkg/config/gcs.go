package config

import (
	"context"
	"fmt"
	"io"
	"strings"

	"sigs.k8s.io/prow/pkg/pod-utils/gcs"

	"github.com/openshift/ci-tools/pkg/util/gzip"
)

type GCSUploader struct {
	gcsBucket          string
	gcsCredentialsFile string
}

// UploadConfigSpec compresses, encodes, and uploads the given ciOpConfigContent to GCS
// returns the full GCS url to the uploaded file
func (u *GCSUploader) UploadConfigSpec(ctx context.Context, location, ciOpConfigContent string) (string, error) {
	compressedConfig, err := gzip.CompressStringAndBase64(ciOpConfigContent)
	if err != nil {
		return "", fmt.Errorf("couldn't compress and base64 encode CONFIG_SPEC: %w", err)
	}
	uploadTargets := map[string]gcs.UploadFunc{
		location: gcs.DataUpload(func() (io.ReadCloser, error) {
			return io.NopCloser(strings.NewReader(compressedConfig)), nil
		}),
	}
	if err := gcs.Upload(ctx, u.gcsBucket, u.gcsCredentialsFile, "", []string{"*"}, uploadTargets); err != nil {
		return "", fmt.Errorf("couldn't upload CONFIG_SPEC to GCS: %w", err)
	}
	return fmt.Sprintf("gs://%s/%s", u.gcsBucket, location), nil
}

func NewGCSUploader(gcsBucket, gcsCredentialsFile string) *GCSUploader {
	return &GCSUploader{
		gcsBucket:          gcsBucket,
		gcsCredentialsFile: gcsCredentialsFile,
	}
}
