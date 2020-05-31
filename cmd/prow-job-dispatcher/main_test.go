package main

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/google/go-cmp/cmp"
	"k8s.io/apimachinery/pkg/util/sets"
)

func TestValidate(t *testing.T) {
	testCases := []struct {
		name     string
		given    *options
		expected error
	}{
		{
			name: "prometheus username is set while password not",
			given: &options{
				prometheusUsername: "user",
			},
			expected: fmt.Errorf("--prometheus-username and --prometheus-password-path must be specified together"),
		},
		{
			name: "prometheus password path is set while username not",
			given: &options{
				prometheusPasswordPath: "some-path",
			},
			expected: fmt.Errorf("--prometheus-username and --prometheus-password-path must be specified together"),
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual := tc.given.validate()
			equalError(t, tc.expected, actual)
		})
	}
}

func TestComplete(t *testing.T) {
	dir, err := ioutil.TempDir("", "test")
	if err != nil {
		t.Error("Failed to create the temp dir")
	}
	defer func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Errorf("Failed to remove the temp dir: %s", dir)
		}
	}()
	passwordPath := filepath.Join(dir, "secret.txt")
	if err := ioutil.WriteFile(passwordPath, []byte("some-pass"), 0644); err != nil {
		t.Errorf("Failed to password to the file: %s", passwordPath)
	}
	emptyPasswordPath := filepath.Join(dir, "empty-secret.txt")
	if err := ioutil.WriteFile(emptyPasswordPath, []byte{}, 0644); err != nil {
		t.Errorf("Failed to password to the file: %s", emptyPasswordPath)
	}

	testCases := []struct {
		name            string
		secrets         sets.String
		given           *options
		expected        error
		expectedSecrets sets.String
	}{
		{
			name: "password path is set",
			given: &options{
				prometheusPasswordPath: passwordPath,
			},
			expectedSecrets: sets.NewString("some-pass"),
		},
		{
			name: "password path is set but file does not exist",
			given: &options{
				prometheusPasswordPath: "not-exist",
			},
			expected:        fmt.Errorf("open not-exist: no such file or directory"),
			expectedSecrets: sets.NewString(),
		},
		{
			name: "empty password",
			given: &options{
				prometheusPasswordPath: emptyPasswordPath,
			},
			expected:        fmt.Errorf("no content in file: %s", emptyPasswordPath),
			expectedSecrets: sets.NewString(),
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			secrets := sets.NewString()
			actual := tc.given.complete(&secrets)
			equalError(t, tc.expected, actual)
			if !reflect.DeepEqual(tc.expectedSecrets, secrets) {
				t.Errorf("%s: actual differs from expected:\n%s", t.Name(), cmp.Diff(tc.expectedSecrets, secrets))
			}
		})
	}
}

func equalError(t *testing.T, expected, actual error) {
	if (expected == nil) != (actual == nil) {
		t.Errorf("%s: expecting error \"%v\", got \"%v\"", t.Name(), expected, actual)
	}
	if expected != nil && actual != nil && expected.Error() != actual.Error() {
		t.Errorf("%s: expecting error msg %q, got %q", t.Name(), expected.Error(), actual.Error())
	}
}
