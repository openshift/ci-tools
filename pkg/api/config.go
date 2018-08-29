package api

import (
	"fmt"
)

// Validate validates config
func (config *ReleaseBuildConfiguration) Validate() error {
	if config.Tests != nil {
		for _, test := range config.Tests {
			if test.As == "images" {
				return fmt.Errorf("Test should not be called 'images' because it gets confused with '[images]' target")
			}
		}
	}

	return nil
}
