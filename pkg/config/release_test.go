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
	f func(string, string),
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
	f(tmp, strings.TrimSpace(string(out)))
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
	expected := CiTemplates{
		filepath.Join(TemplatesPath, "cluster-launch-top-level.yaml"):       "e69de29bb2d1d6434b8b29ae775ad8c2e48c5391",
		filepath.Join(TemplatesPath, "org/repo/cluster-launch-subdir.yaml"): "e69de29bb2d1d6434b8b29ae775ad8c2e48c5391",
	}
	cmp := func(path, rev string) {
		changed, err := GetChangedTemplates(path, rev)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(expected, changed) {
			t.Fatal(diff.ObjectDiff(expected, changed))
		}
	}
	compareChanges(t, TemplatesPath, files, cmd, cmp)
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
	expected := []ClusterProfile{{
		TreeHash: "df2b8fc99e1c1d4dbc0a854d9f72157f1d6ea078",
		Filename: filepath.Join(ClusterProfilesPath, "changeme"),
	}, {
		TreeHash: "b4c3cc91598b6469bf7036502b8ca2bd563b0d0a",
		Filename: filepath.Join(ClusterProfilesPath, "dir"),
	}, {
		TreeHash: "03b9d461447abb84264053a440b4c715842566bb",
		Filename: filepath.Join(ClusterProfilesPath, "moveme"),
	}, {
		TreeHash: "df2b8fc99e1c1d4dbc0a854d9f72157f1d6ea078",
		Filename: filepath.Join(ClusterProfilesPath, "new"),
	}, {
		TreeHash: "9bbab5dcf83793f9edc258136426678cccce940e",
		Filename: filepath.Join(ClusterProfilesPath, "renamed"),
	}}
	cmp := func(path, rev string) {
		changed, err := GetChangedClusterProfiles(path, rev)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(expected, changed) {
			t.Fatal(diff.ObjectDiff(expected, changed))
		}
	}
	compareChanges(t, ClusterProfilesPath, files, cmd, cmp)
}
