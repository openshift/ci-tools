package util

import "strings"

func IsSpecialInformingJobOnTestGrid(jobName string) bool {
	testGridInformingPrefixes := []string{
		"periodic-ci-openshift-cloud-credential-operator-",
		"periodic-ci-openshift-cluster-control-plane-machine-set-operator-",
		"periodic-ci-openshift-cluster-etcd-operator-",
		"periodic-ci-openshift-hypershift-main-periodics-",
		"periodic-ci-openshift-installer-master-altinfra-periodics-",
		"periodic-ci-openshift-multiarch",
		"periodic-ci-openshift-release-master-ci-",
		"periodic-ci-openshift-release-master-nightly-",
		"periodic-ci-openshift-release-master-okd-",
		"periodic-ci-shiftstack-shiftstack-ci-main-periodic-",
		"promote-release-openshift-",
		"release-openshift-",
	}
	for _, prefix := range testGridInformingPrefixes {
		if strings.HasPrefix(jobName, prefix) {
			return true
		}
	}
	return false
}
