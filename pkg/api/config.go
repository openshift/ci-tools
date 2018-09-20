package api

import (
	"errors"
	"fmt"

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
			typeCount := 0
			if testConfig := test.ContainerTestConfiguration; testConfig != nil {
				typeCount += 1
				// TODO remove when the migration is completed
				if len(testConfig.From) == 0 && len(test.From) == 0 {
					errs = append(errs, fmt.Errorf("test %q: `from` is required", test.As))
				}
			}
			if testConfig := test.OpenshiftAnsibleClusterTestConfiguration; testConfig != nil {
				typeCount += 1
				errs = append(errs, validateTargetCloud(test.As, testConfig.TargetCloud))
			}
			if testConfig := test.OpenshiftAnsibleSrcClusterTestConfiguration; testConfig != nil {
				typeCount += 1
				errs = append(errs, validateTargetCloud(test.As, testConfig.TargetCloud))
			}
			if testConfig := test.OpenshiftInstallerClusterTestConfiguration; testConfig != nil {
				typeCount += 1
				errs = append(errs, validateTargetCloud(test.As, testConfig.TargetCloud))
			}
			if testConfig := test.OpenshiftInstallerSmokeClusterTestConfiguration; testConfig != nil {
				typeCount += 1
				errs = append(errs, validateTargetCloud(test.As, testConfig.TargetCloud))
			}
			if typeCount == 0 {
				// TODO remove when the migration is completed
				if len(test.From) == 0 {
					errs = append(errs, fmt.Errorf("test %q has no type", test.As))
				}
			} else if typeCount == 1 {
				// TODO remove when the migration is completed
				if len(test.From) > 0 {
					errs = append(errs, fmt.Errorf("test %q specifies both `From` and a test type", test.As))
				}
			} else if typeCount > 1 {
				errs = append(errs, fmt.Errorf("test %q has more than one type", test.As))
			}
		}
	}
	buildRootImage := config.InputConfiguration.BuildRootImage
	if buildRootImage != nil {
		if buildRootImage.ProjectImageBuild != nil && buildRootImage.ImageStreamTagReference != nil {
			errs = append(errs, errors.New("both project_image and image_stream_tag cannot be set for the build_root"))
		} else if buildRootImage.ProjectImageBuild == nil && buildRootImage.ImageStreamTagReference == nil {
			errs = append(errs, errors.New("you have to specify either project_image or image_stream_tag for the build_root"))
		}
	}
	return kerrors.NewAggregate(errs)
}

func validateTargetCloud(test string, tc TargetCloud) error {
	if tc != TargetCloudAWS && tc != TargetCloudGCP {
		return fmt.Errorf("test %q: invalid target cloud %q", test, tc)
	}
	return nil
}
