package api

import "errors"

// Validate validates config
func (config *ReleaseBuildConfiguration) Validate() error {
	buildRootImage := config.InputConfiguration.BuildRootImage
	testBaseImage := config.InputConfiguration.TestBaseImage
	if config.Tests != nil {
		for _, test := range config.Tests {
			if test.As == "images" {
				return errors.New("test should not be called 'images' because it gets confused with '[images]' target")
			}
		}
	} else if buildRootImage != nil && testBaseImage != nil {
		return errors.New("both build_root and test_base_image cannot be set")
	} else if buildRootImage != nil && testBaseImage == nil {
		if buildRootImage.ProjectImageBuild != nil && buildRootImage.ImageStreamTagReference != nil {
			return errors.New("both git_source_image and image_stream_tag cannot be set for the build_root")
		} else if buildRootImage.ProjectImageBuild == nil && buildRootImage.ImageStreamTagReference == nil {
			return errors.New("you have to specify either git_source_image or image_stream_tag for the build_root")
		}
	} else if buildRootImage == nil && testBaseImage == nil {
		return errors.New("no build_root or test_base_image has been set")
	}
	return nil
}
