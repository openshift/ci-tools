package config

import (
	"errors"
	"reflect"
	"testing"
)

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
		info     *Info
		expected bool
	}{
		{
			name:  "nothing set matches everything",
			input: Options{},
			info: &Info{
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
			info: &Info{
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
			info: &Info{
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
			info: &Info{
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
			info: &Info{
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
			info: &Info{
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
			info: &Info{
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
			info: &Info{
				Org:  "arg",
				Repo: "repo",
			},
			expected: false,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if actual, expected := testCase.input.matches(testCase.info), testCase.expected; actual != expected {
				t.Errorf("%s: got incorrect match: expected %v, got %v", testCase.name, expected, actual)
			}
		})
	}
}
