package main

import (
	"fmt"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/release"
	"github.com/openshift/ci-tools/pkg/release/config"
)

type releaseControllerJobResolver struct {
	httpClient release.HTTPClient
}

func newReleaseControllerJobResolver(httpClient release.HTTPClient) jobResolver {
	return &releaseControllerJobResolver{httpClient: httpClient}
}

func (r *releaseControllerJobResolver) resolve(ocp string, releaseType api.ReleaseStream, jobType config.JobType) ([]config.Job, error) {
	if releaseType != api.ReleaseStreamNightly && releaseType != api.ReleaseStreamCI {
		return nil, fmt.Errorf("release type is not supported: %s", releaseType)
	}

	if jobType != config.Informing && jobType != config.Blocking && jobType != config.Periodics && jobType != config.All {
		return nil, fmt.Errorf("job type is not supported: %s", jobType)
	}
	return config.ResolveJobs(r.httpClient, api.Candidate{
		ReleaseDescriptor: api.ReleaseDescriptor{
			Product:      api.ReleaseProductOCP,
			Architecture: api.ReleaseArchitectureAMD64,
		},
		Stream:  releaseType,
		Version: ocp,
	}, jobType)
}
