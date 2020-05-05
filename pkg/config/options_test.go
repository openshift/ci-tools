package config

import (
	"errors"
	"flag"
	"github.com/openshift/ci-tools/pkg/api"
	"reflect"
	"testing"
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
		expected error
	}{
		{
			name:     "nothing set errors",
			input:    Options{},
			expected: errors.New("required flag --config-dir was unset"),
		},
		{
			name: "invalid log level errors",
			input: Options{
				ConfigDir: "/somewhere",
				LogLevel:  "whoa",
			},
			expected: errors.New("invalid --log-level: not a valid logrus Level: \"whoa\""),
		},
		{
			name: "valid config has no errors",
			input: Options{
				ConfigDir: "/somewhere",
				LogLevel:  "debug",
			},
			expected: nil,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if actual, expected := testCase.input.Validate(), testCase.expected; !reflect.DeepEqual(actual, expected) {
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
