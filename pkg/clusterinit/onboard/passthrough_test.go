package onboard

import (
	"context"
	"io/fs"
	"path"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/sirupsen/logrus"

	"github.com/openshift/ci-tools/pkg/clusterinit/clusterinstall"
)

func newMemFS(entries ...string) fs.FS {
	memFS := fstest.MapFS{}
	for _, e := range entries {
		memFS[path.Join(passthroughRoot, e)] = &fstest.MapFile{}
	}
	return memFS
}

func TestPassthroughManifests(t *testing.T) {
	manifestPaths := func(repo, clusterName string, manifests ...string) []string {
		res := make([]string, 0, len(manifests))
		for _, m := range manifests {
			res = append(res, path.Join(repo, "clusters", "build-clusters", clusterName, m))
		}
		return res
	}
	releaseRepo := "/release/repo"
	for _, tt := range []struct {
		name          string
		ci            clusterinstall.ClusterInstall
		fs            fs.FS
		wantManifests []string
		wantErr       error
	}{
		{
			name: "Write manifests without filters",
			ci: clusterinstall.ClusterInstall{
				ClusterName: "build99",
				Onboard: clusterinstall.Onboard{
					ReleaseRepo: releaseRepo,
				},
			},
			fs:            newMemFS("foo.yaml"),
			wantManifests: manifestPaths("/release/repo", "build99", "foo.yaml"),
		},
		{
			name: "Exclude files",
			ci: clusterinstall.ClusterInstall{
				ClusterName: "build99",
				Onboard: clusterinstall.Onboard{
					ReleaseRepo: releaseRepo,
					PassthroughManifest: clusterinstall.PassthroughManifest{
						Exclude: []string{"super/**", "foo*"},
					},
				},
			},
			fs:            newMemFS("foo.yaml", "bar.yaml", "super/duper.yaml", "super/empty", "empty"),
			wantManifests: manifestPaths("/release/repo", "build99", "bar.yaml", "empty"),
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			step := NewPassthroughStep(logrus.NewEntry(logrus.StandardLogger()), &tt.ci)

			manifests := make([]string, 0)
			step.writeFile = func(name string, _ []byte, _ fs.FileMode) error {
				manifests = append(manifests, name)
				return nil
			}
			step.mkdirAll = func(path string, perm fs.FileMode) error { return nil }
			step.readFile = func(fsys fs.FS, name string) ([]byte, error) { return []byte{}, nil }
			step.manifests = tt.fs

			err := step.Run(context.TODO())

			if err != nil && tt.wantErr == nil {
				t.Fatalf("want err nil but got: %v", err)
			}
			if err == nil && tt.wantErr != nil {
				t.Fatalf("want err %v but nil", tt.wantErr)
			}
			if err != nil && tt.wantErr != nil {
				if tt.wantErr.Error() != err.Error() {
					t.Fatalf("expect error %q but got %q", tt.wantErr.Error(), err.Error())
				}
				return
			}

			sortStr := func(a, b string) bool { return strings.Compare(a, b) <= 0 }
			if diff := cmp.Diff(tt.wantManifests, manifests, cmpopts.SortSlices(sortStr)); diff != "" {
				t.Errorf("manifest paths differs:\n%s", diff)
			}
		})
	}
}
