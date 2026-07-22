package main

import (
	"reflect"
	"testing"

	"github.com/google/go-cmp/cmp"

	"k8s.io/apimachinery/pkg/util/diff"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/utils/ptr"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/config"
	"github.com/openshift/ci-tools/pkg/privateorg"
)

func TestPrivateReleaseTagConfiguration(t *testing.T) {
	testCases := []struct {
		id               string
		tagSpecification *api.ReleaseTagConfiguration
		expected         *api.ReleaseTagConfiguration
	}{
		{
			id: "no changes expected",
			tagSpecification: &api.ReleaseTagConfiguration{
				Name:      "origin-v4",
				Namespace: "openshift",
			},
			expected: &api.ReleaseTagConfiguration{
				Name:      "origin-v4",
				Namespace: "openshift",
			},
		},
		{
			id: "changes expected",
			tagSpecification: &api.ReleaseTagConfiguration{
				Name:      "origin-v4",
				Namespace: "ocp",
			},
			expected: &api.ReleaseTagConfiguration{
				Name:      "origin-v4",
				Namespace: "ocp-priv",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.id, func(t *testing.T) {
			privateReleaseTagConfiguration(tc.tagSpecification)
			if !reflect.DeepEqual(tc.tagSpecification, tc.expected) {
				t.Fatalf("Differences found: %v", diff.ObjectReflectDiff(tc.tagSpecification, tc.expected))
			}
		})
	}
}

func TestPrivateIntegrationRelease(t *testing.T) {
	testCases := []struct {
		id       string
		release  *api.Integration
		expected *api.Integration
	}{
		{
			id: "no changes expected",
			release: &api.Integration{
				Name:      "origin-v4",
				Namespace: "openshift",
			},
			expected: &api.Integration{
				Name:      "origin-v4",
				Namespace: "openshift",
			},
		},
		{
			id: "changes expected",
			release: &api.Integration{
				Name:      "origin-v4",
				Namespace: "ocp",
			},
			expected: &api.Integration{
				Name:      "origin-v4",
				Namespace: "ocp-priv",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.id, func(t *testing.T) {
			privateIntegrationRelease(tc.release)
			if !reflect.DeepEqual(tc.release, tc.expected) {
				t.Fatalf("Differences found: %v", diff.ObjectReflectDiff(tc.release, tc.expected))
			}
		})
	}
}

func TestPrivateBuildRoot(t *testing.T) {
	testCases := []struct {
		id        string
		buildRoot *api.BuildRootImageConfiguration
		expected  *api.BuildRootImageConfiguration
	}{
		{
			id: "no changes expected",
			buildRoot: &api.BuildRootImageConfiguration{
				ImageStreamTagReference: &api.ImageStreamTagReference{
					Name:      "origin-v4",
					Namespace: "openshift",
				},
			},
			expected: &api.BuildRootImageConfiguration{
				ImageStreamTagReference: &api.ImageStreamTagReference{
					Name:      "origin-v4",
					Namespace: "openshift",
				},
			},
		},
		{
			id: "changes expected",
			buildRoot: &api.BuildRootImageConfiguration{
				ImageStreamTagReference: &api.ImageStreamTagReference{
					Name:      "origin-v4",
					Namespace: "ocp",
				},
			},
			expected: &api.BuildRootImageConfiguration{
				ImageStreamTagReference: &api.ImageStreamTagReference{
					Name:      "origin-v4",
					Namespace: "ocp-priv",
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.id, func(t *testing.T) {
			privateBuildRoot(tc.buildRoot)
			if !reflect.DeepEqual(tc.buildRoot, tc.expected) {
				t.Fatalf("Differences found: %v", diff.ObjectReflectDiff(tc.buildRoot, tc.expected))
			}
		})
	}
}

func TestPrivateBaseImages(t *testing.T) {
	testCases := []struct {
		id         string
		baseImages map[string]api.ImageStreamTagReference
		expected   map[string]api.ImageStreamTagReference
	}{
		{
			id: "no changes",
			baseImages: map[string]api.ImageStreamTagReference{
				"base": {Name: "origin-v4", Namespace: "openshift"},
				"os":   {Name: "centos", Namespace: "openshift"},
			},
			expected: map[string]api.ImageStreamTagReference{
				"base": {Name: "origin-v4", Namespace: "openshift"},
				"os":   {Name: "centos", Namespace: "openshift"},
			},
		},

		{
			id: "partly changes",
			baseImages: map[string]api.ImageStreamTagReference{
				"base": {Name: "origin-v4", Namespace: "openshift"},
				"os":   {Name: "centos", Namespace: "ocp"},
				"test": {Name: "4.3", Namespace: "ocp"},
			},
			expected: map[string]api.ImageStreamTagReference{
				"base": {Name: "origin-v4", Namespace: "openshift"},
				"os":   {Name: "centos", Namespace: "ocp"},
				"test": {Name: "4.3", Namespace: "ocp-priv"},
			},
		},

		{
			id: "massive changes",
			baseImages: map[string]api.ImageStreamTagReference{
				"base": {Name: "4.2", Namespace: "ocp"},
				"os":   {Name: "4.3", Namespace: "ocp"},
			},
			expected: map[string]api.ImageStreamTagReference{
				"base": {Name: "4.2", Namespace: "ocp-priv"},
				"os":   {Name: "4.3", Namespace: "ocp-priv"},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.id, func(t *testing.T) {
			privateBaseImages(tc.baseImages)
			if !reflect.DeepEqual(tc.baseImages, tc.expected) {
				t.Fatalf("Differences found: %v", diff.ObjectReflectDiff(tc.baseImages, tc.expected))
			}
		})
	}
}

func TestPrivatePromotionConfiguration(t *testing.T) {
	testCases := []struct {
		id        string
		promotion *api.PromotionConfiguration
		expected  *api.PromotionConfiguration
	}{
		{
			id:        "promoted by name",
			promotion: &api.PromotionConfiguration{Targets: []api.PromotionTarget{{Name: "4.x", Namespace: "ocp"}}},
			expected:  &api.PromotionConfiguration{Targets: []api.PromotionTarget{{Name: "4.x", Namespace: "ocp-priv"}}},
		},
		{
			id:        "promoted by tag",
			promotion: &api.PromotionConfiguration{Targets: []api.PromotionTarget{{Tag: "4.x", Namespace: "ocp"}}},
			expected:  &api.PromotionConfiguration{Targets: []api.PromotionTarget{{Tag: "4.x", Namespace: "ocp-priv"}}},
		},
		{
			id:        "promoted by tag, includes tag_by_commit",
			promotion: &api.PromotionConfiguration{Targets: []api.PromotionTarget{{Tag: "4.x", Namespace: "ocp", TagByCommit: true}}},
			expected:  &api.PromotionConfiguration{Targets: []api.PromotionTarget{{Tag: "4.x", Namespace: "ocp-priv"}}},
		},
		{
			id: "disable non ocp targets",
			promotion: &api.PromotionConfiguration{Targets: []api.PromotionTarget{
				{Tag: "4.x", Namespace: "ocp"},
				{Tag: "4.x", Namespace: "hypershift"},
			}},
			expected: &api.PromotionConfiguration{Targets: []api.PromotionTarget{
				{Tag: "4.x", Namespace: "ocp-priv"},
				{Tag: "4.x", Namespace: "hypershift", Disabled: true},
			}},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.id, func(t *testing.T) {
			privatePromotionConfiguration(tc.promotion)
			if !reflect.DeepEqual(tc.promotion, tc.expected) {
				t.Fatalf("Differences found: %v", diff.ObjectReflectDiff(tc.promotion, tc.expected))
			}
		})
	}
}

func TestCIOperatorConfigsCallback(t *testing.T) {
	for _, tc := range []struct {
		name              string
		opts              options
		rbc               *api.ReleaseBuildConfiguration
		repoInfo          *config.Info
		wantConfigsByRepo configsByRepo
	}{
		{
			name: "Mirror a config",
			opts: options{
				toOrg:   "openshift-priv",
				onlyOrg: "openshift",
				WhitelistOptions: config.WhitelistOptions{
					WhitelistConfig: config.WhitelistConfig{
						Whitelist: map[string][]string{
							"openshift": {"kubernetes"},
						},
					},
				},
			},
			rbc: &api.ReleaseBuildConfiguration{
				Tests: []api.TestStepConfiguration{{
					As: "e2e",
				}},
			},
			repoInfo: &config.Info{
				Metadata: api.Metadata{
					Org:  "openshift",
					Repo: "kubernetes",
				},
			},
			wantConfigsByRepo: configsByRepo{
				"kubernetes": []config.DataWithInfo{{
					Configuration: api.ReleaseBuildConfiguration{
						Metadata:              api.Metadata{Org: "openshift-priv", Repo: "kubernetes"},
						CanonicalGoRepository: ptr.To("github.com/openshift/kubernetes"),
						Prowgen:               &api.ProwgenOverrides{Private: true},
						Tests:                 []api.TestStepConfiguration{{As: "e2e"}},
					},
					Info: config.Info{Metadata: api.Metadata{Org: "openshift-priv", Repo: "kubernetes"}},
				}},
			},
		},
		{
			name: "Do not mirror config without tests and images",
			opts: options{
				toOrg:   "openshift-priv",
				onlyOrg: "openshift",
				WhitelistOptions: config.WhitelistOptions{
					WhitelistConfig: config.WhitelistConfig{
						Whitelist: map[string][]string{
							"openshift": {"kubernetes"},
						},
					},
				},
			},
			rbc: &api.ReleaseBuildConfiguration{},
			repoInfo: &config.Info{
				Metadata: api.Metadata{
					Org:  "openshift",
					Repo: "kubernetes",
				},
			},
			wantConfigsByRepo: configsByRepo{},
		},
		{
			name: "Mirror a non-default org with prefixed repo name in openshift-priv",
			opts: options{
				toOrg: "openshift-priv",
				WhitelistOptions: config.WhitelistOptions{
					WhitelistConfig: config.WhitelistConfig{
						Whitelist: map[string][]string{
							"migtools": {"filebrowser"},
						},
					},
				},
			},
			rbc: &api.ReleaseBuildConfiguration{
				Tests: []api.TestStepConfiguration{{
					As: "e2e",
				}},
			},
			repoInfo: &config.Info{
				Metadata: api.Metadata{
					Org:  "migtools",
					Repo: "filebrowser",
				},
			},
			wantConfigsByRepo: configsByRepo{
				"migtools-filebrowser": []config.DataWithInfo{{
					Configuration: api.ReleaseBuildConfiguration{
						Metadata:              api.Metadata{Org: "openshift-priv", Repo: "migtools-filebrowser"},
						CanonicalGoRepository: ptr.To("github.com/migtools/filebrowser"),
						Prowgen:               &api.ProwgenOverrides{Private: true},
						Tests:                 []api.TestStepConfiguration{{As: "e2e"}},
					},
					Info: config.Info{Metadata: api.Metadata{Org: "openshift-priv", Repo: "migtools-filebrowser"}},
				}},
			},
		},
		{
			name: "Mirror a non-default org with prefixed repo name in non-openshift-priv org",
			opts: options{
				toOrg: "some-org",
				WhitelistOptions: config.WhitelistOptions{
					WhitelistConfig: config.WhitelistConfig{
						Whitelist: map[string][]string{
							"migtools": {"filebrowser"},
						},
					},
				},
			},
			rbc: &api.ReleaseBuildConfiguration{
				Tests: []api.TestStepConfiguration{{
					As: "e2e",
				}},
			},
			repoInfo: &config.Info{
				Metadata: api.Metadata{
					Org:  "migtools",
					Repo: "filebrowser",
				},
			},
			wantConfigsByRepo: configsByRepo{
				"migtools-filebrowser": []config.DataWithInfo{{
					Configuration: api.ReleaseBuildConfiguration{
						Metadata:              api.Metadata{Org: "some-org", Repo: "migtools-filebrowser"},
						CanonicalGoRepository: ptr.To("github.com/migtools/filebrowser"),
						Tests:                 []api.TestStepConfiguration{{As: "e2e"}},
					},
					Info: config.Info{Metadata: api.Metadata{Org: "some-org", Repo: "migtools-filebrowser"}},
				}},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gotConfigsByRepo := make(configsByRepo)
			flattenedOrgs := sets.New[string](privateorg.DefaultFlattenOrgs...)
			flattenedOrgs.Insert(tc.opts.flattenOrgs...)
			if tc.opts.onlyOrg != "" {
				flattenedOrgs.Insert(tc.opts.onlyOrg)
			}
			callback := ciOperatorConfigsCallback(tc.opts, gotConfigsByRepo, flattenedOrgs)

			if err := callback(tc.rbc, tc.repoInfo); err != nil {
				t.Fatalf("callback error: %s", err)
			}

			if diff := cmp.Diff(tc.wantConfigsByRepo, gotConfigsByRepo); diff != "" {
				t.Errorf("unexpected configs: %s", diff)
			}
		})
	}
}
