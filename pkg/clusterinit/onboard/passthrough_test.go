package onboard

import (
	"context"
	"io/fs"
	"testing"
	"testing/fstest"

	"github.com/google/go-cmp/cmp"
	"github.com/sirupsen/logrus"

	"github.com/openshift/ci-tools/pkg/clusterinit/clusterinstall"
)

func newMemFS(entries ...string) fs.FS {
	memFS := fstest.MapFS{}
	for _, e := range entries {
		memFS[e] = &fstest.MapFile{}
	}
	return memFS
}

func TestPassthroughManifests(t *testing.T) {
	manifestPaths := func(manifests ...string) map[string][]interface{} {
		pathToManifests := make(map[string][]interface{})
		for _, m := range manifests {
			pathToManifests[m] = []interface{}{[]byte{}}
		}
		return pathToManifests
	}
	releaseRepo := "/release/repo"
	for _, tt := range []struct {
		name          string
		ci            clusterinstall.ClusterInstall
		fs            fs.FS
		wantManifests map[string][]interface{}
		wantErr       error
	}{
		{
			name: "Generate manifests",
			ci: clusterinstall.ClusterInstall{
				ClusterName: "build99",
				Onboard: clusterinstall.Onboard{
					ReleaseRepo: releaseRepo,
				},
			},
			fs:            newMemFS("release/repo/build99/foo.yaml"),
			wantManifests: manifestPaths("release/repo/build99/foo.yaml"),
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			generator := NewPassthroughGenerator(logrus.NewEntry(logrus.StandardLogger()), &tt.ci)

			generator.readFile = func(fsys fs.FS, name string) ([]byte, error) { return []byte{}, nil }
			generator.manifests = []fs.FS{tt.fs}

			manifests, err := generator.Generate(context.TODO(), logrus.NewEntry(logrus.StandardLogger()))

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

			if diff := cmp.Diff(tt.wantManifests, manifests); diff != "" {
				t.Errorf("manifest differs:\n%s", diff)
			}
		})
	}
}
