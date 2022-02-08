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

func TestGetGitHubID(t *testing.T) {

	testCases := []struct {
		name     string
		value    string
		expected string
	}{
		{
			name:     "base case",
			value:    "Github->https://github.com/tom",
			expected: "tom",
		},
		{
			name:     "slash suffix",
			value:    "Github->https://github.com/tom/",
			expected: "tom",
		},
		{
			name:  "not github",
			value: "Twitter->https://twitter.com/tom",
		},
		{
			name:  "gitlab as github",
			value: "Github->https://gitlab.consulting.redhat.com/tom",
		},
		{
			name:     "www is ignored",
			value:    "Github->https://www.github.com/tom/",
			expected: "tom",
		},
		{
			name:     "capital letters are ignored",
			value:    "Github->https://GitHub.com/tom",
			expected: "tom",
		},
		{
			name:     "http is good",
			value:    "Github->http://github.com/tom/",
			expected: "tom",
		},
		{
			name:     "no protolcol at all",
			value:    "Github->github.com/tom/",
			expected: "tom",
		},
		{
			name:     "git.io",
			value:    "Github->https://git.io/tom",
			expected: "tom",
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual := getGitHubID(tc.value)
			if diff := cmp.Diff(tc.expected, actual, testhelper.EquateErrorMessage); diff != "" {
				t.Errorf("%s differs from expected:\n%s", tc.name, diff)
			}
		})
	}
}
