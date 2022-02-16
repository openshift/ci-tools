package jobtableprimer

import (
	_ "embed"
	"strings"

	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorapi"
)

const (
	gcp       = "gcp"
	aws       = "aws"
	azure     = "azure"
	metal     = "metal"
	vsphere   = "vsphere"
	ovirt     = "ovirt"
	openstack = "openstack"
	libvirt   = "libvirt"

	amd64   = "amd64"
	arm64   = "arm64"
	ppc64le = "ppc64le"
	s390x   = "s390x"

	sdn = "sdn"
	ovn = "ovn"

	ha     = "ha"
	single = "single"

	ipv4 = "ipv4"
	//ipv6 = "ipv6"
	//dual = "dual"

	v408 = "4.8"
	v409 = "4.9"
	v410 = "4.10"
	v411 = "4.11"
	v412 = "4.12"
	v413 = "4.13"
	v414 = "4.14"
	v415 = "4.15"
)

var (
	reverseOrderedVersions = []string{
		v415, v414, v413, v412, v411, v410, v409, v408,
	}
)

var (
	jobsToAnalyze = []jobrunaggregatorapi.JobRow{
		// 4.9
		newJob("periodic-ci-openshift-release-master-ci-4.9-e2e-gcp-upgrade-build02").WithE2EParallel().ToJob(),
		newJob("periodic-ci-openshift-release-master-ci-4.9-e2e-gcp-upgrade").ToJob(),
		newJob("periodic-ci-openshift-release-master-ci-4.9-upgrade-from-stable-4.8-e2e-aws-upgrade").WithE2EParallel().ToJob(),
		newJob("periodic-ci-openshift-release-master-ci-4.9-upgrade-from-stable-4.8-e2e-aws-ovn-upgrade").WithE2EParallel().ToJob(),
		newJob("periodic-ci-openshift-release-master-ci-4.9-e2e-azure-upgrade-single-node").WithoutDisruption().ToJob(),
		newJob("periodic-ci-openshift-release-master-nightly-4.9-e2e-metal-ipi-upgrade").WithE2EParallel().ToJob(),
		newJob("periodic-ci-openshift-release-master-nightly-4.9-upgrade-from-stable-4.8-e2e-metal-ipi-upgrade").WithE2EParallel().ToJob(),
		newJob("periodic-ci-openshift-release-master-nightly-4.9-e2e-aws-upgrade").WithE2EParallel().ToJob(),
		newJob("periodic-ci-openshift-release-master-nightly-4.9-upgrade-from-stable-4.8-e2e-aws-upgrade").WithE2EParallel().ToJob(),
		newJob("periodic-ci-openshift-release-master-ci-4.9-e2e-aws-serial").ToJob(),
		newJob("periodic-ci-openshift-release-master-ci-4.9-e2e-gcp").ToJob(),
		newJob("periodic-ci-openshift-release-master-nightly-4.9-e2e-aws").ToJob(),
		newJob("periodic-ci-openshift-release-master-nightly-4.9-e2e-aws-serial").ToJob(),
	}
)

//go:embed generated_job_names.txt
var jobNames string

func init() {
	for _, jobName := range strings.Split(jobNames, "\n") {
		// skip comments
		if strings.HasPrefix(jobName, "//") {
			continue
		}

		// skip empty lines
		jobName = strings.TrimSpace(jobName)
		if len(jobName) == 0 {
			continue
		}

		// skip duplicates.  This happens when periodics redefine release config ones.
		found := false
		for _, existing := range jobsToAnalyze {
			if existing.JobName == jobName {
				found = true
				break
			}
		}
		if found {
			continue
		}

		jobsToAnalyze = append(jobsToAnalyze, newJob(jobName).ToJob())
	}
}
