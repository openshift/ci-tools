package deprecatetemplates

import (
	"fmt"
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
	existingTemplates sets.String
	allowlist         Allowlist
}

// NewEnforcer initializes a new enforcer instance. The enforcer will be
// initialized with an allowlist from the given location. If the allowlist
// does not exist, the enforcer will have an empty allowlist.
func NewEnforcer(allowlistPath string) (*Enforcer, error) {
	allowlist, err := loadAllowlist(allowlistPath)
	if err != nil {
		return nil, fmt.Errorf("Failed to load template deprecating allowlist from %q", allowlistPath)
	}
	return &Enforcer{
		allowlist:         allowlist,
		existingTemplates: sets.NewString(),
	}, nil
}

// LoadTemplates detects all existing templates from config updater configuration
func (e *Enforcer) LoadTemplates(cuCfg prowplugins.ConfigUpdater) {
	templates := sets.NewString()
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
func (e *Enforcer) ProcessJobs(jobConfig jobconfig) {

	for _, job := range jobConfig.AllStaticPresubmits(nil) {
		e.ingest(job.JobBase)
	}

	for _, job := range jobConfig.AllStaticPostsubmits(nil) {
		e.ingest(job.JobBase)
	}

	for _, job := range jobConfig.AllPeriodics() {
		e.ingest(job.JobBase)
	}

}

func (e *Enforcer) ingest(job prowconfig.JobBase) {
	for template := range e.existingTemplates {
		if rehearse.UsesConfigMap(job, template) {
			e.allowlist.Insert(job, template)
		}
	}
}

// SaveAllowlist dumps the allowlist to the given location
func (e *Enforcer) SaveAllowlist(path string) error {
	return e.allowlist.Save(path)
}

func (e *Enforcer) Prune() {
	e.allowlist.Prune()
}
