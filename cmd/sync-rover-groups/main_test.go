package main

import (
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"
	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/openshift/ci-tools/pkg/testhelper"
)

func TestOptionsValidateLDAPBind(t *testing.T) {
	base := func() *options {
		return &options{
			logLevelRaw:  "info",
			manifestDirs: sets.New("clusters"),
		}
	}

	testCases := []struct {
		name        string
		opts        *options
		envDN       string
		envPW       string
		expectedErr error
	}{
		{
			name:        "ldap sync requires bind",
			opts:        base(),
			expectedErr: fmt.Errorf("LDAP bind credentials are required (set --ldap-bind-dn and --ldap-bind-password-file or LDAP_BIND_DN and LDAP_BIND_PASSWORD)"),
		},
		{
			name: "validate-subjects skips ldap",
			opts: func() *options {
				o := base()
				o.validateSubjects = true
				return o
			}(),
		},
		{
			name: "print-config skips ldap",
			opts: func() *options {
				o := base()
				o.printConfig = true
				return o
			}(),
		},
		{
			name:  "bind from env",
			opts:  base(),
			envDN: "uid=svc,dc=redhat,dc=com",
			envPW: "secret",
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("LDAP_BIND_DN", tc.envDN)
			t.Setenv("LDAP_BIND_PASSWORD", tc.envPW)
			err := tc.opts.validate()
			if diff := cmp.Diff(tc.expectedErr, err, testhelper.EquateErrorMessage); diff != "" {
				t.Errorf("%s differs from expected:\n%s", tc.name, diff)
			}
		})
	}
}
