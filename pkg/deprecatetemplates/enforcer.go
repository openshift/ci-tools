package deprecatetemplates

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"k8s.io/apimachinery/pkg/util/sets"
	prowconfig "k8s.io/test-infra/prow/config"
	prowplugins "k8s.io/test-infra/prow/plugins"

	"github.com/openshift/ci-tools/pkg/rehearse"
)

// Enforcer manages all necessary data to decide if the state of
// openshift/release is valid (and therefore, if a PR to openshift/release
// does not diverge it from the valid state)
type Enforcer struct {
	existingTemplates sets.Set[string]
	allowlist         Allowlist
}

// NewEnforcer initializes a new enforcer instance. The enforcer will be
// initialized with an allowlist from the given location. If the allowlist
// does not exist, the enforcer will have an empty allowlist.
func NewEnforcer(allowlistPath string, newJobBlockers JiraHints) (*Enforcer, error) {
	allowlist, err := loadAllowlist(allowlistPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load template deprecating allowlist from %q: %w", allowlistPath, err)
	}
	allowlist.SetNewJobBlockers(newJobBlockers)

	return &Enforcer{
		allowlist:         allowlist,
		existingTemplates: sets.New[string](),
	}, nil
}

// LoadTemplates detects all existing templates from config updater configuration
func (e *Enforcer) LoadTemplates(cuCfg prowplugins.ConfigUpdater) {
	templates := sets.New[string]()
	templatePathPrefix := "ci-operator/templates/"
	for pattern, cmSpec := range cuCfg.Maps {
		if strings.HasPrefix(pattern, templatePathPrefix) {
			templates.Insert(cmSpec.Name)
		}
	}

	e.existingTemplates = templates
}

type jobconfig interface {
	AllStaticPostsubmits(repos []string) []prowconfig.Postsubmit
	AllStaticPresubmits(repos []string) []prowconfig.Presubmit
	AllPeriodics() []prowconfig.Periodic
}

// ProcessJobs reads all existing Prow jobs and makes sure all jobs that use
// one of the existing templates are present in the allowlist.
func (e *Enforcer) ProcessJobs(jobConfig jobconfig) error {

	for _, job := range jobConfig.AllStaticPresubmits(nil) {
		if err := e.ingest(job.JobBase); err != nil {
			return err
		}
	}

	for _, job := range jobConfig.AllStaticPostsubmits(nil) {
		if err := e.ingest(job.JobBase); err != nil {
			return err
		}
	}

	for _, job := range jobConfig.AllPeriodics() {
		if err := e.ingest(job.JobBase); err != nil {
			return err
		}
	}

	return nil
}

func (e *Enforcer) ingest(job prowconfig.JobBase) error {
	for template := range e.existingTemplates {
		if rehearse.UsesConfigMap(job, template) {
			if err := e.allowlist.Insert(job, template); err != nil {
				return err
			}
		}
	}
	return nil
}

// SaveAllowlist dumps the allowlist to the given location
func (e *Enforcer) SaveAllowlist(path string) error {
	return e.allowlist.Save(path)
}

func (e *Enforcer) Prune() {
	e.allowlist.Prune()
}

func asLine(stats statsLine) []string {
	return []string{
		stats.template,
		stats.blocker,
		strconv.Itoa(stats.total),
		strconv.Itoa(stats.generated),
		strconv.Itoa(stats.handcrafted),
		strconv.Itoa(stats.presubmits),
		strconv.Itoa(stats.postsubmits),
		strconv.Itoa(stats.release),
		strconv.Itoa(stats.periodics),
		strconv.Itoa(stats.unknown),
	}
}

func blockerSortKey(blocker string) string {
	// this determines sort order in stats when the numerical data are equal
	// general idea is to have `total` always at the bottom, `unknown` above it
	// and the actual blockers above it, sorted by the key
	switch {
	case blocker == blockerColTotal:
		// pls dont laugh at me too hard
		return "zzzzzzzzzz"
	case blocker == blockerColUnknown:
		return "yzzzzzzzzz"
	}
	return blocker
}

