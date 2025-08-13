package release

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	imagev1 "github.com/openshift/api/image/v1"

	"github.com/openshift/ci-tools/pkg/api"
)

func TestBuildOcAdmReleaseNewCommand(t *testing.T) {
	sourceTagReference := imagev1.SourceTagReferencePolicy

	tests := []struct {
		name          string
		config        *api.ReleaseTagConfiguration
		referenceMode string
		namespace     string
		streamName    string
		cvo           string
		destination   string
		version       string
		expectedCmd   string
	}{
		{
			name:        "4.10 no keep-manifest-list",
			config:      &api.ReleaseTagConfiguration{Name: "4.10"},
			namespace:   "ns",
			streamName:  "stream",
			cvo:         "cvo",
			destination: "dest",
			version:     "ver",
			expectedCmd: "oc adm release new --max-per-registry=32 -n ns --from-image-stream stream --to-image-base cvo --to-image dest --name ver",
		},
		{
			name:        "4.11 with keep-manifest-list",
			config:      &api.ReleaseTagConfiguration{Name: "4.11"},
			namespace:   "ns",
			streamName:  "stream",
			cvo:         "cvo",
			destination: "dest",
			version:     "ver",
			expectedCmd: "oc adm release new --max-per-registry=32 -n ns --from-image-stream stream --to-image-base cvo --to-image dest --name ver --keep-manifest-list",
		},
		{
			name:        "4.15 with keep-manifest-list and reference mode",
			config:      &api.ReleaseTagConfiguration{Name: "4.15", ReferencePolicy: &sourceTagReference},
			namespace:   "ns",
			streamName:  "stream",
			cvo:         "cvo",
			destination: "dest",
			version:     "ver",
			expectedCmd: "oc adm release new --max-per-registry=32 -n ns --from-image-stream stream --to-image-base cvo --to-image dest --name ver --reference-mode=source --keep-manifest-list",
		},
		{
			name:        "malformed version returns no keep-manifest-list",
			config:      &api.ReleaseTagConfiguration{Name: "not-a-version"},
			namespace:   "ns",
			streamName:  "stream",
			cvo:         "cvo",
			destination: "dest",
			version:     "ver",
			expectedCmd: "oc adm release new --max-per-registry=32 -n ns --from-image-stream stream --to-image-base cvo --to-image dest --name ver",
		},
	}

	for _, tt := range tests {
		cmd := buildOcAdmReleaseNewCommand(
			tt.config,
			tt.namespace,
			tt.streamName,
			tt.cvo,
			tt.destination,
			tt.version,
		)

		if diff := cmp.Diff(tt.expectedCmd, cmd); diff != "" {
			t.Fatal(diff)
		}
	}
}
