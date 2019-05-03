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

func TestGetChangedClusterProfiles(t *testing.T) {
	dir, err := ioutil.TempDir("", "")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	profilesPath := filepath.Join(dir, ClusterProfilesPath)
	for _, f := range []string{
		"nochanges/file", "changeme/file", "removeme/file", "moveme/file",
		"renameme/file", "dir/dir/file",
	} {
		path := filepath.Join(dir, ClusterProfilesPath, f)
		if err := os.MkdirAll(filepath.Dir(path), 0775); err != nil {
			t.Fatal(err)
		}
		if err := ioutil.WriteFile(path, []byte(f+"content"), 0664); err != nil {
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
> changeme/file
git rm --quiet removeme/file
mkdir new/ renamed/
> new/file
git add new/file
git mv moveme/file moveme/moved
git mv renameme/file renamed/file
> dir/dir/file
git commit --quiet -am changes
git rev-parse HEAD^
`, profilesPath))
	p.Dir = dir
	out, err := p.CombinedOutput()
	if err != nil {
		t.Fatalf("%q failed, output:\n%s", p.Args, out)
	}
	changed, err := GetChangedClusterProfiles(dir, strings.TrimSpace(string(out)))
	if err != nil {
		t.Fatal(err)
	}
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
	},
	}
	if !reflect.DeepEqual(expected, changed) {
		t.Fatal(diff.ObjectDiff(expected, changed))
	}
}
