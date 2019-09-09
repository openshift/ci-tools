package rehearse

import (
	"fmt"
	"reflect"
	"sort"
	"testing"

	"github.com/openshift/ci-tools/pkg/api"
	"k8s.io/apimachinery/pkg/util/diff"
	"k8s.io/test-infra/prow/apis/prowjobs/v1"
	prowconfig "k8s.io/test-infra/prow/config"
	"k8s.io/test-infra/prow/pod-utils/downwardapi"

	"github.com/openshift/ci-tools/pkg/config"
)

func TestRecordChangedCiopConfigs(t *testing.T) {
	testFilename := ""

	testCases := []struct {
		description string
		configs     []string
		expected    []string
	}{{
		description: "no changed configs",
		expected:    []string{},
	}, {
		description: "changed configs",
		configs:     []string{"org-repo-branch.yaml", "another-org-repo-branch.yaml"},
		expected:    []string{"another-org-repo-branch.yaml", "org-repo-branch.yaml"},
	}}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			metrics := NewMetrics(testFilename)
			testCiopConfig := config.CompoundCiopConfig{}
			for _, ciopConfig := range tc.configs {
				testCiopConfig[ciopConfig] = &api.ReleaseBuildConfiguration{}
			}
			metrics.RecordChangedCiopConfigs(testCiopConfig)
			sort.Strings(metrics.ChangedCiopConfigs)
			if !reflect.DeepEqual(tc.expected, metrics.ChangedCiopConfigs) {
				t.Errorf("Recorded changed ci-operator configs differ from expected:\n%s", diff.ObjectReflectDiff(tc.expected, metrics.ChangedCiopConfigs))
			}
		})
	}
}

func TestRecordChangedTemplates(t *testing.T) {
	testFilename := ""

	testCases := []struct {
		description string
		templates   []config.ConfigMapSource
		expected    []string
	}{{
		description: "no changed templates",
		expected:    []string{},
	}, {
		description: "changed templates",
		templates: []config.ConfigMapSource{
			{Filename: "awesome-openshift-installer.yaml"},
			{Filename: "old-ugly-ansible-installer.yaml"},
		},
		expected: []string{"awesome-openshift-installer.yaml", "old-ugly-ansible-installer.yaml"},
	}}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			metrics := NewMetrics(testFilename)
			metrics.RecordChangedTemplates(tc.templates)
			sort.Strings(metrics.ChangedTemplates)
			if !reflect.DeepEqual(tc.expected, metrics.ChangedTemplates) {
				t.Errorf("Recorded changed templates differ from expected:\n%s", diff.ObjectReflectDiff(tc.expected, metrics.ChangedTemplates))
			}
		})
	}
}

func TestRecordChangedPresubmits(t *testing.T) {
	testFilename := ""

	var testCases = []struct {
		description string
		presubmits  map[string][]string
		expected    []string
	}{{
		description: "no changed presubmits",
		expected:    []string{},
	}, {
		description: "changed in a single repo",
		presubmits:  map[string][]string{"org/repo": {"org-repo-job", "org-repo-another-job"}},
		expected:    []string{"org-repo-another-job", "org-repo-job"},
	}, {
		description: "changed in multiple repos",
		presubmits: map[string][]string{
			"org/repo":         {"org-repo-job", "org-repo-another-job"},
			"org/another-repo": {"org-another-repo-job"},
		},
		expected: []string{"org-another-repo-job", "org-repo-another-job", "org-repo-job"},
	},
	}
	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			metrics := NewMetrics(testFilename)
			testPresubmits := config.Presubmits{}
			for repo, repoPresubmits := range tc.presubmits {
				testPresubmits[repo] = []prowconfig.Presubmit{}
				for _, presubmit := range repoPresubmits {
					testPresubmits[repo] = append(testPresubmits[repo], prowconfig.Presubmit{JobBase: prowconfig.JobBase{Name: presubmit}})
				}

			}
			metrics.RecordChangedPresubmits(testPresubmits)
			sort.Strings(metrics.ChangedPresubmits)
			if !reflect.DeepEqual(tc.expected, metrics.ChangedPresubmits) {
				t.Errorf("Recorded changed presubmits differ from expected:\n%s", diff.ObjectReflectDiff(tc.expected, metrics.ChangedPresubmits))
			}
		})
	}
}

