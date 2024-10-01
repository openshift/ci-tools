package clusterinstall

import (
	"fmt"
	"path"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"k8s.io/utils/ptr"
)

func TestLoad(t *testing.T) {
	for _, tc := range []struct {
		name               string
		clusterInstallPath string
		loadOptions        []LoadOption
		wantClusterInstall ClusterInstall
		wantErr            error
	}{
		{
			name:               "Load",
			clusterInstallPath: path.Join("testdata", "load", "cluster-install-1.yaml"),
			wantClusterInstall: ClusterInstall{
				ClusterName: "foo",
				InstallBase: path.Join("testdata", "load"),
				Provision:   Provision{AWS: &AWSProvision{}},
				Onboard: Onboard{
					OSD:                      ptr.To(true),
					Hosted:                   ptr.To(true),
					Unmanaged:                ptr.To(true),
					UseTokenFileInKubeconfig: ptr.To(true),
				},
			},
		},
		{
			name:               "Load and finalize",
			clusterInstallPath: path.Join("testdata", "load", "cluster-install-1.yaml"),
			loadOptions: []LoadOption{FinalizeOption(FinalizeOptions{
				InstallBase: "/install/base",
				ReleaseRepo: "/release/repo",
			})},
			wantClusterInstall: ClusterInstall{
				ClusterName: "foo",
				InstallBase: "/install/base",
				Provision:   Provision{AWS: &AWSProvision{}},
				Onboard: Onboard{
					ReleaseRepo:              "/release/repo",
					OSD:                      ptr.To(true),
					Hosted:                   ptr.To(true),
					Unmanaged:                ptr.To(true),
					UseTokenFileInKubeconfig: ptr.To(true),
				},
			},
		},
		{
			name:               "File not found",
			clusterInstallPath: "fake",
			wantErr:            fmt.Errorf("read file fake: open fake: no such file or directory"),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			clusterInstall, err := Load(tc.clusterInstallPath, tc.loadOptions...)

			if err != nil && tc.wantErr == nil {
				t.Fatalf("want err nil but got: %v", err)
			}
			if err == nil && tc.wantErr != nil {
				t.Fatalf("want err %v but got nil", tc.wantErr)
			}
			if err != nil && tc.wantErr != nil {
				if tc.wantErr.Error() != err.Error() {
					t.Fatalf("expect error %q but got %q", tc.wantErr.Error(), err.Error())
				}
				return
			}

			if diff := cmp.Diff(tc.wantClusterInstall, *clusterInstall); diff != "" {
				t.Errorf("cluster install differs:\n%s", diff)
			}
		})
	}
}

func TestLoadFromDir(t *testing.T) {
	for _, tc := range []struct {
		name                string
		clusterInstallsDir  string
		loadOptions         []LoadOption
		wantClusterInstalls map[string]*ClusterInstall
		wantErr             error
	}{
		{
			name:               "Load",
			clusterInstallsDir: path.Join("testdata", "load-from-dir", "dir1"),
			wantClusterInstalls: map[string]*ClusterInstall{
				"foo": {
					ClusterName: "foo",
					InstallBase: path.Join("testdata", "load-from-dir", "dir1"),
					Provision:   Provision{AWS: &AWSProvision{}},
					Onboard: Onboard{
						OSD:                      ptr.To(true),
						Hosted:                   ptr.To(true),
						Unmanaged:                ptr.To(true),
						UseTokenFileInKubeconfig: ptr.To(true),
					},
				},
				"bar": {
					ClusterName: "bar",
					InstallBase: path.Join("testdata", "load-from-dir", "dir1"),
					Provision:   Provision{GCP: &GCPProvision{}},
					Onboard: Onboard{
						OSD:                      ptr.To(true),
						Hosted:                   ptr.To(true),
						Unmanaged:                ptr.To(true),
						UseTokenFileInKubeconfig: ptr.To(true),
					},
				},
			},
		},
		{
			name:               "Load and finalize",
			clusterInstallsDir: path.Join("testdata", "load-from-dir", "dir1"),
			loadOptions: []LoadOption{FinalizeOption(FinalizeOptions{
				InstallBase: "/install/base",
				ReleaseRepo: "/release/repo",
			})},
			wantClusterInstalls: map[string]*ClusterInstall{
				"foo": {
					ClusterName: "foo",
					InstallBase: "/install/base",
					Provision:   Provision{AWS: &AWSProvision{}},
					Onboard: Onboard{
						ReleaseRepo:              "/release/repo",
						OSD:                      ptr.To(true),
						Hosted:                   ptr.To(true),
						Unmanaged:                ptr.To(true),
						UseTokenFileInKubeconfig: ptr.To(true),
					},
				},
				"bar": {
					ClusterName: "bar",
					InstallBase: "/install/base",
					Provision:   Provision{GCP: &GCPProvision{}},
					Onboard: Onboard{
						ReleaseRepo:              "/release/repo",
						OSD:                      ptr.To(true),
						Hosted:                   ptr.To(true),
						Unmanaged:                ptr.To(true),
						UseTokenFileInKubeconfig: ptr.To(true),
					},
				},
			},
		},
		{
			name:               "File not found",
			clusterInstallsDir: "fake",
			wantErr:            fmt.Errorf("read dir fake: lstat fake: no such file or directory"),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			clusterInstalls, err := LoadFromDir(tc.clusterInstallsDir, tc.loadOptions...)

			if err != nil && tc.wantErr == nil {
				t.Fatalf("want err nil but got: %v", err)
			}
			if err == nil && tc.wantErr != nil {
				t.Fatalf("want err %v but got nil", tc.wantErr)
			}
			if err != nil && tc.wantErr != nil {
				if tc.wantErr.Error() != err.Error() {
					t.Fatalf("expect error %q but got %q", tc.wantErr.Error(), err.Error())
				}
				return
			}

			sortFunc := func(a, b *ClusterInstall) bool { return strings.Compare(a.ClusterName, b.ClusterName) <= 0 }
			if diff := cmp.Diff(tc.wantClusterInstalls, clusterInstalls, cmpopts.SortMaps(sortFunc)); diff != "" {
				t.Errorf("cluster install differs:\n%s", diff)
			}
		})
	}
}
