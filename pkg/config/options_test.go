package config

import (
	"flag"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/google/go-cmp/cmp"

	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/openshift/ci-tools/pkg/api"
	cioperatorapi "github.com/openshift/ci-tools/pkg/api"
	jc "github.com/openshift/ci-tools/pkg/jobconfig"
	"github.com/openshift/ci-tools/pkg/testhelper"
)

func TestOptions_Bind(t *testing.T) {
	var testCases = []struct {
		name     string
		input    []string
		expected Options
	}{
		{
			name:  "nothing set has defaults",
			input: []string{},
			expected: Options{
				LogLevel: "info",
			},
		},
		{
			name: "everything set",
			input: []string{
				"--config-dir=foo",
				"--org=bar",
				"--repo=baz",
				"--log-level=debug",
			},
			expected: Options{
				ConfigDir: "foo",
				Org:       "bar",
				Repo:      "baz",
				LogLevel:  "debug",
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			var o Options
			fs := flag.NewFlagSet(testCase.name, flag.PanicOnError)
			o.Bind(fs)
			if err := fs.Parse(testCase.input); err != nil {
				t.Fatalf("%s: cannot parse args: %v", testCase.name, err)
			}
			if actual, expected := o, testCase.expected; !reflect.DeepEqual(actual, expected) {
				t.Errorf("%s: got incorrect options: expected %v, got %v", testCase.name, expected, actual)
			}
		})
	}
}

func TestConfirmableOptions_Bind(t *testing.T) {
	var testCases = []struct {
		name     string
		input    []string
		expected ConfirmableOptions
	}{
		{
			name:  "nothing set has defaults",
			input: []string{},
			expected: ConfirmableOptions{
				Options: Options{
					LogLevel: "info",
				},
			},
		},
		{
			name: "everything set",
			input: []string{
				"--config-dir=foo",
				"--org=bar",
				"--repo=baz",
				"--log-level=debug",
				"--confirm",
			},
			expected: ConfirmableOptions{
				Options: Options{
					ConfigDir: "foo",
					Org:       "bar",
					Repo:      "baz",
					LogLevel:  "debug",
				},
				Confirm: true,
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			var o ConfirmableOptions
			fs := flag.NewFlagSet(testCase.name, flag.PanicOnError)
			o.Bind(fs)
			if err := fs.Parse(testCase.input); err != nil {
				t.Fatalf("%s: cannot parse args: %v", testCase.name, err)
			}
			if actual, expected := o, testCase.expected; !reflect.DeepEqual(actual, expected) {
				t.Errorf("%s: got incorrect options: expected %v, got %v", testCase.name, expected, actual)
			}
		})
	}
}

func TestOptions_Validate(t *testing.T) {
	var testCases = []struct {
		name     string
		input    Options
		expected string
	}{
		{
			name:     "nothing set errors",
			input:    Options{},
			expected: "required flag --config-dir was unset",
		},
		{
			name: "invalid log level errors",
			input: Options{
				ConfigDir: "/somewhere",
				LogLevel:  "whoa",
			},
			expected: "invalid --log-level: not a valid logrus Level: \"whoa\"",
		},
		{
			name: "valid config has no errors",
			input: Options{
				ConfigDir: "/somewhere",
				LogLevel:  "debug",
			},
			expected: "<nil>",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if actual, expected := testCase.input.Validate(), testCase.expected; expected != fmt.Sprintf("%v", actual) {
				t.Errorf("%s: got incorrect error from validate: expected %v, got %v", testCase.name, expected, actual)
			}
		})
	}
}

func TestOptions_Matches(t *testing.T) {
	var testCases = []struct {
		name     string
		input    Options
		metadata api.Metadata
		expected bool
	}{
		{
			name:  "nothing set matches everything",
			input: Options{},
			metadata: api.Metadata{
				Org:  "org",
				Repo: "repo",
			},
			expected: true,
		},
		{
			name: "org set matches repo under that org",
			input: Options{
				Org: "org",
			},
			metadata: api.Metadata{
				Org:  "org",
				Repo: "repo",
			},
			expected: true,
		},
		{
			name: "org set doesn't match repo under other org",
			input: Options{
				Org: "org",
			},
			metadata: api.Metadata{
				Org:  "arg",
				Repo: "repo",
			},
			expected: false,
		},
		{
			name: "repo set matches repo",
			input: Options{
				Repo: "repo",
			},
			metadata: api.Metadata{
				Org:  "anything",
				Repo: "repo",
			},
			expected: true,
		},
		{
			name: "repo set doesn't match other repo",
			input: Options{
				Repo: "repo",
			},
			metadata: api.Metadata{
				Org:  "anything",
				Repo: "ripo",
			},
			expected: false,
		},
		{
			name: "org and repo set matches org/repo",
			input: Options{
				Org:  "org",
				Repo: "repo",
			},
			metadata: api.Metadata{
				Org:  "org",
				Repo: "repo",
			},
			expected: true,
		},
		{
			name: "org and repo set doesn't match other repo in org",
			input: Options{
				Org:  "org",
				Repo: "repo",
			},
			metadata: api.Metadata{
				Org:  "org",
				Repo: "ripo",
			},
			expected: false,
		},
		{
			name: "org and repo set doesn't match repo in other org",
			input: Options{
				Org:  "org",
				Repo: "repo",
			},
			metadata: api.Metadata{
				Org:  "arg",
				Repo: "repo",
			},
			expected: false,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if actual, expected := testCase.input.matches(testCase.metadata.Org, testCase.metadata.Repo), testCase.expected; actual != expected {
				t.Errorf("%s: got incorrect match: expected %v, got %v", testCase.name, expected, actual)
			}
		})
	}
}