func TestRecordPresubmitsOpportunity(t *testing.T) {
	testFilename := ""

	var testCases = []struct {
		description string
		existing    map[string][]string
		presubmits  map[string][]string
		reason      string
		expected    map[string][]string
	}{{
		description: "no opportunities",
		existing:    map[string][]string{},
		reason:      "no reason",
		expected:    map[string][]string{},
	}, {
		description: "opportunity in a single repo",
		existing:    map[string][]string{},
		presubmits:  map[string][]string{"org/repo": {"org-repo-job", "org-repo-another-job"}},
		reason:      "something changed",
		expected: map[string][]string{
			"org-repo-another-job": {"something changed"},
			"org-repo-job":         {"something changed"},
		},
	}, {
		description: "opportunities in multiple repos",
		existing:    map[string][]string{},
		presubmits: map[string][]string{
			"org/repo":         {"org-repo-job", "org-repo-another-job"},
			"org/another-repo": {"org-another-repo-job"},
		},
		reason: "something changed",
		expected: map[string][]string{
			"org-another-repo-job": {"something changed"},
			"org-repo-another-job": {"something changed"},
			"org-repo-job":         {"something changed"},
		},
	}, {
		description: "opportunities for multiple reasons",
		existing:    map[string][]string{"org-repo-job": {"something changed"}},
		presubmits:  map[string][]string{"org/repo": {"org-repo-job"}},
		reason:      "something else changed",
		expected: map[string][]string{
			"org-repo-job": {"something changed", "something else changed"},
		},
	}}
	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			metrics := NewMetrics(testFilename)
			testPresubmits := config.Presubmits{}
			for repo, repoPresubmits := range tc.presubmits {
				testPresubmits[repo] = []prowconfig.Presubmit{}
				for _, presubmit := range repoPresubmits {
					testPresubmits[repo] = append(testPresubmits[repo], prowconfig.Presubmit{JobBase: prowconfig.JobBase{Name: presubmit}})
				}

			}
			metrics.Opportunities = tc.existing
			metrics.RecordPresubmitsOpportunity(testPresubmits, tc.reason)
			if !reflect.DeepEqual(tc.expected, metrics.Opportunities) {
				t.Errorf("Recorded rehearsal opportunities differ from expected:\n%s", diff.ObjectReflectDiff(tc.expected, metrics.Opportunities))
			}
		})
	}
}

func TestRecordActual(t *testing.T) {
	testFilename := ""
	testCases := []struct {
		description string
		jobs        []string
		expected    []string
	}{{
		description: "no actual rehearsals",
		expected:    []string{},
	}, {
		description: "actual rehearsals are recorded",
		jobs:        []string{"rehearse-org-repo-job", "rehearse-org-repo-another-job"},
		expected:    []string{"rehearse-org-repo-another-job", "rehearse-org-repo-job"},
	}}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			metrics := NewMetrics(testFilename)
			var presubmits []*prowconfig.Presubmit
			for _, name := range tc.jobs {
				presubmits = append(presubmits, &prowconfig.Presubmit{JobBase: prowconfig.JobBase{Name: name}})
			}
			metrics.RecordActual(presubmits, nil)
			sort.Strings(metrics.Actual)
			if !reflect.DeepEqual(tc.expected, metrics.Actual) {
				t.Errorf("Recorded rehearsals differ from expected:\n%s", diff.ObjectReflectDiff(tc.expected, metrics.Actual))
			}
		})
	}
}

