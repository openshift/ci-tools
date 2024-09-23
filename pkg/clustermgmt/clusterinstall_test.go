package clustermgmt

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"k8s.io/utils/ptr"
)

func TestApplyDefault(t *testing.T) {
	ci := ClusterInstall{}
	applyDefaults(&ci)
	wantCI := ClusterInstall{
		Onboard: Onboard{
			Hosted:    ptr.To(false),
			Unmanaged: ptr.To(false),
			OSD:       ptr.To(true),
		},
	}
	if diff := cmp.Diff(wantCI, ci); diff != "" {
		t.Errorf("unexpected diff:\n%s", diff)
	}
}
