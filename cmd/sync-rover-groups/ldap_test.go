package main

import (
	"fmt"
	"testing"

	ldapv3 "github.com/go-ldap/ldap/v3"
	"github.com/google/go-cmp/cmp"

	"github.com/openshift/ci-tools/pkg/testhelper"
)

type fakeLDAPConn struct {
	searchResult *ldapv3.SearchResult
	err          error
}

func (conn *fakeLDAPConn) Search(searchRequest *ldapv3.SearchRequest) (*ldapv3.SearchResult, error) {
	if conn.err != nil {
		return nil, conn.err
	}
	return conn.searchResult, nil
}

func TestResolve(t *testing.T) {

	testCases := []struct {
		name        string
		r           *ldapGroupResolver
		group       string
		expected    *Group
		expectedErr error
	}{
		{
			name: "base case",
			r: &ldapGroupResolver{&fakeLDAPConn{
				searchResult: &ldapv3.SearchResult{
					Entries: []*ldapv3.Entry{
						{
							DN: "cn=test-platform-ci-admins,ou=adhoc,ou=managedGroups,dc=redhat,dc=com",
							Attributes: []*ldapv3.EntryAttribute{
								{
									Name: "uniqueMember",
									Values: []string{
										"uid=tom,ou=users,dc=redhat,dc=com",
										"uid=jerry,ou=users,dc=redhat,dc=com",
									},
								},
							},
						},
					},
				},
				err: nil,
			},
			},
			group: "some-group",
			expected: &Group{
				Name:    "some-group",
				Members: []string{"jerry", "tom"},
			},
		},
		{
			name:        "nil conn",
			r:           &ldapGroupResolver{},
			expectedErr: fmt.Errorf("ldapGroupResolver's connection is nil"),
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual, actualErr := tc.r.resolve(tc.group)
			if diff := cmp.Diff(tc.expectedErr, actualErr, testhelper.EquateErrorMessage); diff != "" {
				t.Errorf("%s differs from expected:\n%s", tc.name, diff)
			}
			if actualErr == nil {
				if diff := cmp.Diff(tc.expected, actual, testhelper.RuntimeObjectIgnoreRvTypeMeta); diff != "" {
					t.Errorf("%s differs from expected:\n%s", tc.name, diff)
				}
			}
		})
	}
}
