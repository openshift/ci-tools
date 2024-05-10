package api

import (
	"strconv"
	"strings"
)

// LeasesForTest aggregates all the lease configurations in a test.
// It is assumed that they have been validated and contain only valid and
// unique values.
func LeasesForTest(s *MultiStageTestConfigurationLiteral) (ret []StepLease) {
	if p := s.ClusterProfile; p != "" {
		ret = append(ret, StepLease{
			ResourceType: p.LeaseType(),
			Env:          DefaultLeaseEnv,
			Count:        1,
		})
	}
	for _, step := range append(s.Pre, append(s.Test, s.Post...)...) {
		ret = append(ret, step.Leases...)
	}
	ret = append(ret, s.Leases...)
	return
}

func IPPoolLeaseForTest(s *MultiStageTestConfigurationLiteral, metadata Metadata) (ret StepLease) {
	if p := s.ClusterProfile; p == "aws" { //TODO(sgoeddel): Hardcoded to only work on aws, eventually this will be available as a configuration
		if branchValidForIPPoolLease(metadata.Branch) {
			ret = StepLease{
				ResourceType: p.IPPoolLeaseType(),
				Env:          DefaultIPPoolLeaseEnv,
				Count:        13,
			}
		}
	}
	return
}

const (
	openshiftBranch = "openshift-4."
	releaseBranch   = "release-4."
	minimumVersion  = 16
)

// Currently, we only have the ability to utilize IP pools in 4.16 and later, we want to make sure not to allocate them
// on earlier versions
func branchValidForIPPoolLease(branch string) bool {
	if branch == "master" || branch == "main" {
		return true
	}
	var version string
	if strings.HasPrefix(branch, openshiftBranch) {
		version = strings.TrimPrefix(branch, openshiftBranch)
	}
	if strings.HasPrefix(branch, releaseBranch) {
		version = strings.TrimPrefix(branch, releaseBranch)
	}
	number, err := strconv.Atoi(version)
	if err != nil {
		return false
	}

	return number >= minimumVersion
}
