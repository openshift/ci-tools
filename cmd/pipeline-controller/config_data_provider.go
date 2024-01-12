package main

import (
	"sync"
	"time"

	"k8s.io/test-infra/prow/config"
)

type presubmitTests struct {
	protected                     []string
	alwaysRequired                []string
	conditionallyRequired         []string
	pipelineConditionallyRequired []config.Presubmit
}

type ConfigDataProvider struct {
	configGetter      config.Getter
	updatedPresubmits map[string]presubmitTests
	m                 sync.Mutex
}

func NewConfigDataProvider(configGetter config.Getter) *ConfigDataProvider {
	provider := &ConfigDataProvider{
		configGetter:      configGetter,
		updatedPresubmits: make(map[string]presubmitTests),
		m:                 sync.Mutex{},
	}
	provider.gatherData()
	return provider
}

func (c *ConfigDataProvider) GetPresubmits(orgRepo string) presubmitTests {
	c.m.Lock()
	defer c.m.Unlock()
	if presubmits, ok := c.updatedPresubmits[orgRepo]; ok {
		return presubmits
	}
	return presubmitTests{}
}

func (c *ConfigDataProvider) Run() {
	for {
		time.Sleep(10 * time.Minute)
		c.gatherData()
	}
}

func (c *ConfigDataProvider) gatherData() {
	cfg := c.configGetter()
	orgRepos := []string{}
	for org, orgPolicy := range cfg.ProwConfig.BranchProtection.Orgs {
		for repo, repoPolicy := range cfg.ProwConfig.BranchProtection.Orgs[org].Repos {
			policy := orgPolicy.Apply(repoPolicy.Policy)
			if policy.RequireManuallyTriggeredJobs != nil && *policy.RequireManuallyTriggeredJobs {
				orgRepos = append(orgRepos, org+"/"+repo)
			}
		}
	}
	updatedPresubmits := make(map[string]presubmitTests)
	for _, orgRepo := range orgRepos {
		presubmits := cfg.GetPresubmitsStatic(orgRepo)
		for _, p := range presubmits {
			if !p.AlwaysRun && p.RunIfChanged == "" && p.SkipIfOnlyChanged == "" {
				if val, ok := p.Annotations["pipeline_run_if_changed"]; ok && val != "" {
					if pre, ok := updatedPresubmits[orgRepo]; !ok {
						updatedPresubmits[orgRepo] = presubmitTests{pipelineConditionallyRequired: []config.Presubmit{p}}
					} else {
						pre.pipelineConditionallyRequired = append(pre.pipelineConditionallyRequired, p)
						updatedPresubmits[orgRepo] = pre
					}
					continue
				}
				if !p.Optional {
					if pre, ok := updatedPresubmits[orgRepo]; !ok {
						updatedPresubmits[orgRepo] = presubmitTests{protected: []string{p.Name}}
					} else {
						pre.protected = append(pre.protected, p.Name)
						updatedPresubmits[orgRepo] = pre
					}
					continue
				}
			}
			if !p.Optional && p.AlwaysRun {
				if pre, ok := updatedPresubmits[orgRepo]; !ok {
					updatedPresubmits[orgRepo] = presubmitTests{alwaysRequired: []string{p.Name}}
				} else {
					pre.alwaysRequired = append(pre.alwaysRequired, p.Name)
					updatedPresubmits[orgRepo] = pre
				}
				continue
			}
			if !p.Optional && (p.RunIfChanged != "" || p.SkipIfOnlyChanged != "") {
				if pre, ok := updatedPresubmits[orgRepo]; !ok {
					updatedPresubmits[orgRepo] = presubmitTests{conditionallyRequired: []string{p.Name}}
				} else {
					pre.conditionallyRequired = append(pre.conditionallyRequired, p.Name)
					updatedPresubmits[orgRepo] = pre
				}
				continue
			}
		}
	}
	c.m.Lock()
	defer c.m.Unlock()
	c.updatedPresubmits = updatedPresubmits
}
