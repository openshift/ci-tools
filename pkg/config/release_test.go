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

	"k8s.io/apimachinery/pkg/util/diff"
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
