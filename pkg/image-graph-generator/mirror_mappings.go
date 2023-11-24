package imagegraphgenerator

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/openshift/ci-tools/pkg/api"
)

const (
	mappingFilePrefix = "mapping_"
)

func (o *Operator) UpdateMirrorMappings() error {
	err := filepath.Walk(filepath.Join(o.releaseRepoPath, ReleaseMirrorMappingsPath),
		func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}

			if !strings.HasPrefix(info.Name(), mappingFilePrefix) {
				return nil
			}

			file, err := os.Open(path)
			if err != nil {
				return err
			}
			defer file.Close()

			scanner := bufio.NewScanner(file)
			for scanner.Scan() {
				isDetails := loadImageStreamDetails(scanner.Text())
				if isDetails == nil {
					continue
				}

				imageRef := &ImageRef{
					Name:           isDetails.Fullname,
					Namespace:      isDetails.Namespace,
					ImageStreamRef: isDetails.ImageStream,
					Source:         isDetails.Source,
				}

				if id, ok := o.images[isDetails.Fullname]; ok {
					if err := o.updateImageRef(imageRef, id); err != nil {
						return err
					}
					continue
				}

				if err := o.addImageRef(imageRef); err != nil {
					return err
				}

			}

			if err := scanner.Err(); err != nil {
				return err
			}

			return nil
		})
	if err != nil {
		return err
	}
	return nil
}

type MirrorMapping struct {
	Source      ImageStreamLink
	Destination ImageStreamLink
}

type ImageStreamLink struct {
	Source      string
	Fullname    string
	Namespace   string
	ImageStream string
	Tag         string
}

func loadImageStreamDetails(line string) *ImageStreamLink {
	splitted := strings.Split(line, " ")
	if len(splitted) != 2 {
		return nil
	}

	dest := splitted[1]
	if !strings.HasPrefix(dest, api.ServiceDomainAPPCIRegistry) {
		return nil
	}

	imageName := strings.TrimPrefix(dest, fmt.Sprintf("%s/", api.ServiceDomainAPPCIRegistry))
	s := strings.Split(imageName, "/")
	isWithTag := strings.Split(s[1], ":")

	return &ImageStreamLink{
		Source:      splitted[0],
		Fullname:    imageName,
		Namespace:   s[0],
		ImageStream: isWithTag[0],
		Tag:         isWithTag[1],
	}
}
