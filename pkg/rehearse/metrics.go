package rehearse

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"strings"

	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/test-infra/prow/apis/prowjobs/v1"
	prowconfig "k8s.io/test-infra/prow/config"
	"k8s.io/test-infra/prow/pod-utils/downwardapi"

	"github.com/openshift/ci-operator-prowgen/pkg/config"
)

type ExecutionMetrics struct {
	SubmittedRehearsals []string `json:"submitted"`
	FailedRehearsals    []string `json:"failed"`
	PassedRehearsals    []string `json:"successful"`
}

type Metrics struct {
	JobSpec *downwardapi.JobSpec `json:"spec"`

	ChangedCiopConfigs     []string `json:"changed_ciop_configs"`
	ChangedPresubmits      []string `json:"changed_presubmits"`
	ChangedTemplates       []string `json:"changed_templates"`
	ChangedClusterProfiles []string `json:"changed_cluster_profiles"`

	// map a job name to a list of reasons why we want to rehearse it
	Opportunities map[string][]string `json:"opportunities"`
	Actual        []string            `json:"actual"`

	Execution *ExecutionMetrics `json:"execution"`

	logger logrus.Entry
	file   string

	// DEPRECATED (we need to keep these to read old artifacts)
	Org  string `json:"org"`
	Repo string `json:"repo"`
	Pr   int    `json:"pr"`
}

func NewMetrics(file string) *Metrics {
	return &Metrics{
		ChangedCiopConfigs: []string{},
		ChangedPresubmits:  []string{},
		ChangedTemplates:   []string{},

		Opportunities: map[string][]string{},
		Actual:        []string{},

		file: file,
	}
}

func (m *Metrics) RecordChangedCiopConfigs(configs config.CompoundCiopConfig) {
	for configName := range configs {
		m.ChangedCiopConfigs = append(m.ChangedCiopConfigs, configName)
	}
}

func (m *Metrics) RecordChangedTemplates(templates config.CiTemplates) {
	for templateName := range templates {
		m.ChangedTemplates = append(m.ChangedTemplates, templateName)
	}
}

func (m *Metrics) RecordChangedClusterProfiles(ps []config.ClusterProfile) {
	for _, p := range ps {
		m.ChangedClusterProfiles = append(m.ChangedClusterProfiles, p.Name)
	}
}

func (m *Metrics) RecordChangedPresubmits(presubmits config.Presubmits) {
	for _, jobs := range presubmits {
		for _, job := range jobs {
			m.ChangedPresubmits = append(m.ChangedPresubmits, job.Name)
		}
	}
}

func (m *Metrics) RecordOpportunity(toRehearse config.Presubmits, reason string) {
	for _, jobs := range toRehearse {
		for _, job := range jobs {
			if _, ok := m.Opportunities[job.Name]; !ok {
				m.Opportunities[job.Name] = []string{reason}
			} else {
				m.Opportunities[job.Name] = append(m.Opportunities[job.Name], reason)
			}
		}
	}
}

func (m *Metrics) RecordActual(rehearsals []*prowconfig.Presubmit) {
	for _, job := range rehearsals {
		m.Actual = append(m.Actual, job.Name)
	}
}

func (m *Metrics) Dump() {
	if m.file != "" {
		payload, err := json.MarshalIndent(m, "", "  ")
		if err != nil {
			m.logger.Warn("Failed to marshal metrics to JSON")
			return
		}

		if err := ioutil.WriteFile(m.file, payload, 0644); err != nil {
			m.logger.Warn("Failed to dump metrics")
		}
	}
}

func LoadMetrics(path string) (*Metrics, error) {
	data, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, err
	}

	metrics := Metrics{}
	if err = json.Unmarshal(data, &metrics); err != nil {
		return nil, err
	}

	if metrics.JobSpec == nil {
		// old metrics artifact: partially reconstruct refs from the info we saved at the time
		metrics.JobSpec = &downwardapi.JobSpec{
			Refs: &v1.Refs{Org: metrics.Org, Repo: metrics.Repo, Pulls: []v1.Pull{{Number: metrics.Pr}}},
		}
	}

	return &metrics, nil
}

type MetricsCounter interface {
	Process(metrics *Metrics)
	Report() string
}

type metricsCounter struct {
	purpose        string
	filter         func(metrics *Metrics) bool
	seenPrs        sets.Int
	totalBuilds    int
	matchingBuilds int
	matching       map[int][]*Metrics
}

func NewMetricsCounter(purpose string, filter func(metrics *Metrics) bool) MetricsCounter {
	return &metricsCounter{
		purpose:        purpose,
		filter:         filter,
		seenPrs:        sets.NewInt(),
		totalBuilds:    0,
		matchingBuilds: 0,
		matching:       map[int][]*Metrics{},
	}
}

func (m *metricsCounter) Process(metrics *Metrics) {
	m.totalBuilds++
	pr := metrics.JobSpec.Refs.Pulls[0].Number
	m.seenPrs.Insert(pr)
	if m.filter(metrics) {
		m.matchingBuilds++
		if m.matching[pr] == nil {
			m.matching[pr] = []*Metrics{metrics}
		} else {
			m.matching[pr] = append(m.matching[pr], metrics)
		}
	}
}

func (m *metricsCounter) Report() string {
	template := `# %s

PR statistics:    %d/%d (%.f%%)
Build statistics: %d/%d (%.f%%)

PR links:
%s
`
	prCount := len(m.matching)
	links := []string{}
	for pr, runs := range m.matching {
		runNumbers := []string{}
		for _, run := range runs {
			runNumbers = append(runNumbers, run.JobSpec.BuildID)
		}
		line := fmt.Sprintf("- https://github.com/openshift/release/pull/%d (runs: %s)", pr, strings.Join(runNumbers, ","))
		links = append(links, line)
	}

	pctPrs := float64(prCount) / float64(len(m.seenPrs)) * 100.0
	pctBuilds := float64(m.matchingBuilds) / float64(m.totalBuilds) * 100.0
	return fmt.Sprintf(template, m.purpose, prCount, len(m.seenPrs), pctPrs, m.matchingBuilds, m.totalBuilds, pctBuilds, strings.Join(links, "\n"))
}
