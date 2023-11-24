package imagegraphgenerator

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestLoadImageStreamDetails(t *testing.T) {

	tests := []struct {
		name string
		line string
		want *ImageStreamLink
	}{
		{
			name: "basic case",
			line: "registry.access.redhat.com/rhel7:latest registry.ci.openshift.org/azure/plugin-base:latest",
			want: &ImageStreamLink{
				Source:      "registry.access.redhat.com/rhel7:latest",
				Fullname:    "azure/plugin-base:latest",
				Namespace:   "azure",
				ImageStream: "plugin-base",
				Tag:         "latest",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if diff := cmp.Diff(loadImageStreamDetails(tt.line), tt.want); diff != "" {
				t.Fatal(diff)
			}
		})
	}
}
