package api

import (
	"errors"

	kerrors "k8s.io/apimachinery/pkg/util/errors"
)

// Validate validates config
func (config *ReleaseBuildConfiguration) Validate() error {
	errs := []error{}
	if config.Tests != nil {
		for _, test := range config.Tests {
			if test.As == "images" {
				errs = append(errs, errors.New("test should not be called 'images' because it gets confused with '[images]' target"))
			}
		}
	}
	buildRootImage := config.InputConfiguration.BuildRootImage
	if buildRootImage != nil {
		if buildRootImage.ProjectImageBuild != nil && buildRootImage.ImageStreamTagReference != nil {
			errs = append(errs, errors.New("both git_source_image and image_stream_tag cannot be set for the build_root"))
		} else if buildRootImage.ProjectImageBuild == nil && buildRootImage.ImageStreamTagReference == nil {
			errs = append(errs, errors.New("you have to specify either git_source_image or image_stream_tag for the build_root"))
		}
	}
	return kerrors.NewAggregate(errs)
}
