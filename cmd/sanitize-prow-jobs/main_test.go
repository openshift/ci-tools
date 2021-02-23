package main

import (
	"testing"

	"k8s.io/test-infra/prow/config"

	"github.com/openshift/ci-tools/pkg/dispatcher"
)

func TestDefaultJobConfig(t *testing.T) {
	jc := &config.JobConfig{
		PresubmitsStatic: map[string][]config.Presubmit{
			"a": {{}, {}, {JobBase: config.JobBase{Agent: "kubernetes", Cluster: "default"}}},
			"b": {{}, {}},
		},
		PostsubmitsStatic: map[string][]config.Postsubmit{
			"a": {{}, {}, {JobBase: config.JobBase{Agent: "kubernetes", Cluster: "default"}}},
			"b": {{}, {}},
		},
		Periodics: []config.Periodic{{}, {}, {JobBase: config.JobBase{Agent: "kubernetes", Cluster: "default"}}},
	}

	config := &dispatcher.Config{Default: "api.ci"}
	if err := defaultJobConfig(jc, "", config); err != nil {
		t.Errorf("failed default job config: %v", err)
	}

	for k := range jc.PresubmitsStatic {
		for _, j := range jc.PresubmitsStatic[k] {
			if j.Agent == "kubernetes" && j.Cluster != "api.ci" {
				t.Errorf("expected cluster to be 'api.ci', was '%s'", j.Cluster)
			}
		}
	}
	for k := range jc.PostsubmitsStatic {
		for _, j := range jc.PostsubmitsStatic[k] {
			if j.Agent == "kubernetes" && j.Cluster != "api.ci" {
				t.Errorf("expected cluster to be 'api.ci', was '%s'", j.Cluster)
			}
		}
	}
	for _, j := range jc.Periodics {
		if j.Agent == "kubernetes" && j.Cluster != "api.ci" {
			t.Errorf("expected cluster to be 'api.ci', was '%s'", j.Cluster)
		}
	}
}