func TestMetricsCounter(t *testing.T) {
	counter := NewMetricsCounter("Testing counter counting only PRs above 99", func(metrics *Metrics) bool {
		return metrics.JobSpec.Refs.Pulls[0].Number > 99
	})

	for i := 1; i <= 100; i++ {
		counter.Process(&Metrics{JobSpec: &downwardapi.JobSpec{
			BuildID: fmt.Sprintf("x%d", i),
			Refs:    &v1.Refs{Pulls: []v1.Pull{{Number: i}}},
		}})
		counter.Process(&Metrics{JobSpec: &downwardapi.JobSpec{
			BuildID: fmt.Sprintf("y%d", i),
			Refs:    &v1.Refs{Pulls: []v1.Pull{{Number: i}}},
		}})
	}

	expected := `# Testing counter counting only PRs above 99

PR statistics:    1/100 (1%)
Build statistics: 2/200 (1%)

PR links:
- https://github.com/openshift/release/pull/100 (runs: x100, y100)
`

	actual := counter.Report()
	if actual != expected {
		t.Errorf("Report differs from expected:\n%s", diff.StringDiff(expected, actual))
	}
}

func TestStaleStatusCounter(t *testing.T) {
	makeTestBuild := func(pr int, id, sha string, jobs []string) *Metrics {
		opps := map[string][]string{}
		for _, job := range jobs {
			opps[job] = []string{"some reason to rehearse"}
		}
		return &Metrics{
			JobSpec: &downwardapi.JobSpec{
				BuildID: id,
				Refs: &v1.Refs{
					Pulls: []v1.Pull{{Number: pr, SHA: sha}},
				},
			},
			Opportunities: opps,
		}
	}
	testCases := []struct {
		description string
		builds      []*Metrics
		expected    *staleStatusStats
	}{{
		description: "job rehearsed in two subsequent builds over same sha is not stale",
		builds: []*Metrics{
			makeTestBuild(1, "build-1", "SHA", []string{"rehearsed-job"}),
			makeTestBuild(1, "build-2", "SHA", []string{"rehearsed-job"}),
		},
		expected: &staleStatusStats{prHit: 0, prTotal: 1, prPct: 0, buildsHit: 0, buildsTotal: 2, buildsPct: 0},
	}, {
		description: "job rehearsed in old build but not in new one is not stale when SHA differs",
		builds: []*Metrics{
			makeTestBuild(1, "build-1", "SHA", []string{"rehearsed-job"}),
			makeTestBuild(1, "build-2", "D11FFE2E47SHA", []string{"another-rehearsed-job"}),
		},
		expected: &staleStatusStats{prHit: 0, prTotal: 1, prPct: 0, buildsHit: 0, buildsTotal: 2, buildsPct: 0},
	}, {
		description: "job rehearsed in old build but not in new one over same SHA is stale",
		builds: []*Metrics{
			makeTestBuild(1, "build-1", "SHA", []string{"stale-job", "rehearsed-job"}),
			makeTestBuild(1, "build-2", "SHA", []string{"rehearsed-job"}),
		},
		expected: &staleStatusStats{
			prHit: 1, prTotal: 1, prPct: 100, buildsHit: 1, buildsTotal: 2, buildsPct: 50,
			occurrences: []staleStatusOcc{{pr: 1, sha: "SHA", oldBuild: "build-1", newBuild: "build-2", jobs: []string{"stale-job"}}},
		},
	},
	}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			allBuilds := AllBuilds{Pulls: map[int][]*Metrics{}}
			counter := StaleStatusCounter{&allBuilds}
			for _, build := range tc.builds {
				counter.Process(build)
			}
			stats := counter.computeStats()
			if !reflect.DeepEqual(tc.expected, stats) {
				t.Errorf("Stats differ from expected:\n%s", diff.ObjectReflectDiff(tc.expected, stats))
			}
		})
	}
}
