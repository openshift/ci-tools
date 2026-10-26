package main

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ghodss/yaml"
	"github.com/google/go-cmp/cmp"
	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/clusterimageset"
	"github.com/openshift/ci-tools/pkg/testhelper"
	hivev1 "github.com/openshift/hive/apis/hive/v1"
)

func TestRun(t *testing.T) {
	if err := addSchemes(); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name            string
		golden          bool
		failures        []string
		shared          bool
		protectedTarget bool
		noncanonical    bool
		fault           string
		wantError       string
	}{
		{name: "existing golden output", golden: true},
		{name: "all successful colliding streams"},
		{name: "failure before successes", failures: []string{"a"}},
		{name: "failure after successes", failures: []string{"c"}},
		{name: "all lookups fail", failures: []string{"a", "b", "c"}},
		{name: "shared old reference survives successful group cleanup", failures: []string{"a"}, shared: true},
		{name: "successful target is a preserved image set", failures: []string{"a"}, shared: true, protectedTarget: true},
		{name: "preserved image set at noncanonical path is reused", failures: []string{"a"}, shared: true, protectedTarget: true, noncanonical: true},
		{name: "conflicting preserved image set is unsafe", failures: []string{"a"}, shared: true, protectedTarget: true, fault: "conflict", wantError: "conflicts with preserved"},
		{name: "invalid pool is unsafe", fault: "pool", wantError: "failed to get list"},
		{name: "invalid bounds are unsafe", fault: "bounds", wantError: "invalid version bounds"},
		{name: "duplicate image names are unsafe", failures: []string{"a"}, fault: "duplicate", wantError: "duplicate clusterimageset"},
		{name: "invalid image set is unsafe after lookup failure", failures: []string{"a"}, fault: "image", wantError: "failed to scan"},
		{name: "write failure is unsafe after lookup failure", failures: []string{"a"}, fault: "write", wantError: "failed to write file"},
		{name: "cleanup failure is unsafe after lookup failure", failures: []string{"a"}, fault: "cleanup", wantError: "failed to delete file"},
		{name: "missing preserved reference is unsafe", failures: []string{"a"}, fault: "missing", wantError: "references missing"},
		{name: "invalid response pullspec is unsafe", failures: []string{"a"}, fault: "pullspec", wantError: "invalid release pullspec"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			poolDir, imagesetDir := t.TempDir(), t.TempDir()
			o := options{poolDir: poolDir, outputDir: imagesetDir, releaseControllerURL: "https://release.example/api/v1/releasestream"}
			client := &lookupClient{failures: tc.failures, invalidPullspec: tc.fault == "pullspec"}
			before := map[string][]byte{}
			if tc.golden {
				copyDir(t, "testdata/run/input/pools", poolDir)
				copyDir(t, "testdata/run/input/imagesets", imagesetDir)
			} else {
				for _, stream := range []string{"a", "b", "c"} {
					ref := "old-" + stream
					imageStream := stream
					if tc.shared {
						ref, imageStream = "shared", "c"
					}
					if tc.protectedTarget {
						ref = nameFromPullspec(testPullspec, testBounds("c"))
					}
					poolPath := filepath.Join(poolDir, stream+"_clusterpool.yaml")
					pool := fmt.Sprintf("apiVersion: hive.openshift.io/v1\nkind: ClusterPool\nmetadata:\n  name: pool-%s\n  namespace: pools\n  labels:\n    owner: test\n    version_lower: 4.21.0-0\n    version_upper: 4.22.0-0\n    version_stream: %s\nspec:\n  imageSetRef:\n    name: %s\n", stream, stream, ref)
					writeTestFile(t, poolPath, []byte(pool))
					if tc.fault == "bounds" && stream == "a" {
						writeTestFile(t, poolPath, []byte(strings.ReplaceAll(pool, "4.21.0-0", "invalid")))
					}
					imagePath := filepath.Join(imagesetDir, ref+"_clusterimageset.yaml")
					pullspec := "quay.io/example/ocp-release:old"
					if tc.noncanonical {
						imagePath = filepath.Join(imagesetDir, "preserved_clusterimageset.yaml")
					}
					if tc.protectedTarget && tc.fault != "conflict" {
						pullspec = testPullspec
					}
					image := fmt.Sprintf("apiVersion: hive.openshift.io/v1\nkind: ClusterImageSet\nmetadata:\n  name: %s\n  annotations:\n    version_lower: 4.21.0-0\n    version_upper: 4.22.0-0\n    version_stream: %s\nspec:\n  releaseImage: %s\n", ref, imageStream, pullspec)
					writeTestFile(t, imagePath, []byte(image))
					unusedPath := filepath.Join(imagesetDir, "unused-"+stream+"_clusterimageset.yaml")
					unused := strings.ReplaceAll(image, "name: "+ref+"\n", "name: unused-"+stream+"\n")
					unused = strings.ReplaceAll(unused, "version_stream: "+imageStream+"\n", "version_stream: "+stream+"\n")
					writeTestFile(t, unusedPath, []byte(unused))
					if tc.fault == "duplicate" && stream == "a" {
						writeTestFile(t, filepath.Join(imagesetDir, "duplicate_clusterimageset.yaml"), []byte(image))
					}
					if slices.Contains(tc.failures, stream) {
						before[poolPath], before[imagePath] = []byte(pool), []byte(image)
						before[unusedPath] = []byte(unused)
					}
				}
			}
			switch tc.fault {
			case "pool":
				writeTestFile(t, filepath.Join(poolDir, "broken_clusterpool.yaml"), []byte("["))
			case "image":
				writeTestFile(t, filepath.Join(imagesetDir, "broken_clusterimageset.yaml"), []byte("["))
			case "write":
				if err := os.Mkdir(clusterImageSetYAMLPath(imagesetDir, testPullspec, testBounds("c")), 0755); err != nil {
					t.Fatal(err)
				}
			case "cleanup":
				o.removeImageSet = failRemoval
			case "missing":
				if err := os.Remove(filepath.Join(imagesetDir, "old-a_clusterimageset.yaml")); err != nil {
					t.Fatal(err)
				}
			}
			err := run(o, client)
			var partial *clusterimageset.LookupFailures
			if tc.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantError) || errors.As(err, &partial) {
					t.Fatalf("want unsafe error containing %q, got %v", tc.wantError, err)
				}
				return
			}
			if len(tc.failures) > 0 {
				if !errors.As(err, &partial) {
					t.Fatalf("want lookup-only failure, got %v", err)
				}
				for _, stream := range tc.failures {
					for _, diagnostic := range []string{"pools/pool-" + stream, stream + "_clusterpool.yaml", "stream \"" + stream + "\"", "lower \"4.21.0-0\"", "upper \"4.22.0-0\"", "unavailable-" + stream} {
						if !strings.Contains(err.Error(), diagnostic) {
							t.Errorf("missing diagnostic %q: %v", diagnostic, err)
						}
					}
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if !tc.golden {
				if diff := cmp.Diff([]string{"a", "b", "c"}, client.streams); diff != "" {
					t.Errorf("lookup order (-want +got):\n%s", diff)
				}
			}
			for path, want := range before {
				got, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if diff := cmp.Diff(string(want), string(got)); diff != "" {
					t.Errorf("preserved file %s (-want +got):\n%s", path, diff)
				}
			}
			images := map[string]hivev1.ClusterImageSet{}
			for _, dir := range []struct{ path, prefix string }{{imagesetDir, "imagesets-"}, {poolDir, "pools-"}} {
				entries, err := os.ReadDir(dir.path)
				if err != nil {
					t.Fatal(err)
				}
				for _, entry := range entries {
					data, err := os.ReadFile(filepath.Join(dir.path, entry.Name()))
					if err != nil {
						t.Fatal(err)
					}
					if tc.golden {
						testhelper.CompareWithFixture(t, data, testhelper.WithPrefix(dir.prefix+entry.Name()+"-"))
					}
					if dir.path == imagesetDir {
						var image hivev1.ClusterImageSet
						if err := yaml.Unmarshal(data, &image); err != nil {
							t.Fatal(err)
						}
						images[image.Name] = image
						continue
					}
					var pool hivev1.ClusterPool
					if err := yaml.Unmarshal(data, &pool); err != nil {
						t.Fatal(err)
					}
					image, ok := images[pool.Spec.ImageSetRef.Name]
					if !ok {
						t.Fatalf("dangling reference in %s: %s", pool.Name, pool.Spec.ImageSetRef.Name)
					}
					if !slices.Contains(tc.failures, pool.Labels[versionStreamLabel]) && image.Spec.ReleaseImage != testPullspec {
						t.Errorf("successful pool not updated: %s", pool.Name)
					}
				}
			}
		})
	}
}

