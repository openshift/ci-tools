package lease

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/openshift/ci-tools/pkg/testhelper"
)

func TestLoadLeaseCredentials(t *testing.T) {
	dir, err := os.MkdirTemp("", "test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	leaseServerCredentialsFile := filepath.Join(dir, "leaseServerCredentialsFile")
	if err := os.WriteFile(leaseServerCredentialsFile, []byte("ci-new:secret-new"), 0644); err != nil {
		t.Fatal(err)
	}

	leaseServerCredentialsInvalidFile := filepath.Join(dir, "leaseServerCredentialsInvalidFile")
	if err := os.WriteFile(leaseServerCredentialsInvalidFile, []byte("no-colon"), 0644); err != nil {
		t.Fatal(err)
	}

	testCases := []struct {
		name                       string
		leaseServerCredentialsFile string
		expectedUsername           string
		passwordGetterVerify       func(func() []byte) error
		expectedErr                error
	}{
		{
			name:                       "valid credential file",
			leaseServerCredentialsFile: leaseServerCredentialsFile,
			expectedUsername:           "ci-new",
			passwordGetterVerify: func(passwordGetter func() []byte) error {
				p := string(passwordGetter())
				if diff := cmp.Diff("secret-new", p); diff != "" {
					return fmt.Errorf("actual does not match expected, diff: %s", diff)
				}
				return nil
			},
		},
		{
			name:                       "wrong credential file",
			leaseServerCredentialsFile: leaseServerCredentialsInvalidFile,
			expectedErr:                fmt.Errorf("got invalid content of lease server credentials file which must be of the form '<username>:<passwrod>'"),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			opts := Options{leaseServerCredentialsFile: tc.leaseServerCredentialsFile}
			username, passwordGetter, err := opts.loadLeaseCredentials()
			if diff := cmp.Diff(tc.expectedUsername, username); diff != "" {
				t.Errorf("actual does not match expected, diff: %s", diff)
			}
			if diff := cmp.Diff(tc.expectedErr, err, testhelper.EquateErrorMessage); diff != "" {
				t.Errorf("actualError does not match expectedError, diff: %s", diff)
			}
			if tc.passwordGetterVerify != nil {
				if err := tc.passwordGetterVerify(passwordGetter); err != nil {
					t.Errorf("unexpcected error: %v", err)
				}
			}
		})
	}
}
