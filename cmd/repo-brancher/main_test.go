package main

import (
	"flag"
	"reflect"
	"testing"

	"sigs.k8s.io/prow/pkg/flagutil"

	"github.com/openshift/ci-tools/pkg/config"
	"github.com/openshift/ci-tools/pkg/promotion"
)

func TestOptions_Bind(t *testing.T) {
	var testCases = []struct {
		name               string
		input              []string
		expected           options
		expectedFutureOpts []string
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
			name: "everything set",
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
				"--fast-forward",
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
							Confirm: true},
						CurrentRelease:            "one",
						CurrentPromotionNamespace: "promotionns",
					},
					FutureReleases: flagutil.Strings{},
				},
				gitDir:      "/tmp",
				username:    "hi",
				tokenPath:   "somewhere",
				fastForward: true,
			},
			expectedFutureOpts: []string{"two"},
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
			if actual, expected := o, expected; !reflect.DeepEqual(actual, expected) {
				t.Errorf("%s: got incorrect options: expected %v, got %v", testCase.name, expected, actual)
			}
		})
	}
}
