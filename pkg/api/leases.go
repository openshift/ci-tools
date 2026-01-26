package api

import (
	"fmt"
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

const maxAddressesRequired = 13

func IPPoolLeaseForTest(s *MultiStageTestConfigurationLiteral, metadata Metadata) (ret StepLease) {
	p := s.ClusterProfile
	if p != "" {
		if lt := p.IPPoolLeaseType(); lt != "" {
			if !p.IPPoolLeaseShouldValidateBranch() || branchValidForIPPoolLease(metadata.Branch) {
				ret = StepLease{
					ResourceType: lt,
					Env:          DefaultIPPoolLeaseEnv,
					Count:        maxAddressesRequired,
				}
			}
		}
	}
	return
}

const (
	minimumMajorVersion = 4
	minimumMinorVersion = 16
)

// Currently, we only have the ability to utilize IP pools in 4.16 and later, we want to make sure not to allocate them
// on earlier versions. 5.x and later versions are supported.
func branchValidForIPPoolLease(branch string) bool {
	if branch == "master" || branch == "main" {
		return true
	}

	// Check for openshift-X.Y or release-X.Y pattern
	var majorVersion, minorVersion int
	var err error

	if strings.HasPrefix(branch, "openshift-") {
		versionPart := strings.TrimPrefix(branch, "openshift-")
		if majorVersion, minorVersion, err = parseMajorMinorVersion(versionPart); err != nil {
			return false
		}
	} else if strings.HasPrefix(branch, "release-") {
		versionPart := strings.TrimPrefix(branch, "release-")
		if majorVersion, minorVersion, err = parseMajorMinorVersion(versionPart); err != nil {
			return false
		}
	} else {
		return false
	}

	// 5.x and later are supported
	if majorVersion >= 5 {
		return true
	}

	// For 4.x, check minimum minor version
	if majorVersion == 4 {
		return minorVersion >= minimumMinorVersion
	}

	// Earlier major versions not supported
	return false
}

// parseMajorMinorVersion parses a version string like "4.16" or "5.1" into major and minor components
func parseMajorMinorVersion(version string) (int, int, error) {
	parts := strings.Split(version, ".")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("invalid version format: %s", version)
	}

	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, fmt.Errorf("invalid major version: %s", parts[0])
	}

	minor, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, fmt.Errorf("invalid minor version: %s", parts[1])
	}

	return major, minor, nil
}
