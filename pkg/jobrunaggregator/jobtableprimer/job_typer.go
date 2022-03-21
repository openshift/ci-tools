package jobtableprimer

import (
	"strings"

	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorapi"
)

type jobRowBuilder struct {
	job *jobrunaggregatorapi.JobRow
}

func newJob(name string) *jobRowBuilder {
	platform := ""
	switch {
	case strings.Contains(name, "gcp"):
		platform = gcp
	case strings.Contains(name, "aws"):
		platform = aws
	case strings.Contains(name, "azure"):
		platform = azure
	case strings.Contains(name, "metal"):
		platform = metal
	case strings.Contains(name, "vsphere"):
		platform = vsphere
	case strings.Contains(name, "ovirt"):
		platform = ovirt
	case strings.Contains(name, "openstack"):
		platform = openstack
	case strings.Contains(name, "libvirt"):
		platform = libvirt
	}

	architecture := ""
	switch {
	case strings.Contains(name, "arm64"):
		architecture = arm64
	case strings.Contains(name, "ppc64le"):
		architecture = ppc64le
	case strings.Contains(name, "s390x"):
		architecture = s390x
	default:
		architecture = amd64
	}

	runsUpgrade := false
	if strings.Contains(name, "upgrade") {
		runsUpgrade = true
	}

	network := sdn
	if strings.Contains(name, "ovn") {
		network = ovn
	}

	topology := ha
	if strings.Contains(name, "single") {
		topology = single
	}

	// figure out some way to do the ip mode
	ipMode := ipv4

	versions := []string{}
	for _, curr := range reverseOrderedVersions {
		if strings.Contains(name, curr) {
			versions = append(versions, curr)
		}
	}
	currRelease := "unknown"
	if len(versions) >= 1 {
		currRelease = versions[0]
	}

	fromRelease := ""
	if runsUpgrade {
		switch {
		case len(versions) == 1:
			fromRelease = versions[0]
		case len(versions) >= 2:
			fromRelease = versions[1]
		}
	}

	runsSerial := false
	if strings.Contains(name, "serial") {
		runsSerial = true
	}

	runsE2E := false
	if !runsUpgrade && !runsSerial {
		runsE2E = true
	}

	return &jobRowBuilder{
		job: &jobrunaggregatorapi.JobRow{
			JobName:                     name,
			GCSBucketName:               "origin-ci-test",
			GCSJobHistoryLocationPrefix: "logs/" + name,
			Platform:                    platform,
			Architecture:                architecture,
			Network:                     network,
			IPMode:                      ipMode,
			Topology:                    topology,
			Release:                     currRelease,
			FromRelease:                 fromRelease,
			CollectDisruption:           true, // by default we collect disruption
			CollectTestRuns:             true, // by default we collect disruption
			RunsUpgrade:                 runsUpgrade,
			RunsE2EParallel:             runsE2E,
			RunsE2ESerial:               runsSerial,
		},
	}
}

func (b *jobRowBuilder) WithoutDisruption() *jobRowBuilder {
	b.job.CollectDisruption = false
	return b
}

func (b *jobRowBuilder) WithoutTestRuns() *jobRowBuilder {
	b.job.CollectTestRuns = true
	return b
}

func (b *jobRowBuilder) WithE2EParallel() *jobRowBuilder {
	b.job.RunsE2EParallel = true
	return b
}

func (b *jobRowBuilder) ToJob() jobrunaggregatorapi.JobRow {
	return *b.job
}