func TestOperateOnCIOperatorConfigDir(t *testing.T) {
	treeDir := "./testdata/tree/config"
	cmDir := "./testdata/cm/config"
	testCases := []struct {
		id, path               string
		options                Options
		expectedProcessedFiles sets.String
	}{
		{
			id:      "no options, expect to process all files",
			path:    treeDir,
			options: Options{},
			expectedProcessedFiles: sets.NewString([]string{
				"foo-bar-master.yaml",
				"foo-bar-release-4.9.yaml",
				"super-duper-master.yaml",
				"super-duper-release-4.9.yaml",
			}...),
		},
		{
			id:      "specify org, expect to process only files that belong to that org",
			path:    treeDir,
			options: Options{Org: "foo"},
			expectedProcessedFiles: sets.NewString([]string{
				"foo-bar-master.yaml",
				"foo-bar-release-4.9.yaml",
			}...),
		},
		{
			id:      "specify org and repo, expect to process only files that belong to that org/repo",
			path:    treeDir,
			options: Options{Org: "foo", Repo: "bar"},
			expectedProcessedFiles: sets.NewString([]string{
				"foo-bar-master.yaml",
				"foo-bar-release-4.9.yaml",
			}...),
		},
		{
			id:   "process only a single modified file",
			path: treeDir,
			options: Options{
				onlyProcessChanges: true,
				modifiedFiles: sets.NewString(
					filepath.Join(treeDir, "foo/bar/foo-bar-master.yaml"),
				),
			},
			expectedProcessedFiles: sets.NewString([]string{
				"foo-bar-master.yaml",
			}...),
		},
		{
			id:   "process only the multiple modified files",
			path: treeDir,
			options: Options{
				onlyProcessChanges: true,
				modifiedFiles: sets.NewString(
					filepath.Join(treeDir, "foo/bar/foo-bar-master.yaml"),
					filepath.Join(treeDir, "super/duper/super-duper-release-4.9.yaml"),
				),
			},
			expectedProcessedFiles: sets.NewString([]string{
				"foo-bar-master.yaml",
				"super-duper-release-4.9.yaml",
			}...),
		},
		{
			id:   "load from a ConfigMap mount",
			path: cmDir,
			expectedProcessedFiles: sets.NewString(
				"foo-bar-master.yaml",
				"foo-bar-release-4.9.yaml",
				"super-duper-master.yaml",
				"super-duper-release-4.9.yaml",
			),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.id, func(t *testing.T) {
			processed := sets.NewString()

			if err := tc.options.OperateOnCIOperatorConfigDir(tc.path, func(configuration *cioperatorapi.ReleaseBuildConfiguration, info *Info) error {
				filename := filepath.Base(info.Filename)
				if !tc.expectedProcessedFiles.Has(filename) {
					t.Errorf("file %s wasn't expected to be processed", filename)
				}
				if processed.Has(filename) {
					t.Errorf("file %s was processed more than once", filename)
				}
				processed.Insert(filename)
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if !processed.Equal(tc.expectedProcessedFiles) {
				t.Errorf("unexpected processed files: %s", cmp.Diff(processed, tc.expectedProcessedFiles))
			}
		})
	}
}

func TestOperateOnJobConfigSubdirPaths(t *testing.T) {
	dir, err := testhelper.TmpDir(t, map[string]fstest.MapFile{
		"a":                              {},
		"b/c.yaml":                       {},
		"d/e/d-e.yaml":                   {},
		"f/g/f-g-master-presubmits.yaml": {},
		"f/h/f-h-master-presubmits.yaml": {},
		"i/k/i-k-master-presubmits.yaml": {},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name     string
		opt      Options
		sub      string
		expected []string
	}{{
		name: "all",
		expected: []string{
			"/f/g/f-g-master-presubmits.yaml",
			"/f/h/f-h-master-presubmits.yaml",
			"/i/k/i-k-master-presubmits.yaml",
		},
	}, {
		name: "subdir",
		sub:  "f",
		expected: []string{
			"/f/g/f-g-master-presubmits.yaml",
			"/f/h/f-h-master-presubmits.yaml",
		},
	}, {
		name: "org",
		opt:  Options{Org: "f"},
		expected: []string{
			"/f/g/f-g-master-presubmits.yaml",
			"/f/h/f-h-master-presubmits.yaml",
		},
	}, {
		name: "repo",
		opt:  Options{Repo: "g"},
		expected: []string{
			"/f/g/f-g-master-presubmits.yaml",
		},
	}, {
		name: "org+repo",
		opt:  Options{Org: "f", Repo: "g"},
		expected: []string{
			"/f/g/f-g-master-presubmits.yaml",
		},
	}} {
		t.Run(tc.name, func(t *testing.T) {
			var ret []string
			if err := tc.opt.OperateOnJobConfigSubdirPaths(dir, tc.sub, func(info *jc.Info) error {
				ret = append(ret, strings.TrimPrefix(info.Filename, dir))
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			testhelper.Diff(t, "directories", ret, tc.expected)
		})
	}
}
