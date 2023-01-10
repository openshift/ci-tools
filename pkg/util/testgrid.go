package util

import "strings"

func IsSpecialInformingJobOnTestGrid(jobName string) bool {
	testGridInformingPrefixes := []string{
		"release-openshift-",
		"promote-release-openshift-",
		"periodic-ci-openshift-hypershift-main-periodics-",
		"periodic-ci-openshift-multiarch",
		"periodic-ci-openshift-release-master-ci-",
		"periodic-ci-openshift-release-master-okd-",
		"periodic-ci-openshift-release-master-nightly-",
		"periodic-ci-shiftstack-shiftstack-ci-main-periodic-",
	}
	for _, prefix := range testGridInformingPrefixes {
		if strings.HasPrefix(jobName, prefix) {
			return true
		}
	}
	return false
}
