package config

import (
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"k8s.io/apimachinery/pkg/util/diff"
	"k8s.io/test-infra/prow/plugins"
)

func compareChanges(
	t *testing.T,
	path string,
	files []string,
	cmd string,
	f func(string, string) ([]string, error),
	expected []string,
) {
	t.Helper()
	tmp, err := ioutil.TempDir("", "")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)
	dir := filepath.Join(tmp, path)
	for _, f := range files {
		n := filepath.Join(dir, f)
		if err := os.MkdirAll(filepath.Dir(n), 0775); err != nil {
			t.Fatal(err)
		}
		if err := ioutil.WriteFile(n, []byte(f+"content"), 0664); err != nil {
			t.Fatal(err)
		}
	}
	p := exec.Command("sh", "-ec", fmt.Sprintf(`
git init --quiet .
git config user.name test
git config user.email test
git config commit.gpgsign false
git add .
git commit --quiet -m initial
cd %s
%s
git commit --quiet --all --message changes
git rev-parse HEAD^
`, path, cmd))
	p.Dir = tmp
	out, err := p.CombinedOutput()
	if err != nil {
		t.Fatalf("%q failed, output:\n%s", p.Args, out)
	}
	changed, err := f(dir, strings.TrimSpace(string(out)))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(expected, changed) {
		t.Fatal(diff.ObjectDiff(expected, changed))
	}
}

func TestGetChangedTemplates(t *testing.T) {
	files := []string{
		"cluster-launch-top-level.yaml", "org/repo/cluster-launch-subdir.yaml",
		"org/repo/OWNERS", "org/repo/README.md",
	}
	cmd := `
> cluster-launch-top-level.yaml
> org/repo/cluster-launch-subdir.yaml
> org/repo/OWNERS
> org/repo/README.md
`
	expected := []string{
		filepath.Join(TemplatesPath, "cluster-launch-top-level.yaml"),
		filepath.Join(TemplatesPath, "org/repo/cluster-launch-subdir.yaml"),
	}
	compareChanges(t, TemplatesPath, files, cmd, GetChangedTemplates, expected)
}

func TestGetChangedClusterProfiles(t *testing.T) {
	files := []string{
		"nochanges/file", "changeme/file", "removeme/file", "moveme/file",
		"renameme/file", "dir/dir/file",
	}
	cmd := `
> changeme/file
git rm --quiet removeme/file
mkdir new/ renamed/
> new/file
git add new/file
git mv moveme/file moveme/moved
git mv renameme/file renamed/file
> dir/dir/file
`
	expected := []string{
		filepath.Join(ClusterProfilesPath, "changeme", "file"),
		filepath.Join(ClusterProfilesPath, "dir", "dir", "file"),
		filepath.Join(ClusterProfilesPath, "moveme", "moved"),
		filepath.Join(ClusterProfilesPath, "new", "file"),
		filepath.Join(ClusterProfilesPath, "renamed", "file"),
	}
	compareChanges(t, ClusterProfilesPath, files, cmd, GetChangedClusterProfiles, expected)
}

func TestGetAddedConfigs(t *testing.T) {
	files := []string{
		"nochanges/file", "changeme/file", "removeme/file", "moveme/file",
		"renameme/file", "dir/dir/file",
	}
	cmd := `
> changeme/file
git rm --quiet removeme/file
mkdir new/ renamed/
> new/file
git add new/file
git mv moveme/file moveme/moved
git mv renameme/file renamed/file
> dir/dir/file
`
	expected := []string{
		filepath.Join(CiopConfigInRepoPath, "moveme", "moved"),
		filepath.Join(CiopConfigInRepoPath, "new", "file"),
		filepath.Join(CiopConfigInRepoPath, "renamed", "file"),
	}
	compareChanges(t, CiopConfigInRepoPath, files, cmd, GetAddedConfigs, expected)
}

func TestConfigMapName(t *testing.T) {
	path := "path/to/a-file.yaml"
	dnfError := fmt.Errorf("path not covered by any config-updater pattern: path/to/a-file.yaml")
	cm := "a-config-map"

	testCases := []struct {
		description string
		maps        map[string]string

		expectName    string
		expectPattern string
		expectError   error
	}{
		{
			description: "empty config",
			expectError: dnfError,
		},
		{
			description: "no pattern applies",
			maps:        map[string]string{"path/to/different/a-file.yaml": cm},
			expectError: dnfError,
		},
		{
			description:   "direct path",
			maps:          map[string]string{path: cm},
			expectPattern: path,
			expectName:    cm,
		},
		{
			description:   "glob",
			maps:          map[string]string{"path/to/*.yaml": cm},
			expectPattern: "path/to/*.yaml",
			expectName:    cm,
		},
		{
			description: "brace",
			// zglob is buggy: https://github.com/mattn/go-zglob/pull/31
			// maps:          map[string]string{"path/to/a-{file,dir}.yaml": cm},
			maps:          map[string]string{"path/to/*-{file,dir}.yaml": cm},
			expectPattern: "path/to/*-{file,dir}.yaml",
			expectName:    cm,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			cuCfg := plugins.ConfigUpdater{
				Maps: map[string]plugins.ConfigMapSpec{},
			}
			for k, v := range tc.maps {
				cuCfg.Maps[k] = plugins.ConfigMapSpec{
					Name: v,
				}
			}

			name, pattern, err := ConfigMapName(path, cuCfg)
			if (tc.expectError == nil) != (err == nil) {
				t.Fatalf("Did not return error as expected:\n%s", cmp.Diff(tc.expectError, err))
			} else if tc.expectError != nil && err != nil && tc.expectError.Error() != err.Error() {
				t.Fatalf("Expected different error:\n%s", cmp.Diff(tc.expectError.Error(), err.Error()))
			}

			if err == nil {
				if diffName := cmp.Diff(tc.expectName, name); diffName != "" {
					t.Errorf("ConfigMap name differs:\n%s", diffName)
				}
				if diffPattern := cmp.Diff(tc.expectPattern, pattern); diffPattern != "" {
					t.Errorf("Pattern differs:\n%s", diffPattern)
				}
			}
		})
	}
}
