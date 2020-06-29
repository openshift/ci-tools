package v1

import (
	"regexp"
	"testing"
)

func TestSetDeterministicName(t *testing.T) {
	dns1123regexp := regexp.MustCompile(`[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*`)
	testCases := []struct {
		name         string
		in           TestImageStreamTagImportSpec
		expectedName string
	}{
		{
			name: "colon gets replaced",
			in: TestImageStreamTagImportSpec{
				ClusterName: "build01",
				Namespace:   "ocp",
				Name:        "ubi:7",
			},
			expectedName: "build01-ocp-ubi.7",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			imp := &TestImageStreamTagImport{Spec: tc.in}
			imp.SetDeterministicName()
			if imp.Name != tc.expectedName {
				t.Errorf("expected name %s, got name %s", tc.expectedName, imp.Name)
			}
			if match := dns1123regexp.MatchString(imp.Name); !match {
				t.Errorf("dns1123 regexp didn't match name %s", imp.Name)
			}
		})
	}
}
