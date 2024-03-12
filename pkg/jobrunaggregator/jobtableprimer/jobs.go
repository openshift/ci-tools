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
	reverseOrderedVersions := []string{}
	for _, release := range releases {
		reverseOrderedVersions = append(reverseOrderedVersions, release.Release)
	}
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
	jobsRowToCreate := []jobrunaggregatorapi.JobRow{}

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
