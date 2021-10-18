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

		// 4.10 CI
		newJob("periodic-ci-openshift-release-master-ci-4.10-e2e-gcp-upgrade").WithE2EParallel().ToJob(),
		newJob("periodic-ci-openshift-release-master-ci-4.10-upgrade-from-stable-4.9-e2e-aws-upgrade").ToJob(),
		newJob("periodic-ci-openshift-release-master-ci-4.10-upgrade-from-stable-4.9-e2e-aws-ovn-upgrade").ToJob(),
		newJob("periodic-ci-openshift-release-master-ci-4.10-e2e-aws-ovn-upgrade").ToJob(),
		newJob("periodic-ci-openshift-release-master-ci-4.10-e2e-aws-serial").ToJob(),
		newJob("periodic-ci-openshift-release-master-ci-4.10-e2e-gcp").ToJob(),
		// 4.10 nightly
		newJob("periodic-ci-openshift-release-master-ci-4.10-e2e-azure-upgrade-single-node").WithoutDisruption().ToJob(),
		newJob("periodic-ci-openshift-release-master-nightly-4.10-e2e-metal-ipi-upgrade").ToJob(),
		newJob("periodic-ci-openshift-release-master-nightly-4.10-upgrade-from-stable-4.9-e2e-metal-ipi-upgrade").ToJob(),
		newJob("periodic-ci-openshift-release-master-ci-4.10-e2e-azure-ovn-upgrade").ToJob(),
		newJob("periodic-ci-openshift-release-master-ci-4.10-upgrade-from-stable-4.9-e2e-gcp-ovn-upgrade").ToJob(),
		newJob("periodic-ci-openshift-release-master-ci-4.10-upgrade-from-stable-4.9-e2e-azure-upgrade").ToJob(),
		newJob("periodic-ci-openshift-release-master-nightly-4.10-e2e-aws-upgrade").ToJob(),
		newJob("periodic-ci-openshift-release-master-nightly-4.10-upgrade-from-stable-4.9-e2e-aws-upgrade").ToJob(),
		newJob("periodic-ci-openshift-release-master-nightly-4.10-e2e-aws").ToJob(),
		newJob("periodic-ci-openshift-release-master-nightly-4.10-e2e-aws-serial").ToJob(),
		newJob("periodic-ci-openshift-release-master-nightly-4.10-e2e-metal-ipi").WithoutDisruption().ToJob(),
		newJob("periodic-ci-openshift-release-master-nightly-4.10-e2e-metal-ipi-ovn-ipv6").WithoutDisruption().ToJob(),
		newJob("periodic-ci-openshift-release-master-nightly-4.10-e2e-metal-ipi-serial-ipv4").WithoutDisruption().ToJob(),
		newJob("periodic-ci-openshift-release-master-nightly-4.10-console-aws").WithoutDisruption().ToJob(),
		newJob("periodic-ci-openshift-release-master-nightly-4.10-e2e-aws-fips").ToJob(),
		newJob("periodic-ci-openshift-release-master-ci-4.10-e2e-aws-ovn").ToJob(),
		newJob("periodic-ci-openshift-release-master-nightly-4.10-e2e-aws-single-node").ToJob(),
		newJob("periodic-ci-openshift-release-master-nightly-4.10-e2e-aws-single-node-serial").ToJob(),
		newJob("periodic-ci-openshift-release-master-ci-4.10-e2e-aws-techpreview").ToJob(),
		newJob("periodic-ci-openshift-release-master-ci-4.10-e2e-aws-techpreview-serial").ToJob(),
		newJob("release-openshift-ocp-installer-e2e-aws-upi-4.10").ToJob(),
		newJob("periodic-ci-openshift-release-master-nightly-4.10-e2e-azure").ToJob(),
		newJob("periodic-ci-openshift-release-master-ci-4.10-e2e-azure-ovn").ToJob(),
		newJob("release-openshift-ocp-installer-e2e-azure-serial-4.10").ToJob(),
		newJob("periodic-ci-openshift-release-master-ci-4.10-e2e-azure-techpreview").ToJob(),
		newJob("periodic-ci-openshift-release-master-ci-4.10-e2e-azure-techpreview-serial").ToJob(),
		newJob("periodic-ci-openshift-release-master-nightly-4.10-e2e-gcp").ToJob(),
		newJob("periodic-ci-openshift-release-master-ci-4.10-e2e-gcp-ovn").ToJob(),
		newJob("periodic-ci-openshift-release-master-nightly-4.10-e2e-gcp-rt").ToJob(),
		newJob("release-openshift-ocp-installer-e2e-gcp-serial-4.10").ToJob(),
		newJob("periodic-ci-openshift-release-master-ci-4.10-e2e-gcp-techpreview").ToJob(),
		newJob("periodic-ci-openshift-release-master-ci-4.10-e2e-gcp-techpreview-serial").ToJob(),
		newJob("release-openshift-ocp-installer-e2e-metal-4.10").ToJob(),
		newJob("periodic-ci-openshift-release-master-nightly-4.10-e2e-metal-assisted").WithoutDisruption().ToJob(),
		newJob("periodic-ci-openshift-release-master-nightly-4.10-e2e-metal-assisted-ipv6").WithoutDisruption().ToJob(),
		newJob("periodic-ci-openshift-release-master-nightly-4.10-e2e-metal-ipi-ovn-dualstack").WithoutDisruption().ToJob(),
		newJob("periodic-ci-openshift-release-master-nightly-4.10-e2e-metal-ipi-virtualmedia").WithoutDisruption().ToJob(),
		newJob("release-openshift-ocp-installer-e2e-metal-serial-4.10").ToJob(),
		newJob("release-openshift-ocp-osd-aws-nightly-4.10").WithoutDisruption().ToJob(),
		newJob("release-openshift-ocp-osd-gcp-nightly-4.10").WithoutDisruption().ToJob(),
		newJob("periodic-ci-openshift-release-master-nightly-4.10-e2e-ovirt").ToJob(),
		newJob("periodic-ci-openshift-release-master-nightly-4.10-e2e-aws-proxy").WithoutDisruption().ToJob(),
		newJob("periodic-ci-openshift-release-master-nightly-4.10-e2e-metal-single-node-live-iso").WithoutDisruption().ToJob(),
		newJob("periodic-ci-openshift-release-master-nightly-4.10-e2e-vsphere").ToJob(),
		newJob("periodic-ci-openshift-release-master-nightly-4.10-e2e-vsphere-serial").ToJob(),
		newJob("periodic-ci-openshift-release-master-nightly-4.10-e2e-vsphere-techpreview").ToJob(),
		newJob("periodic-ci-openshift-release-master-nightly-4.10-e2e-vsphere-techpreview-serial").ToJob(),
		newJob("periodic-ci-openshift-release-master-nightly-4.10-e2e-vsphere-upi").ToJob(),
		newJob("periodic-ci-openshift-release-master-nightly-4.10-e2e-vsphere-upi-serial").ToJob(),
	}
)
