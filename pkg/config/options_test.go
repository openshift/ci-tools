package config

import (
	"flag"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/google/go-cmp/cmp"

	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/openshift/ci-tools/pkg/api"
	cioperatorapi "github.com/openshift/ci-tools/pkg/api"
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
			if actual, expected := testCase.input.matches(testCase.metadata), testCase.expected; actual != expected {
				t.Errorf("%s: got incorrect match: expected %v, got %v", testCase.name, expected, actual)
			}
		})
	}
}

func TestOperateOnCIOperatorConfigDir(t *testing.T) {
	testConfigDir := "./testdata/config"

	testCases := []struct {
		id                     string
		options                Options
		expectedProcessedFiles sets.String
	}{
		{
			id:      "no options, expect to process all files",
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
			options: Options{Org: "foo"},
			expectedProcessedFiles: sets.NewString([]string{
				"foo-bar-master.yaml",
				"foo-bar-release-4.9.yaml",
			}...),
		},
		{
			id:      "specify org and repo, expect to process only files that belong to that org/repo",
			options: Options{Org: "foo", Repo: "bar"},
			expectedProcessedFiles: sets.NewString([]string{
				"foo-bar-master.yaml",
				"foo-bar-release-4.9.yaml",
			}...),
		},
		{
			id: "process only a single modified file",
			options: Options{
				onlyProcessChanges: true,
				modifiedFiles:      sets.NewString([]string{"testdata/config/foo/bar/foo-bar-master.yaml"}...),
			},
			expectedProcessedFiles: sets.NewString([]string{
				"foo-bar-master.yaml",
			}...),
		},
		{
			id: "process only the multiple modified files",
			options: Options{
				onlyProcessChanges: true,
				modifiedFiles:      sets.NewString([]string{"testdata/config/foo/bar/foo-bar-master.yaml", "testdata/config/super/duper/super-duper-release-4.9.yaml"}...),
			},
			expectedProcessedFiles: sets.NewString([]string{
				"foo-bar-master.yaml",
				"super-duper-release-4.9.yaml",
			}...),
		},
	}

	for _, tc := range testCases {
		var errs []error
		processed := sets.NewString()

		if err := tc.options.OperateOnCIOperatorConfigDir(testConfigDir, func(configuration *cioperatorapi.ReleaseBuildConfiguration, info *Info) error {
			filename := filepath.Base(info.Filename)
			if !tc.expectedProcessedFiles.Has(filename) {
				errs = append(errs, fmt.Errorf("file %s wasn't expected to be processed", filename))
			}
			processed.Insert(filename)
			return nil
		}); err != nil {
			t.Fatal(err)
		}

		if len(errs) > 0 {
			t.Fatal("unexpected errors: %w", errs)
		}

		if diff := cmp.Diff(processed, tc.expectedProcessedFiles); diff != "" {
			t.Fatal(diff)
		}
	}
}
