package deprecatetemplates

import (
	"os"
	"sort"
	"strings"

	"github.com/ghodss/yaml"
	"github.com/sirupsen/logrus"

	prowv1 "k8s.io/test-infra/prow/apis/prowjobs/v1"
	"k8s.io/test-infra/prow/config"

	jc "github.com/openshift/ci-tools/pkg/jobconfig"
	"github.com/openshift/ci-tools/pkg/prowgen"
	"github.com/openshift/ci-tools/pkg/util/gzip"
)

const (
	release = "release"
	unknown = "unknown"

	blockerColUnknown = "unknown"
	blockerColTotal   = "total"
)

func getKind(job config.JobBase) string {
	if strings.HasPrefix(job.Name, "pull-ci") {
		return string(prowv1.PresubmitJob)
	} else if strings.HasPrefix(job.Name, "branch-ci-") {
		return string(prowv1.PostsubmitJob)
	} else if strings.HasPrefix(job.Name, "release-") {
		return release
	} else if strings.HasPrefix(job.Name, "periodic-") {
		return string(prowv1.PeriodicJob)
	}

	// this is fine, it is best effort
	// the allowlist does not need to be 100% precise category-wise
	return unknown
}

type blockedJob struct {
	// unexported field so it is never serialized and it is `false` by default on
	// read. We touch this field when we process jobs so that we know this is a
	// still existing job, so that we can recognize nonexistent jobs later and remove
	// them
	current bool

	Generated bool   `json:"generated"`
	Kind      string `json:"kind"`
}

type blockedJobs map[string]blockedJob

func (b blockedJobs) Has(job config.JobBase) bool {
	_, has := b[job.Name]
	return has
}

func (b blockedJobs) Insert(job config.JobBase) error {
	generated, err := jc.IsGenerated(job, prowgen.Generator)
	if err != nil {
		return err
	}
	b[job.Name] = blockedJob{
		current:   true,
		Generated: generated,
		Kind:      getKind(job),
	}
	return nil
}

func (b blockedJobs) Union(other blockedJobs) blockedJobs {
	union := blockedJobs{}
	for k, v := range b {
		union[k] = v
	}
	for k, v := range other {
		union[k] = v
	}
	return union
}

type deprecatedTemplateBlocker struct {
	// unexported field so it is never serialized and it is `false` by default on
	// read. we set this to true when we create new blockers to recognize them
	newlyAdded bool

	Description string      `json:"description"`
	Jobs        blockedJobs `json:"jobs,omitempty"`
}

type deprecatedTemplate struct {
	Name           string                               `json:"template_name"`
	UnknownBlocker *deprecatedTemplateBlocker           `json:"unknown_blocker,omitempty"`
	Blockers       map[string]deprecatedTemplateBlocker `json:"blockers,omitempty"`
}

func (d *deprecatedTemplate) insert(job config.JobBase, defaultBlockers JiraHints) error {
	var knownBlocker bool
	for _, blocker := range d.Blockers {
		if blocker.Jobs.Has(job) {
			// we need to insert to mark the job as 'current' so it is not pruned later
			// we need to do this for each blocker where this job is listed
			if err := blocker.Jobs.Insert(job); err != nil {
				return err
			}
			knownBlocker = true
		}
	}
	if knownBlocker {
		return nil
	}

	if len(defaultBlockers) > 0 && (d.UnknownBlocker == nil || d.UnknownBlocker.Jobs == nil || !d.UnknownBlocker.Jobs.Has(job)) {
		if d.Blockers == nil {
			d.Blockers = map[string]deprecatedTemplateBlocker{}
		}
		for key, description := range defaultBlockers {
			if _, ok := d.Blockers[key]; !ok {
				d.Blockers[key] = deprecatedTemplateBlocker{
					Description: description,
					Jobs:        blockedJobs{},
				}
			}
			if err := d.Blockers[key].Jobs.Insert(job); err != nil {
				return err
			}
		}
		return nil
	}

	if d.UnknownBlocker == nil {
		d.UnknownBlocker = &deprecatedTemplateBlocker{
			newlyAdded: true,
			Jobs:       blockedJobs{},
		}
	} else if d.UnknownBlocker.Jobs == nil {
		d.UnknownBlocker.newlyAdded = true
		d.UnknownBlocker.Jobs = blockedJobs{}
	}
	return d.UnknownBlocker.Jobs.Insert(job)
}

