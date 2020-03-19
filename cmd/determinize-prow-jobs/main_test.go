package main

import (
	"testing"

	"k8s.io/test-infra/prow/config"
)

func TestDefaultJobConfig(t *testing.T) {
	jc := &config.JobConfig{
		PresubmitsStatic: map[string][]config.Presubmit{
			"a": {{}, {}, {JobBase: config.JobBase{Cluster: "default"}}},
			"b": {{}, {}},
		},
		PostsubmitsStatic: map[string][]config.Postsubmit{
			"a": {{}, {}, {JobBase: config.JobBase{Cluster: "default"}}},
			"b": {{}, {}},
		},
		Periodics: []config.Periodic{{}, {}, {JobBase: config.JobBase{Cluster: "default"}}},
	}

	defaultJobConfig(jc)

	for k := range jc.PresubmitsStatic {
		for _, j := range jc.PresubmitsStatic[k] {
			if j.Cluster != "api.ci" {
				t.Errorf("expected cluster to be 'api.ci', was '%s'", j.Cluster)
			}
		}
	}
	for k := range jc.PostsubmitsStatic {
		for _, j := range jc.PostsubmitsStatic[k] {
			if j.Cluster != "api.ci" {
				t.Errorf("expected cluster to be 'api.ci', was '%s'", j.Cluster)
			}
		}
	}
	for _, j := range jc.Periodics {
		if j.Cluster != "api.ci" {
			t.Errorf("expected cluster to be 'api.ci', was '%s'", j.Cluster)
		}
	}
}
