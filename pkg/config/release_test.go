package config

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"k8s.io/apimachinery/pkg/util/diff"
	"sigs.k8s.io/prow/pkg/plugins"

	"github.com/openshift/ci-tools/pkg/registry"
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
	tmp, err := os.MkdirTemp("", "")
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
		if err := os.WriteFile(n, []byte(f+"content"), 0664); err != nil {
			t.Fatal(err)
		}
	}
	p := exec.Command("sh", "-ec", fmt.Sprintf(`
git init --quiet .
git config user.name test
git config user.email test
git config commit.gpgsign false
git config core.hooksPath /dev/null
git add .
git commit --quiet -m initial
cd %s
%s
git commit --quiet --all --message changes
git rev-parse HEAD^
`, path, cmd))
	p.Dir = tmp
	// Use Output() instead of CombinedOutput() to avoid pre-commit hook output on stderr
	out, err := p.Output()
	if err != nil {
		// If Output() fails, try CombinedOutput() for error details
		combinedOut, combinedErr := p.CombinedOutput()
		if combinedErr != nil {
			t.Fatalf("%q failed, output:\n%s", p.Args, combinedOut)
		}
		out = combinedOut
	}
	commitHash := strings.TrimSpace(string(out))
	if len(commitHash) != 40 {
		t.Fatalf("invalid commit hash from git rev-parse: %q", commitHash)
	}
	changed, err := f(dir, commitHash)
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

type testNode struct {
	string
}

func (t testNode) Name() string {
	return t.string
}

func (t testNode) Type() registry.Type          { return 0 }
func (t testNode) Ancestors() []registry.Node   { return nil }
func (t testNode) Descendants() []registry.Node { return nil }
func (t testNode) Parents() []registry.Node     { return nil }
func (t testNode) Children() []registry.Node    { return nil }

var _ registry.Node = &testNode{}

func TestGetChangedRegistrySteps(t *testing.T) {
	files := []string{
		"ipi/conf/aws/ipi-conf-aws-chain.yaml",
		"ipi/conf/aws/ipi-conf-aws-ref.yaml",
		"ipi/conf/aws/ipi-conf-aws-commands.sh",
		"ipi/conf/aws/ipi-conf-gcp-chain.yaml",
		"ipi/conf/aws/ipi-conf-gcp-ref.yaml",
		"ipi/conf/aws/ipi-conf-gcp-commands.sh",
		"observer/test/observer-test-observer.yaml",
		"observer/test/observer-test-commands.sh",
		"openshift/e2e/test/openshift-e2e-test-ref.yaml",
		"openshift/e2e/test/openshift-e2e-test-commands.sh",
		"upi/aws/upi-aws-workflow.yaml",
		"upi/gcp/upi-gcp-workflow.yaml",
	}
	cmd := `
> ipi/conf/aws/ipi-conf-aws-chain.yaml
> observer/test/observer-test-observer.yaml
> observer/test/observer-test-commands.sh
> openshift/e2e/test/openshift-e2e-test-ref.yaml
> openshift/e2e/test/openshift-e2e-test-commands.sh
> upi/aws/upi-aws-workflow.yaml
git add \
    ipi/conf/aws/ipi-conf-aws-chain.yaml \
    observer/test/observer-test-observer.yaml \
    observer/test/observer-test-commands.sh \
    openshift/e2e/test/openshift-e2e-test-ref.yaml \
    openshift/e2e/test/openshift-e2e-test-commands.sh \
    upi/aws/upi-aws-workflow.yaml
`
	graph := registry.NodeByName{
		Chains: map[string]registry.Node{
			"ipi-conf-aws": &testNode{"chain/ipi-conf-aws"},
			"ipi-conf-gcp": &testNode{"chain/ipi-conf-gcp"},
		},
		Observers: map[string]registry.Node{
			"observer-test": &testNode{"observer/test"},
		},
		References: map[string]registry.Node{
			"ipi-conf-aws":       &testNode{"ref/ipi-conf-aws"},
			"ipi-conf-gcp":       &testNode{"ref/ipi-conf-gcp"},
			"openshift-e2e-test": &testNode{"ref/openshift-e2e-test"},
		},
		Workflows: map[string]registry.Node{
			"upi-aws": &testNode{"workflow/upi-aws"},
			"upi-gcp": &testNode{"workflow/upi-gcp"},
		},
	}
	f := func(path string, baseRev string) (ret []string, _ error) {
		nodes, err := GetChangedRegistrySteps(path, baseRev, graph)
		for _, x := range nodes {
			ret = append(ret, x.Name())
		}
		return ret, err
	}
	compareChanges(t, RegistryPath, files, cmd, f, []string{
		"chain/ipi-conf-aws",
		"observer/test",
		"observer/test",
		"ref/openshift-e2e-test",
		"ref/openshift-e2e-test",
		"workflow/upi-aws",
	})
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
