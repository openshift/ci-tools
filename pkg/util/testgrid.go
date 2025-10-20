package util

import "strings"

func IsSpecialInformingJobOnTestGrid(jobName string) bool {
	// Each entry is a list of substrings that ALL must be present in the job name
	// If only one substring is provided, only that substring needs to match (as a prefix for the first element)
	testGridInformingPrefixes := [][]string{
		{"periodic-ci-ComplianceAsCode-"},
		{"periodic-ci-openshift-cloud-credential-operator-"},
		{"periodic-ci-openshift-cluster-control-plane-machine-set-operator-"},
		{"periodic-ci-openshift-cluster-etcd-operator-"},
		{"periodic-ci-openshift-ovn-kubernetes-release-"},
		{"periodic-ci-openshift-hypershift-main-periodics-"},
		{"periodic-ci-openshift-multiarch"},
		{"periodic-ci-openshift-release-master-ci-"},
		{"periodic-ci-openshift-release-master-nightly-"},
		{"periodic-ci-openshift-release-master-okd-"},
		{"periodic-ci-shiftstack-ci-release-"},
		{"promote-release-openshift-"},
		{"release-openshift-"},
		{"periodic-ci-stolostron-policy-collection-"},
		{"periodic-ci-openshift-eng-ocp-qe-perfscale-"},
		// For operator-framework jobs, require "default" or "stable" in the name
		{"periodic-ci-openshift-operator-framework-olm-release-", "default"},
		{"periodic-ci-openshift-operator-framework-olm-release-", "stable"},
		{"periodic-ci-openshift-operator-framework-operator-controller-", "default"},
		{"periodic-ci-openshift-operator-framework-operator-controller-", "stable"},
	}

	for _, pattern := range testGridInformingPrefixes {
		if len(pattern) == 0 {
			continue
		}
		// First element must be a prefix match, remaining elements must be substrings
		if !strings.HasPrefix(jobName, pattern[0]) {
			continue
		}
		// Check if all additional required substrings are present
		if matchesAllSubstrings(jobName, pattern[1:]) {
			return true
		}
	}
	return false
}

// matchesAllSubstrings checks if all substrings are present in the job name
func matchesAllSubstrings(jobName string, substrings []string) bool {
	for _, substring := range substrings {
		if !strings.Contains(jobName, substring) {
			return false
		}
	}
	return true
}
