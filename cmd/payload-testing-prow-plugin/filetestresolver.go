package main

import (
	"fmt"

	"github.com/openshift/ci-tools/pkg/api"
	jc "github.com/openshift/ci-tools/pkg/jobconfig"
	"github.com/openshift/ci-tools/pkg/load/agents"
)

type fileTestResolver struct {
	configAgent agents.ConfigAgent
}

func (r *fileTestResolver) resolve(job string) (api.MetadataWithTest, error) {
	byOrgRepo := r.configAgent.GetAll()
	if v, ok := byOrgRepo["openshift"]; ok {
		for _, configurations := range v {
			for _, configuration := range configurations {
				for _, element := range configuration.Tests {
					if element.IsPeriodic() {
						jobName := configuration.Metadata.JobName(jc.PeriodicPrefix, element.As)
						if jobName == job {
							return api.MetadataWithTest{
								Metadata: configuration.Metadata,
								Test:     element.As,
							}, nil
						}
					}
				}
			}
		}
	}
	return api.MetadataWithTest{}, fmt.Errorf("failed to resolve job %s", job)
}
