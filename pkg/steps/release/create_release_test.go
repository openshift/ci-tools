package release

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	imagev1 "github.com/openshift/api/image/v1"

	"github.com/openshift/ci-tools/pkg/api"
)

func TestAssembleReleaseVersionPrefix(t *testing.T) {
	tests := []struct {
		name   string
		config *api.ReleaseTagConfiguration
		want   string
	}{
		{name: "nil config", want: "0.0.1-0"},
		{name: "unparseable", config: &api.ReleaseTagConfiguration{Name: "not-a-version"}, want: "0.0.1-0"},
		{name: "4.19 stream", config: &api.ReleaseTagConfiguration{Name: "4.19"}, want: "4.19.0-0"},
		{name: "5.0 stream", config: &api.ReleaseTagConfiguration{Name: "5.0"}, want: "5.0.0-0"},
		{name: "5.0-priv stream", config: &api.ReleaseTagConfiguration{Name: "5.0-priv"}, want: "5.0.0-0"},
		{name: "4.19-priv stream", config: &api.ReleaseTagConfiguration{Name: "4.19-priv"}, want: "4.19.0-0"},
		{name: "5.0-private ignored", config: &api.ReleaseTagConfiguration{Name: "5.0-private"}, want: "0.0.1-0"},
		{name: "extra component", config: &api.ReleaseTagConfiguration{Name: "4.19.1"}, want: "0.0.1-0"},
		{name: "suffix", config: &api.ReleaseTagConfiguration{Name: "4.19-suffix"}, want: "0.0.1-0"},
		{name: "trailing letters", config: &api.ReleaseTagConfiguration{Name: "4.19x"}, want: "0.0.1-0"},
		{name: "signed major", config: &api.ReleaseTagConfiguration{Name: "-1.2"}, want: "0.0.1-0"},
		{name: "signed minor", config: &api.ReleaseTagConfiguration{Name: "4.-19"}, want: "0.0.1-0"},
		{name: "leading whitespace", config: &api.ReleaseTagConfiguration{Name: " 4.19"}, want: "0.0.1-0"},
		{name: "trailing whitespace", config: &api.ReleaseTagConfiguration{Name: "4.19 "}, want: "0.0.1-0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if diff := cmp.Diff(tt.want, assembleReleaseVersionPrefix(tt.config)); diff != "" {
				t.Fatal(diff)
			}
		})
	}
}

func TestBuildOcAdmReleaseNewCommand(t *testing.T) {
	sourceTagReference := imagev1.SourceTagReferencePolicy

	t.Run("stream", func(t *testing.T) {
		tests := []struct {
			name        string
			config      *api.ReleaseTagConfiguration
			namespace   string
			streamName  string
			cvo         string
			destination string
			version     string
			expectedCmd string
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
				name:        "4.12 with keep-manifest-list and reference mode",
				config:      &api.ReleaseTagConfiguration{Name: "4.12", ReferencePolicy: &sourceTagReference},
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
			{
				name:        "malformed version with reference policy yields no extra flags",
				config:      &api.ReleaseTagConfiguration{Name: "oops", ReferencePolicy: &sourceTagReference},
				namespace:   "ns",
				streamName:  "stream",
				cvo:         "cvo",
				destination: "dest",
				version:     "ver",
				expectedCmd: "oc adm release new --max-per-registry=32 -n ns --from-image-stream stream --to-image-base cvo --to-image dest --name ver",
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				cmd := joinOcAdmReleaseNewCommand(tt.config, tt.namespace, tt.cvo, tt.destination, tt.version, "--from-image-stream", tt.streamName)
				if diff := cmp.Diff(tt.expectedCmd, cmd); diff != "" {
					t.Fatal(diff)
				}
			})
		}
	})

	t.Run("assemble_script", func(t *testing.T) {
		srcPol := imagev1.SourceTagReferencePolicy
		config := &api.ReleaseTagConfiguration{Name: "4.12", ReferencePolicy: &srcPol}
		got := buildOcAdmReleaseNewCommand(config, "test-ns", "stable", "cvo-pullspec", "dest:tag", "0.0.1-ver")
		want := `_CI_RELEASE_IS_FILE="/tmp/ci-operator-release-is-stable.yaml"
if oc get imagestream "stable" -n "test-ns" -o yaml > "${_CI_RELEASE_IS_FILE}" 2>/dev/null; then
  oc adm release new --max-per-registry=32 -n test-ns --from-image-stream-file ${_CI_RELEASE_IS_FILE} --to-image-base cvo-pullspec --to-image dest:tag --name 0.0.1-ver --keep-manifest-list || oc adm release new --max-per-registry=32 -n test-ns --from-image-stream stable --to-image-base cvo-pullspec --to-image dest:tag --name 0.0.1-ver --keep-manifest-list
else
  oc adm release new --max-per-registry=32 -n test-ns --from-image-stream stable --to-image-base cvo-pullspec --to-image dest:tag --name 0.0.1-ver --keep-manifest-list
fi`
		if diff := cmp.Diff(want, got); diff != "" {
			t.Fatalf("buildOcAdmReleaseNewCommand() mismatch (-want +got):\n%s", diff)
		}
		got = buildOcAdmReleaseNewCommand(&api.ReleaseTagConfiguration{Name: "4.23", ReferencePolicy: &srcPol}, "test-ns", "stable", "cvo-pullspec", "dest:tag", "0.0.1-ver")
		want = "oc adm release new --max-per-registry=32 -n test-ns --from-image-stream stable --to-image-base cvo-pullspec --to-image dest:tag --name 0.0.1-ver --reference-mode=source --keep-manifest-list"
		if diff := cmp.Diff(want, got); diff != "" {
			t.Fatalf("buildOcAdmReleaseNewCommand() mismatch (-want +got):\n%s", diff)
		}
	})
}
