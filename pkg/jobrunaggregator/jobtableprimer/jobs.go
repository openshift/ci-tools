package jobtableprimer

import (
	_ "embed"
	"sort"
	"strconv"
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
	alibaba   = "alibaba"
	ibmcloud  = "ibmcloud"

	amd64   = "amd64"
	arm64   = "arm64"
	ppc64le = "ppc64le"
	s390x   = "s390x"

	sdn = "sdn"
	ovn = "ovn"

	ha       = "ha"
	single   = "single"
	external = "external"

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
	v416 = "4.16"
)

var (
	// defaultReverseOrderedVersions lists the default releases in reverse order
	defaultReverseOrderedVersions = []string{
		v416, v415, v414, v413, v412, v411, v410, v409, v408,
	}
)

// jobRowListBuilder builds the list of job rows used to prime the job table
type jobRowListBuilder struct {
	releases []jobrunaggregatorapi.ReleaseRow
}

func newJobRowListBuilder(releases []jobrunaggregatorapi.ReleaseRow) *jobRowListBuilder {
	return &jobRowListBuilder{
		releases: releases,
	}
}

func (j *jobRowListBuilder) getReverseOrderedVersions(releases []jobrunaggregatorapi.ReleaseRow) []string {
	reverseOrderedVersions := defaultReverseOrderedVersions
	newReleases := []string{}
	for _, release := range releases {
		found := false
		for _, version := range reverseOrderedVersions {
			if strings.Contains(version, release.Release) {
				found = true
				break
			}
		}
		if !found {
			newReleases = append(newReleases, release.Release)
		}
	}
	reverseOrderedVersions = append(reverseOrderedVersions, newReleases...)
	sort.Slice(reverseOrderedVersions, func(i, j int) bool {
		iVersionStrs := strings.Split(reverseOrderedVersions[i], ".")
		if len(iVersionStrs) < 2 {
			return false
		}
		iMajor, err := strconv.ParseInt(iVersionStrs[0], 10, 64)
		if err != nil {
			return false
		}
		iMinor, err := strconv.ParseInt(iVersionStrs[1], 10, 64)
		if err != nil {
			return false
		}
		jVersionStrs := strings.Split(reverseOrderedVersions[j], ".")
		if len(jVersionStrs) < 2 {
			return false
		}
		jMajor, err := strconv.ParseInt(jVersionStrs[0], 10, 64)
		if err != nil {
			return false
		}
		jMinor, err := strconv.ParseInt(jVersionStrs[1], 10, 64)
		if err != nil {
			return false
		}

		return iMajor > jMajor || iMinor > jMinor
	})
	return reverseOrderedVersions
}

func (j *jobRowListBuilder) CreateAllJobRows(jobNames []string) []jobrunaggregatorapi.JobRow {
	reverseOrderedVersions := j.getReverseOrderedVersions(j.releases)
	// Start with a default set of jobs
	jobsRowToCreate := []jobrunaggregatorapi.JobRow{
		// 4.9
		newJob("periodic-ci-openshift-release-master-ci-4.9-e2e-gcp-upgrade-build02", reverseOrderedVersions).WithE2EParallel().ToJob(),
		newJob("periodic-ci-openshift-release-master-ci-4.9-e2e-gcp-upgrade", reverseOrderedVersions).ToJob(),
		newJob("periodic-ci-openshift-release-master-ci-4.9-upgrade-from-stable-4.8-e2e-aws-upgrade", reverseOrderedVersions).WithE2EParallel().ToJob(),
		newJob("periodic-ci-openshift-release-master-ci-4.9-upgrade-from-stable-4.8-e2e-aws-ovn-upgrade", reverseOrderedVersions).WithE2EParallel().ToJob(),
		newJob("periodic-ci-openshift-release-master-ci-4.9-e2e-azure-upgrade-single-node", reverseOrderedVersions).WithoutDisruption().ToJob(),
		newJob("periodic-ci-openshift-release-master-nightly-4.9-e2e-metal-ipi-upgrade", reverseOrderedVersions).WithE2EParallel().ToJob(),
		newJob("periodic-ci-openshift-release-master-nightly-4.9-upgrade-from-stable-4.8-e2e-metal-ipi-upgrade", reverseOrderedVersions).WithE2EParallel().ToJob(),
		newJob("periodic-ci-openshift-release-master-nightly-4.9-e2e-aws-upgrade", reverseOrderedVersions).WithE2EParallel().ToJob(),
		newJob("periodic-ci-openshift-release-master-nightly-4.9-upgrade-from-stable-4.8-e2e-aws-upgrade", reverseOrderedVersions).WithE2EParallel().ToJob(),
		newJob("periodic-ci-openshift-release-master-ci-4.9-e2e-aws-serial", reverseOrderedVersions).ToJob(),
		newJob("periodic-ci-openshift-release-master-ci-4.9-e2e-gcp", reverseOrderedVersions).ToJob(),
		newJob("periodic-ci-openshift-release-master-nightly-4.9-e2e-aws", reverseOrderedVersions).ToJob(),
		newJob("periodic-ci-openshift-release-master-nightly-4.9-e2e-aws-serial", reverseOrderedVersions).ToJob(),
	}

	for _, jobName := range jobNames {
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
		for _, existing := range jobsRowToCreate {
			if existing.JobName == jobName {
				found = true
				break
			}
		}
		if found {
			continue
		}

		jobsRowToCreate = append(jobsRowToCreate, newJob(jobName, reverseOrderedVersions).ToJob())
	}
	return jobsRowToCreate
}
