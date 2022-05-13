package api

import "testing"

func TestPromotesOfficialImages(t *testing.T) {
	var testCases = []struct {
		name       string
		configSpec *ReleaseBuildConfiguration
		expected   bool
	}{
		{
			name: "config without promotion doesn't produce official images",
			configSpec: &ReleaseBuildConfiguration{
				PromotionConfiguration: nil,
			},
			expected: false,
		},
		{
			name: "config explicitly promoting to ocp namespace produces official images",
			configSpec: &ReleaseBuildConfiguration{
				PromotionConfiguration: &PromotionConfiguration{
					Namespace: "ocp",
				},
			},
			expected: true,
		},
		{
			name: "config with disabled explicit promotion to ocp namespace does not produce official images",
			configSpec: &ReleaseBuildConfiguration{
				PromotionConfiguration: &PromotionConfiguration{
					Namespace: "ocp",
					Disabled:  true,
				},
			},
			expected: false,
		},
		{
			name: "config explicitly promoting to okd namespace produces official images",
			configSpec: &ReleaseBuildConfiguration{
				PromotionConfiguration: &PromotionConfiguration{
					Namespace: "origin",
				},
			},
			expected: true,
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if actual, expected := PromotesOfficialImages(testCase.configSpec, WithOKD), testCase.expected; actual != expected {
				t.Errorf("%s: did not identify official promotion correctly, expected %v got %v", testCase.name, expected, actual)
			}
		})
	}
}
