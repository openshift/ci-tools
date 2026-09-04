package release

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	coreapi "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	imagev1 "github.com/openshift/api/image/v1"

	"github.com/openshift/ci-tools/pkg/api"
)

func TestSnapshotImportSource(t *testing.T) {
	specPull := "quay-proxy.ci.openshift.org/openshift/ci@sha256:abc"
	base := api.ImageStreamTagReference{Namespace: "ocp", Name: "4.18", Tag: "cluster-version-operator"}
	tests := []struct {
		name       string
		namespace  string
		stream     string
		tag        string
		source     *imagev1.ImageStream
		tagSources map[string]coreapi.ObjectReference
		wantOK     bool
		wantFrom   *coreapi.ObjectReference
	}{
		{
			name:   "ocp spec first",
			stream: "4.18",
			tag:    base.Tag,
			source: &imagev1.ImageStream{
				ObjectMeta: metav1.ObjectMeta{Namespace: "ocp", Name: "4.18"},
				Spec: imagev1.ImageStreamSpec{Tags: []imagev1.TagReference{{
					Name: base.Tag,
					From: &coreapi.ObjectReference{Kind: "DockerImage", Name: specPull},
				}}},
			},
			wantOK:   true,
			wantFrom: &coreapi.ObjectReference{Kind: "DockerImage", Name: specPull},
		},
		{
			name:     "consolidated quay fallback",
			stream:   "4.18",
			tag:      base.Tag,
			source:   &imagev1.ImageStream{ObjectMeta: metav1.ObjectMeta{Namespace: "ocp", Name: "4.18"}},
			wantOK:   true,
			wantFrom: &coreapi.ObjectReference{Kind: "DockerImage", Name: api.QuayImageReference(base)},
		},
		{
			name:     "ocp missing source imagestream without tag sources",
			stream:   "4.22",
			tag:      base.Tag,
			wantOK:   false,
			wantFrom: nil,
		},
		{
			name:   "ocp uses integrated stream spec when source missing",
			stream: "5.1",
			tag:    "karpenter-operator",
			tagSources: map[string]coreapi.ObjectReference{
				"karpenter-operator": {Kind: "DockerImage", Name: specPull},
			},
			wantOK:   true,
			wantFrom: &coreapi.ObjectReference{Kind: "DockerImage", Name: specPull},
		},
		{
			name:   "rejects internal registry tag source",
			stream: "5.1",
			tag:    "cli",
			tagSources: map[string]coreapi.ObjectReference{
				"cli": {Kind: "DockerImage", Name: api.ServiceDomainAPPCIRegistry + "/origin/4.23:cli"},
			},
			wantOK: false,
		},
		{
			name:   "rejects unsupported tag source kind",
			stream: "5.1",
			tag:    "cli",
			tagSources: map[string]coreapi.ObjectReference{
				"cli": {Kind: "ConfigMap", Name: "pull-secret"},
			},
			wantOK: false,
		},
		{
			name:   "ocp spec docker 4.23",
			stream: "4.23",
			tag:    "cli",
			source: &imagev1.ImageStream{
				ObjectMeta: metav1.ObjectMeta{Namespace: "ocp", Name: "4.23"},
				Spec: imagev1.ImageStreamSpec{Tags: []imagev1.TagReference{{
					Name: "cli",
					From: &coreapi.ObjectReference{Kind: "DockerImage", Name: specPull},
				}}},
			},
			wantOK:   true,
			wantFrom: &coreapi.ObjectReference{Kind: "DockerImage", Name: specPull},
		},
		{
			name:     "default quay float",
			stream:   "4.23",
			tag:      "cli",
			source:   &imagev1.ImageStream{ObjectMeta: metav1.ObjectMeta{Namespace: "ocp", Name: "4.23"}},
			wantOK:   true,
			wantFrom: &coreapi.ObjectReference{Kind: "DockerImage", Name: api.QuayImageReference(api.ImageStreamTagReference{Namespace: "ocp", Name: "4.23", Tag: "cli"})},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			namespace := tt.namespace
			if namespace == "" {
				namespace = "ocp"
			}
			from, ok := snapshotImportSource(namespace, tt.stream, tt.tag, tt.source, tt.tagSources)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if !ok {
				return
			}
			if diff := cmp.Diff(tt.wantFrom, from); diff != "" {
				t.Fatalf("from mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
