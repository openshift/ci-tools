package sanitizer

import (
	"testing"

	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/prow/pkg/config"

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
	if err := defaultJobConfig(jc, "", config, nil, make(sets.Set[string])); err != nil {
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

func TestIsCIOperatorLatest(t *testing.T) {
	testCases := []struct {
		name     string
		image    string
		expected bool
	}{
		{name: "standard image", image: "ci-operator:latest", expected: true},
		{name: "full registry path", image: "registry/namespace/ci-operator:latest", expected: true},
		{name: "different tag", image: "registry/namespace/ci-operator:other", expected: false},
		{name: "fifferent image", image: "other-image:latest", expected: false},
	}
	for _, tc := range testCases {
		t.Run(tc.image, func(t *testing.T) {
			got := isCIOperatorLatest(tc.image)
			if got != tc.expected {
				t.Errorf("For image %s, expected %v but got %v", tc.image, tc.expected, got)
			}
		})
	}
}
