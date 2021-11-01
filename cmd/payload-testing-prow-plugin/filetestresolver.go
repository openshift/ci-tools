package main

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/config"
	jc "github.com/openshift/ci-tools/pkg/jobconfig"
)

type fileTestResolver struct {
	// TODO: Refresh when the data on files change
	tuples map[string]api.MetadataWithTest
}

func newFileTestResolver(dir string) (testResolver, error) {
	ret := &fileTestResolver{
		tuples: map[string]api.MetadataWithTest{},
	}
	if err := config.OperateOnCIOperatorConfigDir(dir, func(configuration *api.ReleaseBuildConfiguration, info *config.Info) error {
		if !strings.HasPrefix(filepath.Base(info.Filename), "openshift-release-master") {
			return nil
		}
		for _, element := range configuration.Tests {
			if element.Cron != nil || element.Interval != nil || element.ReleaseController {
				jobName := info.JobName(jc.PeriodicPrefix, element.As)
				ret.tuples[jobName] = api.MetadataWithTest{
					Metadata: configuration.Metadata,
					Test:     element.As,
				}
			}
		}
		return nil
	}); err != nil {
		return nil, fmt.Errorf("failed to operater on ci operator config dir %s: %w", dir, err)
	}
	return ret, nil
}

func (r *fileTestResolver) resolve(job string) (api.MetadataWithTest, error) {
	if jt, ok := r.tuples[job]; ok {
		return jt, nil
	}
	return api.MetadataWithTest{}, fmt.Errorf("failed to resolve job %s", job)
}
