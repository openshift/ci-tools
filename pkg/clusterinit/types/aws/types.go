package aws

import (
	"fmt"

	rhcostream "github.com/coreos/stream-metadata-go/stream"
)

func FindAMI(stream rhcostream.Stream, arch, region string) (string, error) {
	architecture, ok := stream.Architectures[arch]
	if !ok {
		return "", fmt.Errorf("no ami for arch %s", arch)
	}

	if architecture.Images.Aws == nil {
		return "", fmt.Errorf("no AWS images for %s/%s", arch, region)
	}

	image, ok := architecture.Images.Aws.Regions[region]
	if !ok {
		return "", fmt.Errorf("no ami for region %s", region)
	}

	return image.Image, nil
}
