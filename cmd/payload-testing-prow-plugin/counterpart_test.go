package main

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/openshift/ci-tools/pkg/api"
	cioperatorconfig "github.com/openshift/ci-tools/pkg/config"
)

func TestPublicCounterpart(t *testing.T) {
	for _, tc := range []struct {
		name, repo, org, publicRepo, errorText string
		configs                                cioperatorconfig.ByOrgRepo
	}{
		{name: "openshift mirror", repo: "component", org: "openshift", publicRepo: "component", configs: cioperatorconfig.ByOrgRepo{"openshift": {"component": nil}}},
		{name: "other flattened organization", repo: "component", org: "operator-framework", publicRepo: "component", configs: cioperatorconfig.ByOrgRepo{"operator-framework": {"component": nil}}},
		{name: "prefixed organization", repo: "example-component", org: "example", publicRepo: "component", configs: cioperatorconfig.ByOrgRepo{"example": {"component": nil}}},
		{name: "missing", repo: "component", configs: cioperatorconfig.ByOrgRepo{"openshift-priv": {"component": nil}}, errorText: "found 0"},
		{name: "ambiguous", repo: "component", configs: cioperatorconfig.ByOrgRepo{"openshift": {"component": nil}, "operator-framework": {"component": nil}}, errorText: "found 2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			metadata := api.Metadata{Org: "openshift-priv", Repo: tc.repo, Branch: "release-4.99", Variant: "variant"}
			actual, err := publicCounterpart(metadata, tc.configs)
			if tc.errorText != "" {
				if err == nil || !strings.Contains(err.Error(), tc.errorText) {
					t.Fatalf("error = %v, want %q", err, tc.errorText)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			metadata.Org, metadata.Repo = tc.org, tc.publicRepo
			if diff := cmp.Diff(metadata, actual); diff != "" {
				t.Errorf("metadata differs (-want +got):\n%s", diff)
			}
		})
	}
}
