package api

import (
	"fmt"
	"slices"

	aggerrs "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"

	utilregexp "github.com/openshift/ci-tools/pkg/util/regexp"
)

func ClusterProfileFromParams(params Parameters) (*ClusterProfile, error) {
	return GetParamTyped[*ClusterProfile](params, ClusterProfileDetailsParam)
}

type ClusterProfileKonfluxConfig struct {
	ClusterGroups map[string][]string `yaml:"cluster_groups,omitempty" json:"cluster_groups,omitempty"`
}

// +kubebuilder:object:generate=false
type ClusterProfiles struct {
	ClusterProfiles          []ClusterProfile             `yaml:"cluster_profiles,omitempty" json:"cluster_profiles,omitempty"`
	KonfluxConfig            *ClusterProfileKonfluxConfig `yaml:"konflux,omitempty" json:"konflux,omitempty"`
	ClusterProfileSetsConfig *ClusterProfileSetsConfig    `yaml:"cluster_profile_sets_config,omitempty" json:"cluster_profile_sets_config,omitempty"`
}

func (cp *ClusterProfiles) Resolve() error {
	errs := make([]error, 0)

	clusterGroups := make(map[string][]string)
	if cp.KonfluxConfig != nil {
		clusterGroups = cp.KonfluxConfig.ClusterGroups
	}

	for i := range cp.ClusterProfiles {
		profile := &cp.ClusterProfiles[i]

	ownersLoop:
		for j := range profile.Owners {
			owner := &profile.Owners[j]
			if owner.Konflux == nil {
				continue ownersLoop
			}

			allClusters := sets.New(owner.Konflux.Clusters...)

		clusterGroupsLoop:
			for _, clusterGroupName := range owner.Konflux.ClusterGroups {
				clusters, ok := clusterGroups[clusterGroupName]
				if !ok {
					err := fmt.Errorf("profiles[%d].owners[%d] cluster group %s not found", i, j, clusterGroupName)
					errs = append(errs, err)
					continue clusterGroupsLoop
				}
				allClusters.Insert(clusters...)
			}

			if allClusters.Len() > 0 {
				owner.Konflux.ClustersResolved = allClusters.UnsortedList()
				slices.Sort(owner.Konflux.ClustersResolved)
			}
		}
	}

	return aggerrs.NewAggregate(errs)
}

type ClusterProfilesMap map[string]ClusterProfile

type ClusterProfile struct {
	Name            string                 `yaml:"name,omitempty" json:"name,omitempty"`
	Owners          []ClusterProfileOwners `yaml:"owners,omitempty" json:"owners,omitempty"`
	ClusterType     string                 `yaml:"cluster_type,omitempty" json:"cluster_type,omitempty"`
	LeaseType       string                 `yaml:"lease_type,omitempty" json:"lease_type,omitempty"`
	IPPoolLeaseType string                 `yaml:"ip_pool_lease_type,omitempty" json:"ip_pool_lease_type,omitempty"`
	Secret          string                 `yaml:"secret,omitempty" json:"secret,omitempty"`
	ConfigMap       string                 `yaml:"config_map,omitempty" json:"config_map,omitempty"`
	SetMembers      []string               `yaml:"set_members,omitempty" json:"set_members,omitempty"`
}

type ClusterProfileKonfluxOwner struct {
	Tenant           string   `yaml:"tenant,omitempty" json:"tenant,omitempty"`
	Clusters         []string `yaml:"clusters,omitempty" json:"clusters,omitempty"`
	ClusterGroups    []string `yaml:"cluster_groups,omitempty" json:"cluster_groups,omitempty"`
	ClustersResolved []string `yaml:"-" json:"-"`
}

type ClusterProfileOwners struct {
	Org     string                      `yaml:"org,omitempty" json:"org,omitempty"`
	Repos   []string                    `yaml:"repos,omitempty" json:"repos,omitempty"`
	Konflux *ClusterProfileKonfluxOwner `yaml:"konflux,omitempty" json:"konflux,omitempty"`
}
type ClusterClaimOwnersMap map[string]ClusterClaimDetails

// +kubebuilder:object:generate=false
type ClusterProfileSetsConfig struct {
	// TestsExceptions holds a list of tests for which we do not enfoce policy
	// regarding the cluster profile sets usage.
	// This deeply nested type match the following pattern:
	//  "org/repo": "branch": "variant": "test"
	TestsExceptions map[utilregexp.Regexp]map[utilregexp.Regexp]map[utilregexp.Regexp][]utilregexp.Regexp `json:"tests_exceptions,omitempty"`
}

// +kubebuilder:object:generate=false
type ClusterProfileSetDetails struct {
	ClusterProfileSets map[string][]string `json:"cluster_profile_sets,omitempty"`

	// TestsAllowlist holds a list of tests for which we do not enfoce policy
	// regarding the cluster profile sets usage.
	// This deeply nested type match the following pattern:
	//  "org/repo": "branch": "variant": "test"
	TestsAllowlist map[utilregexp.Regexp]map[utilregexp.Regexp]map[utilregexp.Regexp][]utilregexp.Regexp `json:"tests_allowlist,omitempty"`
}

func (cps ClusterProfileSetDetails) FindSetByProfile(profileName string) (string, bool) {
	for cpsName, cpDetails := range cps.ClusterProfileSets {
		if slices.Contains(cpDetails, profileName) {
			return cpsName, true
		}
	}
	return "", false
}

func (cps ClusterProfileSetDetails) IsTestAllowlisted(test string, metadata Metadata) bool {
	if cps.TestsAllowlist == nil {
		return false
	}

	orgRepo, ok := utilregexp.LookupByMatch(cps.TestsAllowlist, metadata.Org+"/"+metadata.Repo)
	if !ok {
		return false
	}

	branch, ok := utilregexp.LookupByMatch(orgRepo, metadata.Branch)
	if !ok {
		return false
	}

	tests, ok := utilregexp.LookupByMatch(branch, metadata.Variant)
	if !ok {
		return false
	}

	for _, t := range tests {
		if t.Pattern.MatchString(test) {
			return true
		}
	}

	return false
}
