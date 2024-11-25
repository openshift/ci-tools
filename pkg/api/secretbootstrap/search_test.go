package secretbootstrap

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestFindSecret(t *testing.T) {
	for _, tc := range []struct {
		name             string
		secrets          []SecretConfig
		filters          []secretConfigFilter
		wantSecretConfig SecretConfig
		wantIdx          int
	}{
		{
			name:    "Filter by namespaced name",
			filters: []secretConfigFilter{ByNamespacedName("ci", "config-updater")},
			secrets: []SecretConfig{
				{
					From: map[string]ItemContext{
						"sa.config-updater.build01.config": {
							Field: "sa.config-updater.build01.config",
							Item:  "config-updater",
						},
					},
					To: []SecretContext{{Cluster: "app.ci", Namespace: "ci", Name: "config-updater"}},
				},
				{
					From: map[string]ItemContext{
						"sa.deck.app.ci.config": {
							Field: "sa.deck.app.ci.config",
							Item:  "config-updater",
						},
					},
					To: []SecretContext{{Cluster: "app.ci", Namespace: "ci", Name: "deck"}},
				},
			},
			wantSecretConfig: SecretConfig{
				From: map[string]ItemContext{
					"sa.config-updater.build01.config": {
						Field: "sa.config-updater.build01.config",
						Item:  "config-updater",
					},
				},
				To: []SecretContext{{Cluster: "app.ci", Namespace: "ci", Name: "config-updater"}},
			},
			wantIdx: 0,
		},
		{
			name:    "Filter by name only",
			filters: []secretConfigFilter{ByNamespacedName("", "deck")},
			secrets: []SecretConfig{
				{
					From: map[string]ItemContext{
						"sa.config-updater.build01.config": {
							Field: "sa.config-updater.build01.config",
							Item:  "config-updater",
						},
					},
					To: []SecretContext{{Cluster: "app.ci", Namespace: "ci", Name: "config-updater"}},
				},
				{
					From: map[string]ItemContext{
						"sa.deck.app.ci.config": {
							Field: "sa.deck.app.ci.config",
							Item:  "config-updater",
						},
					},
					To: []SecretContext{{Cluster: "app.ci", Namespace: "ci", Name: "deck"}},
				},
			},
			wantSecretConfig: SecretConfig{
				From: map[string]ItemContext{
					"sa.deck.app.ci.config": {
						Field: "sa.deck.app.ci.config",
						Item:  "config-updater",
					},
				},
				To: []SecretContext{{Cluster: "app.ci", Namespace: "ci", Name: "deck"}},
			},
			wantIdx: 1,
		},
	} {
		t.Run(tc.name, func(tt *testing.T) {
			tt.Parallel()
			sc, i := FindSecret(tc.secrets, tc.filters...)
			if tc.wantIdx != i {
				tt.Errorf("want index %d but got %d", tc.wantIdx, i)
			}
			if diff := cmp.Diff(&tc.wantSecretConfig, sc); diff != "" {
				tt.Errorf("unexpected diff:\n%s", diff)
			}
		})
	}
}
