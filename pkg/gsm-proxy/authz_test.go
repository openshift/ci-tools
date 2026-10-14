package gsmproxy

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/openshift/ci-tools/pkg/group"
)

func TestCollectionAuthorizer(t *testing.T) {
	config, err := group.LoadConfig(filepath.Join("testdata", "ownership.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	authorizer := NewCollectionAuthorizer(config)
	testCases := []struct {
		name       string
		groups     []string
		collection string
		wantAllow  bool
	}{
		{name: "owner", groups: []string{"team-a"}, collection: "foo", wantAllow: true},
		{name: "second collection", groups: []string{"team-a"}, collection: "foo-extra", wantAllow: true},
		{name: "second owner of shared collection", groups: []string{"team-b"}, collection: "shared", wantAllow: true},
		{name: "owner among unrelated groups", groups: []string{"unknown", "team-b", "team-a"}, collection: "foo", wantAllow: true},
		{name: "duplicate memberships", groups: []string{"team-a", "team-a"}, collection: "foo", wantAllow: true},
		{name: "claimed ownership still grants access when also listed as unclaimed", groups: []string{"unclaimed-secrets", "team-a"}, collection: "shared", wantAllow: true},
		{name: "another team's collection", groups: []string{"team-a"}, collection: "bar"},
		{name: "no groups", collection: "foo"},
		{name: "empty groups", groups: []string{}, collection: "foo"},
		{name: "unknown group", groups: []string{"unknown"}, collection: "foo"},
		{name: "group without collections", groups: []string{"team-without-collections"}, collection: "foo"},
		{name: "unknown collection", groups: []string{"team-a"}, collection: "unknown"},
		{name: "collection prefix", groups: []string{"team-a"}, collection: "fo"},
		{name: "collection suffix", groups: []string{"team-a"}, collection: "foo-suffix"},
		{name: "collection substring", groups: []string{"team-a"}, collection: "oo"},
		{name: "collection case mismatch", groups: []string{"team-a"}, collection: "Foo"},
		{name: "empty collection", groups: []string{"team-a"}},
		{name: "group prefix", groups: []string{"team"}, collection: "foo"},
		{name: "group suffix", groups: []string{"team-a-suffix"}, collection: "foo"},
		{name: "group case mismatch", groups: []string{"Team-a"}, collection: "foo"},
		{name: "group email is not the Rover group name", groups: []string{"team-a@redhat.com"}, collection: "foo"},
		{name: "Kubernetes rename does not grant ownership", groups: []string{"kubernetes-team-a"}, collection: "foo"},
		{name: "unclaimed owner", groups: []string{"unclaimed-secrets"}, collection: "orphaned"},
		{name: "unclaimed membership alone does not grant shared collection", groups: []string{"unclaimed-secrets"}, collection: "shared"},
		{name: "unrelated claimed group does not grant unclaimed collection", groups: []string{"unclaimed-secrets", "team-a"}, collection: "orphaned"},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := authorizer.Authorize(Identity{Username: "test-user", Groups: tc.groups}, tc.collection)
			if tc.wantAllow {
				if err != nil {
					t.Fatalf("expected collection access, got %v", err)
				}
				return
			}
			if !errors.Is(err, ErrForbidden) {
				t.Fatalf("error = %v, want ErrForbidden", err)
			}
		})
	}
}

func TestCollectionAuthorizerWithoutConfig(t *testing.T) {
	for _, config := range []*group.Config{nil, {}} {
		authorizer := NewCollectionAuthorizer(config)
		err := authorizer.Authorize(Identity{Username: "test-user", Groups: []string{"team-a"}}, "foo")
		if !errors.Is(err, ErrForbidden) {
			t.Fatalf("error = %v, want ErrForbidden for missing ownership", err)
		}
	}
}