// prune removes all jobs that were not inserted into the allowlist by this
// execution
func (d *deprecatedTemplate) prune() {
	for key, blocker := range d.Blockers {
		for name, job := range blocker.Jobs {
			if !job.current {
				delete(d.Blockers[key].Jobs, name)
			}
		}
		if len(blocker.Jobs) == 0 {
			delete(d.Blockers, key)
		}
	}
	if len(d.Blockers) == 0 {
		d.Blockers = nil
	}

	if d.UnknownBlocker == nil {
		return
	}
	for name, job := range d.UnknownBlocker.Jobs {
		if !job.current {
			delete(d.UnknownBlocker.Jobs, name)
		}
	}
}

type statsLine struct {
	template string
	blocker  string
	total    int

	handcrafted int
	generated   int

	presubmits  int
	postsubmits int
	release     int
	periodics   int
	unknown     int
}

func statsFromJobs(name, blocker string, jobs blockedJobs) statsLine {
	stats := statsLine{
		template: name,
		blocker:  blocker,
	}
	for _, job := range jobs {
		stats.total++

		if job.Generated {
			stats.generated++
		} else {
			stats.handcrafted++
		}

		switch job.Kind {
		case "presubmit":
			stats.presubmits++
		case "postsubmit":
			stats.postsubmits++
		case "release":
			stats.release++
		case "periodic":
			stats.periodics++
		default:
			stats.unknown++
		}
	}

	return stats
}

func (d *deprecatedTemplate) Stats() (total, unknown statsLine, blockers []statsLine) {
	allJobs := blockedJobs{}

	if d.UnknownBlocker != nil {
		unknown = statsFromJobs(d.Name, blockerColUnknown, d.UnknownBlocker.Jobs)
		allJobs = allJobs.Union(d.UnknownBlocker.Jobs)
	}

	for jira, blocker := range d.Blockers {
		blockerStats := statsFromJobs(d.Name, jira, blocker.Jobs)
		blockers = append(blockers, blockerStats)
		allJobs = allJobs.Union(blocker.Jobs)
	}

	sort.Slice(blockers, func(i, j int) bool {
		return blockers[i].blocker < blockers[j].blocker
	})

	total = statsFromJobs(d.Name, blockerColTotal, allJobs)

	return total, unknown, blockers
}

type Allowlist interface {
	Insert(job config.JobBase, template string) error
	Save(path string) error
	Prune()
	GetTemplates() map[string]*deprecatedTemplate
	SetNewJobBlockers(blockers JiraHints)
}

// JiraHints maps JIRA-1234 to (possibly empty) descriptions
type JiraHints map[string]string

type allowlist struct {
	Templates      map[string]*deprecatedTemplate `json:"templates"`
	newJobBlockers JiraHints
}

func (a *allowlist) SetNewJobBlockers(blockers JiraHints) {
	a.newJobBlockers = blockers
}

func (a *allowlist) Prune() {
	for _, template := range a.Templates {
		template.prune()
	}
}

func (a *allowlist) Insert(job config.JobBase, template string) error {
	if a.Templates == nil {
		a.Templates = map[string]*deprecatedTemplate{}
	}

	if _, ok := a.Templates[template]; !ok {
		a.Templates[template] = &deprecatedTemplate{
			Name: template,
			UnknownBlocker: &deprecatedTemplateBlocker{
				Description: blockerColUnknown,
				Jobs:        blockedJobs{},
			},
		}
	}

	return a.Templates[template].insert(job, a.newJobBlockers)
}

func loadAllowlist(allowlistPath string) (Allowlist, error) {
	var allowlist allowlist

	raw, err := gzip.ReadFileMaybeGZIP(allowlistPath)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}

	if err == nil {
		return &allowlist, yaml.Unmarshal(raw, &allowlist)
	}

	logrus.Warn("template deprecation allowlist does not exist, will populate a new one")
	return &allowlist, nil
}

func (a allowlist) Save(path string) error {
	raw, err := yaml.Marshal(a)
	if err != nil {
		return err
	}

	if err := os.WriteFile(path, raw, 0644); err != nil {
		return err
	}

	return nil
}

func (a *allowlist) GetTemplates() map[string]*deprecatedTemplate {
	t := map[string]*deprecatedTemplate{}
	for k, v := range a.Templates {
		t[k] = v
	}
	return t
}