func (e *Enforcer) Stats(hideTotals bool) (header, footer []string, lines [][]string) {
	header = []string{"Template", "Blocker", "Total", "Generated", "Handcrafted", "Presubmits", "Postsubmits", "Release", "Periodics", "Unknown"}
	var data []statsLine
	var sumTotal int
	var sumGenerated int
	var sumHandcrafted int
	var sumPre int
	var sumPost int
	var sumRelease int
	var sumPeriodics int
	var sumUnknown int

	totals := map[string]int{}
	templates := e.allowlist.GetTemplates()
	for name, template := range templates {
		total, unknown, blockers := template.Stats()
		if !(name == blockerColTotal && hideTotals) {
			totals[name] = total.total
		}
		if !hideTotals {
			data = append(data, total)
		}
		if unknown.total != 0 {
			data = append(data, unknown)
		}
		data = append(data, blockers...)

		sumTotal += total.total
		sumGenerated += total.generated
		sumHandcrafted += total.handcrafted
		sumPre += total.presubmits
		sumPost += total.postsubmits
		sumRelease += total.release
		sumPeriodics += total.periodics
		sumUnknown += total.unknown
	}

	sort.Slice(data, func(i, j int) bool {
		switch {
		// Primary sort by total jobs using this template
		case totals[data[i].template] < totals[data[j].template]:
			return true
		case totals[data[j].template] < totals[data[i].template]:
			return false
		// Secondary sort by # of jobs using this template for a given blocker
		case data[i].total < data[j].total:
			return true
		case data[j].total < data[i].total:
			return false
		// Tertiary sort by template name
		case data[i].template < data[j].template:
			return true
		case data[j].template < data[i].template:
			return false
		// Last sort by blocker name
		case blockerSortKey(data[i].blocker) < blockerSortKey(data[j].blocker):
			return true
		case blockerSortKey(data[j].blocker) < blockerSortKey(data[i].blocker):
			return false
		}
		return false
	})

	for _, item := range data {
		lines = append(lines, asLine(item))
	}

	footer = []string{
		fmt.Sprintf("%d templates", len(templates)),
		"Total",
		strconv.Itoa(sumTotal),
		strconv.Itoa(sumGenerated),
		strconv.Itoa(sumHandcrafted),
		strconv.Itoa(sumPre),
		strconv.Itoa(sumPost),
		strconv.Itoa(sumRelease),
		strconv.Itoa(sumPeriodics),
		strconv.Itoa(sumUnknown),
	}
	return header, footer, lines
}

type enforcingFunc func() []error

func (e *Enforcer) noNewUnknownBlockers() []error {
	makeError := func(template string, blockers map[string]deprecatedTemplateBlocker) error {
		lines := []string{fmt.Sprintf(`Jobs using the '%s' template were added with an
unknown blocker. Add them under one of existing blockers by running one of the following:`, template)}
		for id, blocker := range blockers {
			lines = append(lines, fmt.Sprintf("$ make template-allowlist BLOCKER=%s # %s", id, blocker.Description))
		}
		lines = append(lines, "", `Alternatively, create a new JIRA and start tracking it in the allowlist:
$ make template-allowlist BLOCKER="JIRAID:short description"`)

		return errors.New(strings.Join(lines, "\n"))
	}

	var errs []error
	if e.allowlist == nil {
		return errs
	}

	for name, record := range e.allowlist.GetTemplates() {
		if record.UnknownBlocker != nil && record.UnknownBlocker.newlyAdded {
			errs = append(errs, makeError(name, record.Blockers))
		}
	}
	return errs
}

func (e *Enforcer) noUnusedTemplates() []error {
	var unused []string
	configured := e.existingTemplates
	used := e.allowlist.GetTemplates()
	for template := range configured {
		if _, ok := used[template]; !ok {
			unused = append(unused, template)
		}
	}

	if len(unused) == 0 {
		return nil
	}
	lines := []string{`The following templates are not used by any job. Please remove their
config-updater config from core-services/prow/02_config/_plugins.yaml)
and code from ci-operator/templates. If you are trying to add a new template,
you should add multi-stage steps instead.`}
	for _, line := range unused {
		lines = append(lines, fmt.Sprintf("- %s", line))
	}

	return []error{errors.New(strings.Join(lines, "\n"))}
}

func (e *Enforcer) Validate() []string {
	checks := []enforcingFunc{
		e.noUnusedTemplates,
		e.noNewUnknownBlockers,
	}
	var violations []string
	for _, check := range checks {
		if errs := check(); len(errs) > 0 {
			for _, err := range errs {
				violations = append(violations, err.Error())
			}
		}
	}

	return violations
}
