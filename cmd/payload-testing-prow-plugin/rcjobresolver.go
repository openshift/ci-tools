package main

import (
	"fmt"
)

type releaseControllerJobResolver struct {
}

func newReleaseControllerJobResolver() (jobResolver, error) {
	ret := &releaseControllerJobResolver{}
	return ret, nil
}

func (r *releaseControllerJobResolver) resolve(_ string, releaseType releaseType, jobType jobType) ([]Job, error) {
	// TODO: Use rest API implemented in https://issues.redhat.com/browse/OCPCRT-109
	// promoted-image-governor has struct of RC's config. We can share it by then.
	switch releaseType {
	case nightlyRelease, ciRelease:
	default:
		return nil, fmt.Errorf("release type is not supported: %s", releaseType)

	}

	switch jobType {
	case informing, blocking, periodics, all:
	default:
		return nil, fmt.Errorf("job type is not supported: %s", jobType)

	}
	return []Job{
		{Name: "periodic-ci-openshift-release-master-nightly-4.10-e2e-aws-serial"},
		{Name: "periodic-ci-openshift-release-master-nightly-4.10-e2e-metal-ipi"},
	}, nil
}
