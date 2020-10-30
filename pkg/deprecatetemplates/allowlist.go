package deprecatetemplates

import (
	"io/ioutil"
	"os"
	"strings"

	"github.com/ghodss/yaml"
	"github.com/sirupsen/logrus"

	prowv1 "k8s.io/test-infra/prow/apis/prowjobs/v1"
	"k8s.io/test-infra/prow/config"

	jc "github.com/openshift/ci-tools/pkg/jobconfig"
)

const (
	release = "release"
	unknown = "unknown"
)

func isGenerated(job config.JobBase) bool {
	_, generated := job.Labels[jc.ProwJobLabelGenerated]
	return generated
}

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
	Generated bool   `json:"generated"`
	Kind      string `json:"kind"`
}

type blockedJobs map[string]blockedJob

func (b blockedJobs) Has(job config.JobBase) bool {
	_, has := b[job.Name]
	return has
}

func (b blockedJobs) Insert(job config.JobBase) {
	b[job.Name] = blockedJob{
		Generated: isGenerated(job),
		Kind:      getKind(job),
	}
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
	Description string      `json:"description"`
	Jobs        blockedJobs `json:"jobs"`
}

type deprecatedTemplate struct {
	Name           string                               `json:"template_name"`
	UnknownBlocker deprecatedTemplateBlocker            `json:"unknown_blocker"`
	Blockers       map[string]deprecatedTemplateBlocker `json:"blockers,omitempty"`
}

func (d deprecatedTemplate) insert(job config.JobBase) {
	for _, blocker := range d.Blockers {
		if blocker.Jobs.Has(job) {
			return
		}
	}
	d.UnknownBlocker.Jobs.Insert(job)
}

type Allowlist interface {
	Insert(job config.JobBase, template string)
	Save(path string) error
}

type allowlist struct {
	Templates map[string]deprecatedTemplate `json:"templates"`
}

func (a *allowlist) Insert(job config.JobBase, template string) {
	if a.Templates == nil {
		a.Templates = map[string]deprecatedTemplate{}
	}

	if _, ok := a.Templates[template]; !ok {
		a.Templates[template] = deprecatedTemplate{
			Name: template,
			UnknownBlocker: deprecatedTemplateBlocker{
				Description: "unknown",
				Jobs:        blockedJobs{},
			},
		}
	}

	a.Templates[template].insert(job)
}

func loadAllowlist(allowlistPath string) (Allowlist, error) {
	var allowlist allowlist

	raw, err := ioutil.ReadFile(allowlistPath)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}

	if err == nil {
		err := yaml.Unmarshal(raw, &allowlist)
		return &allowlist, err
	}

	logrus.Warn("template deprecation allowlist does not exist, will populate a new one")
	return &allowlist, nil
}

func (a allowlist) Save(path string) error {
	raw, err := yaml.Marshal(a)
	if err != nil {
		return err
	}

	if err := ioutil.WriteFile(path, raw, 0644); err != nil {
		return err
	}

	return nil
}
