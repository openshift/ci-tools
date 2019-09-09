package rehearse

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"sort"
	"strings"

	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/test-infra/prow/apis/prowjobs/v1"
	prowconfig "k8s.io/test-infra/prow/config"
	"k8s.io/test-infra/prow/pod-utils/downwardapi"

	"github.com/openshift/ci-tools/pkg/config"
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
	ChangedPeriodics       []string `json:"changed_periodics"`
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
		ChangedPeriodics:   []string{},
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

func (m *Metrics) RecordChangedTemplates(ts []config.ConfigMapSource) {
	for _, t := range ts {
		m.ChangedTemplates = append(m.ChangedTemplates, t.Filename)
	}
}

func (m *Metrics) RecordChangedClusterProfiles(ps []config.ConfigMapSource) {
	for _, p := range ps {
		m.ChangedClusterProfiles = append(m.ChangedClusterProfiles, p.Name())
	}
}

func (m *Metrics) RecordChangedPresubmits(presubmits config.Presubmits) {
	for _, jobs := range presubmits {
		for _, job := range jobs {
			m.ChangedPresubmits = append(m.ChangedPresubmits, job.Name)
		}
	}
}

func (m *Metrics) RecordChangedPeriodics(periodics []prowconfig.Periodic) {
	for _, job := range periodics {
		m.ChangedPeriodics = append(m.ChangedPeriodics, job.Name)
	}
}

func (m *Metrics) RecordPresubmitsOpportunity(presubmits config.Presubmits, reason string) {
	for _, jobs := range presubmits {
		for _, job := range jobs {
			if _, ok := m.Opportunities[job.Name]; !ok {
				m.Opportunities[job.Name] = []string{reason}
			} else {
				m.Opportunities[job.Name] = append(m.Opportunities[job.Name], reason)
			}
		}
	}
}

func (m *Metrics) RecordPeriodicsOpportunity(periodics []prowconfig.Periodic, reason string) {
	for _, job := range periodics {
		m.Opportunities[job.Name] = append(m.Opportunities[job.Name], reason)
	}
}

func (m *Metrics) RecordActual(presubmits []*prowconfig.Presubmit, periodics []prowconfig.Periodic) {
	for _, job := range presubmits {
		m.Actual = append(m.Actual, job.Name)
	}

	for _, job := range periodics {
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
	var links []string
	for pr, runs := range m.matching {
		var runNumbers []string
		for _, run := range runs {
			runNumbers = append(runNumbers, run.JobSpec.BuildID)
		}
		line := fmt.Sprintf("- https://github.com/openshift/release/pull/%d (runs: %s)", pr, strings.Join(runNumbers, ", "))
		links = append(links, line)
	}

	pctPrs := float64(prCount) / float64(len(m.seenPrs)) * 100.0
	pctBuilds := float64(m.matchingBuilds) / float64(m.totalBuilds) * 100.0
	return fmt.Sprintf(template, m.purpose, prCount, len(m.seenPrs), pctPrs, m.matchingBuilds, m.totalBuilds, pctBuilds, strings.Join(links, "\n"))
}

type AllBuilds struct {
	Pulls map[int][]*Metrics
}

func (b *AllBuilds) Process(build *Metrics) {
	pr := build.JobSpec.Refs.Pulls[0].Number
	if len(b.Pulls[pr]) == 0 {
		b.Pulls[pr] = []*Metrics{}
	}
	b.Pulls[pr] = append(b.Pulls[pr], build)
}

func (b *AllBuilds) Sort() {
	for pr := range b.Pulls {
		sort.Slice(b.Pulls[pr], func(i, j int) bool {
			return b.Pulls[pr][i].JobSpec.BuildID < b.Pulls[pr][j].JobSpec.BuildID
		})
	}
}

func (b *AllBuilds) PrTotal() int {
	return len(b.Pulls)
}

func (b *AllBuilds) BuildsTotal() int {
	total := 0
	for _, builds := range b.Pulls {
		total += len(builds)
	}

	return total
}

type staleStatusOcc struct {
	pr                      int
	sha, oldBuild, newBuild string
	jobs                    []string
}

func (s *staleStatusOcc) message() string {
	template := "- https://github.com/openshift/release/pull/%d got stale statuses on SHA %s (old build=%s new build=%s):\n  - %s"
	return fmt.Sprintf(template, s.pr, s.sha, s.oldBuild, s.newBuild, strings.Join(s.jobs, "\n  - "))
}

type staleStatusStats struct {
	prHit, prTotal, prPct             int
	buildsHit, buildsTotal, buildsPct int
	occurrences                       []staleStatusOcc
}

func (s *staleStatusStats) messages() string {
	if s.buildsHit > 0 {
		messages := make([]string, 0, len(s.occurrences))
		for _, occ := range s.occurrences {
			messages = append(messages, occ.message())
		}
		return strings.Join(messages, "\n")
	}

	return "No occurrences"
}

func (s *staleStatusStats) createReport() string {
	template := `# Stale job statuses in PRs

PR statistics:    %d/%d (%d%%)
Build statistics: %d/%d (%d%%)

PR links:
%s
`
	return fmt.Sprintf(template, s.prHit, s.prTotal, s.prPct, s.buildsHit, s.buildsTotal, s.buildsPct, s.messages())
}

type StaleStatusCounter struct {
	Builds *AllBuilds
}

func (s *StaleStatusCounter) Process(build *Metrics) {
	s.Builds.Process(build)
}

func (s *StaleStatusCounter) computeStats() *staleStatusStats {
	s.Builds.Sort()

	stats := &staleStatusStats{}

	for pr, builds := range s.Builds.Pulls {
		prWasHit := false
		for i := 1; i < len(builds); i++ {
			oldBuild := builds[i-1]
			oldSHA := oldBuild.JobSpec.Refs.Pulls[0].SHA
			newBuild := builds[i]
			newSHA := newBuild.JobSpec.Refs.Pulls[0].SHA
			if newSHA == "" || oldSHA != newSHA {
				continue
			}
			var staleJobs []string
			for job := range oldBuild.Opportunities {
				if _, hasJob := newBuild.Opportunities[job]; !hasJob {
					staleJobs = append(staleJobs, job)
				}
			}
			if len(staleJobs) > 0 {
				prWasHit = true
				stats.buildsHit += len(staleJobs)
				occurrence := staleStatusOcc{
					pr:       pr,
					sha:      newSHA,
					oldBuild: oldBuild.JobSpec.BuildID,
					newBuild: newBuild.JobSpec.BuildID,
					jobs:     staleJobs,
				}
				stats.occurrences = append(stats.occurrences, occurrence)
			}
		}
		if prWasHit {
			stats.prHit++
		}
	}

	stats.prTotal = s.Builds.PrTotal()
	stats.buildsTotal = s.Builds.BuildsTotal()
	stats.prPct = int(float64(stats.prHit) / float64(stats.prTotal) * 100.0)
	stats.buildsPct = int(float64(stats.buildsHit) / float64(stats.buildsTotal) * 100.0)

	return stats
}

func (s *StaleStatusCounter) Report() string {
	return s.computeStats().createReport()
}