const testPullspec = "quay.io/openshift-release-dev/ocp-release:4.21.99-multi"

type lookupClient struct {
	failures        []string
	streams         []string
	invalidPullspec bool
}

func (client *lookupClient) Do(request *http.Request) (*http.Response, error) {
	stream := strings.TrimSuffix(strings.TrimPrefix(request.URL.Path, "/api/v1/releasestream/"), "-multi/latest")
	client.streams = append(client.streams, stream)
	if slices.Contains(client.failures, stream) {
		return nil, fmt.Errorf("unavailable-%s", stream)
	}
	pullspec := testPullspec
	if client.invalidPullspec {
		pullspec = ""
	}
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(fmt.Sprintf(`{"name":"4.21.99","phase":"Accepted","pullSpec":%q}`, pullspec)))}, nil
}

func testBounds(stream string) api.VersionBounds {
	return api.VersionBounds{Lower: "4.21.0-0", Upper: "4.22.0-0", Stream: stream}
}

func failRemoval(path string) error { return fmt.Errorf("cannot remove %s", path) }

func writeTestFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
}

func copyDir(t *testing.T, src, dst string) {
	t.Helper()
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		data, err := os.ReadFile(filepath.Join(src, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dst, e.Name()), data, 0644); err != nil {
			t.Fatal(err)
		}
	}
}
