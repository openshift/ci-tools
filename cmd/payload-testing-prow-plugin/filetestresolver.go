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
	for _, org := range []string{"openshift", "openshift-eng"} {
		if v, ok := byOrgRepo[org]; ok {
			for _, configurations := range v {
				for _, configuration := range configurations {
					for _, element := range configuration.Tests {
						if element.IsPeriodic() {
							testName := configuration.Metadata.TestNameFromJobName(job, jc.PeriodicPrefix)
							if element.As == testName {
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
	}
	return api.MetadataWithTest{}, fmt.Errorf("failed to resolve job %s", job)
}
