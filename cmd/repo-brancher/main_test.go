package main

import (
	"errors"
	"flag"
	"fmt"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/sirupsen/logrus"
	"sigs.k8s.io/prow/pkg/flagutil"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/config"
	"github.com/openshift/ci-tools/pkg/promotion"
)

func TestOptions_Bind(t *testing.T) {
	var testCases = []struct {
		name               string
		input              []string
		expected           options
		expectedFutureOpts []string
		expectedIgnore     []string
	}{
		{
			name:  "nothing set has defaults",
			input: []string{},
			expected: options{
				FutureOptions: promotion.FutureOptions{
					Options: promotion.Options{
						ConfirmableOptions: config.ConfirmableOptions{
							Options: config.Options{
								LogLevel: "info",
							},
						},
					},
				},
			},
		},
		{
			name: "everything set including ignore",
			input: []string{
				"--config-dir=foo",
				"--org=bar",
				"--repo=baz",
				"--log-level=debug",
				"--confirm",
				"--current-release=one",
				"--current-promotion-namespace=promotionns",
				"--future-release=two",
				"--git-dir=/tmp",
				"--username=hi",
				"--token-path=somewhere",
				"--ignore=xyz/abc",
				"--ignore=pqr/lmn",
			},
			expected: options{
				FutureOptions: promotion.FutureOptions{
					Options: promotion.Options{
						ConfirmableOptions: config.ConfirmableOptions{
							Options: config.Options{
								ConfigDir: "foo",
								Org:       "bar",
								Repo:      "baz",
								LogLevel:  "debug",
							},
							Confirm: true,
						},
						CurrentRelease:            "one",
						CurrentPromotionNamespace: "promotionns",
					},
					FutureReleases: flagutil.Strings{},
				},
				gitDir:    "/tmp",
				username:  "hi",
				tokenPath: "somewhere",
				ignore:    flagutil.Strings{},
			},
			expectedFutureOpts: []string{"two"},
			expectedIgnore:     []string{"xyz/abc", "pqr/lmn"},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			var o options
			fs := flag.NewFlagSet(testCase.name, flag.PanicOnError)
			o.bind(fs)
			if err := fs.Parse(testCase.input); err != nil {
				t.Fatalf("%s: cannot parse args: %v", testCase.name, err)
			}
			expected := testCase.expected
			// this is not exposed for testing
			for _, opt := range testCase.expectedFutureOpts {
				if err := expected.FutureReleases.Set(opt); err != nil {
					t.Errorf("failed to set future release: %v", err)
				}
			}
			// Set expected ignore values
			for _, ign := range testCase.expectedIgnore {
				if err := expected.ignore.Set(ign); err != nil {
					t.Errorf("failed to set ignore: %v", err)
				}
			}
			if actual, expected := o, expected; !reflect.DeepEqual(actual, expected) {
				t.Errorf("%s: got incorrect options: expected %#v, got %#v", testCase.name, expected, actual)
			}
		})
	}
}

func TestPushBranch(t *testing.T) {
	remote, _ := url.Parse("https://github.com/org/repo")
	logger := logrus.NewEntry(logrus.StandardLogger())

	testCases := []struct {
		name          string
		gitErr        error
		expectedRetry bool
		expectedErr   bool
	}{
		{
			name:          "successful push",
			gitErr:        nil,
			expectedRetry: false,
			expectedErr:   false,
		},
		{
			name:          "too shallow error triggers retry",
			gitErr:        fmt.Errorf("Updates were rejected because the remote contains work that you do"),
			expectedRetry: true,
			expectedErr:   false,
		},
		{
			name:          "other push error is fatal",
			gitErr:        fmt.Errorf("permission denied"),
			expectedRetry: false,
			expectedErr:   true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mockGit := func(_ *logrus.Entry, args ...string) error {
				if args[0] != "push" {
					t.Errorf("expected push command, got %v", args)
				}
				return tc.gitErr
			}
			retry, err := pushBranch(logger, remote, "release-4.18", mockGit)
			if retry != tc.expectedRetry {
				t.Errorf("expected retry=%v, got %v", tc.expectedRetry, retry)
			}
			if (err != nil) != tc.expectedErr {
				t.Errorf("expected error=%v, got %v", tc.expectedErr, err)
			}
		})
	}
}

func TestFetchDeeper(t *testing.T) {
	remote, _ := url.Parse("https://github.com/org/repo")
	logger := logrus.NewEntry(logrus.StandardLogger())
	repoInfo := &config.Info{Metadata: api.Metadata{Branch: "main"}}

	testCases := []struct {
		name        string
		deepenBy    int
		gitErr      error
		expectedErr bool
	}{
		{
			name:     "successful deepen",
			deepenBy: 4,
		},
		{
			name:        "fetch error propagated",
			deepenBy:    8,
			gitErr:      errors.New("network error"),
			expectedErr: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mockGit := func(_ *logrus.Entry, args ...string) error {
				expected := fmt.Sprintf("fetch --deepen %d %s main", tc.deepenBy, remote.String())
				got := strings.Join(args, " ")
				if got != expected {
					t.Errorf("expected command %q, got %q", expected, got)
				}
				return tc.gitErr
			}
			err := fetchDeeper(logger, remote, mockGit, repoInfo, tc.deepenBy)
			if (err != nil) != tc.expectedErr {
				t.Errorf("expected error=%v, got %v", tc.expectedErr, err)
			}
		})
	}
}

func TestFetchUnshallow(t *testing.T) {
	remote, _ := url.Parse("https://github.com/org/repo")
	logger := logrus.NewEntry(logrus.StandardLogger())
	repoInfo := &config.Info{Metadata: api.Metadata{Branch: "main"}}

	testCases := []struct {
		name        string
		gitErr      error
		expectedErr bool
	}{
		{
			name: "successful unshallow",
		},
		{
			name:        "fetch error propagated",
			gitErr:      errors.New("network error"),
			expectedErr: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mockGit := func(_ *logrus.Entry, args ...string) error {
				expected := fmt.Sprintf("fetch --unshallow %s main", remote.String())
				got := strings.Join(args, " ")
				if got != expected {
					t.Errorf("expected command %q, got %q", expected, got)
				}
				return tc.gitErr
			}
			err := fetchUnshallow(logger, remote, mockGit, repoInfo)
			if (err != nil) != tc.expectedErr {
				t.Errorf("expected error=%v, got %v", tc.expectedErr, err)
			}
		})
	}
}
