package api

import (
	"testing"
)

func TestValidate(t *testing.T) {
	var testCases = []struct {
		id            string
		config        ReleaseBuildConfiguration
		expectedValid bool
		expectedError string
	}{
		{
			id: `ReleaseBuildConfiguration{Tests: {As: "unit"}}`,
			config: ReleaseBuildConfiguration{
				Tests: []TestStepConfiguration{
					{
						As: "unit",
					},
				},
			},
			expectedValid: true,
		},
		{
			id: `ReleaseBuildConfiguration{Tests: {As: "images"}}`,
			config: ReleaseBuildConfiguration{
				Tests: []TestStepConfiguration{
					{
						As: "images",
					},
				},
			},
			expectedValid: false,
			expectedError: "Test should not be called 'images' because it gets confused with '[images]' target",
		},
	}

	for _, tc := range testCases {
		err := tc.config.Validate()
		valid := err == nil

		if tc.expectedValid && !valid {
			t.Errorf("%s expected to be valid, got 'Error(%v)' instead", tc.id, err)
		}
		if !tc.expectedValid {
			if valid {
				t.Errorf("%s expected to be invalid, Validate() returned valid", tc.id)
			} else if tc.expectedError != err.Error() {
				t.Errorf("%s expected to be invalid w/ '%s', got '%s' instead", tc.id, tc.expectedError, err.Error())
			}
		}
	}
}
