package jobtableprimer

import "github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorapi"

const (
	gcp   = "gcp"
	aws   = "aws"
	azure = "azure"
	metal = "metal"

	sdn = "sdn"
	ovn = "ovn"

	ha     = "ha"
	single = "single"

	ipv4 = "ipv4"
	ipv6 = "ipv6"
	dual = "dual"

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
		newJob("periodic-ci-openshift-release-master-ci-4.9-e2e-gcp-upgrade-build02").
			WithTestRuns().
			WithE2EParallel().
			ToJob(),
		newJob("periodic-ci-openshift-release-master-ci-4.9-e2e-gcp-upgrade").
			WithTestRuns().
			ToJob(),
		newJob("periodic-ci-openshift-release-master-ci-4.9-upgrade-from-stable-4.8-e2e-aws-upgrade").
			WithTestRuns().
			WithE2EParallel().
			ToJob(),
		newJob("periodic-ci-openshift-release-master-ci-4.9-upgrade-from-stable-4.8-e2e-aws-ovn-upgrade").
			WithTestRuns().
			WithE2EParallel().
			ToJob(),
		newJob("periodic-ci-openshift-release-master-ci-4.9-e2e-azure-upgrade-single-node").
			WithTestRuns().
			ToJob(),
		newJob("periodic-ci-openshift-release-master-nightly-4.9-e2e-metal-ipi-upgrade").
			WithTestRuns().
			WithE2EParallel().
			ToJob(),
		newJob("periodic-ci-openshift-release-master-nightly-4.9-upgrade-from-stable-4.8-e2e-metal-ipi-upgrade").
			WithTestRuns().
			WithE2EParallel().
			ToJob(),
		newJob("periodic-ci-openshift-release-master-nightly-4.9-e2e-aws-upgrade").
			WithTestRuns().
			WithE2EParallel().
			ToJob(),
		newJob("periodic-ci-openshift-release-master-nightly-4.9-upgrade-from-stable-4.8-e2e-aws-upgrade").
			WithTestRuns().
			WithE2EParallel().
			ToJob(),
		newJob("periodic-ci-openshift-release-master-ci-4.9-e2e-aws-serial").
			ToJob(),
		newJob("periodic-ci-openshift-release-master-ci-4.9-e2e-gcp").
			ToJob(),
		newJob("periodic-ci-openshift-release-master-nightly-4.9-e2e-aws").
			ToJob(),
		newJob("periodic-ci-openshift-release-master-nightly-4.9-e2e-aws-serial").
			ToJob(),
	}
)
