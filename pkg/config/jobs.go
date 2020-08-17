package config

import (
	prowconfig "k8s.io/test-infra/prow/config"
)

type Presubmits map[string][]prowconfig.Presubmit

// AddAll adds all jobs from a different instance.
// The method assumes two jobs with a matching name for the same repository
// are identical, so if a presubmit with a given name already exists, it
// is kept as is.
func (p Presubmits) AddAll(jobs Presubmits) {
	for repo := range jobs {
		if _, ok := p[repo]; !ok {
			p[repo] = []prowconfig.Presubmit{}
		}

		for _, sourceJob := range jobs[repo] {
			p.Add(repo, sourceJob)
		}
	}
}

// Add a presubmit for a given repo.
// The method assumes two jobs with a matching name are identical, so if
// a presubmit with a given name already exists, it is kept as is.
func (p Presubmits) Add(repo string, job prowconfig.Presubmit) {
	for _, destJob := range p[repo] {
		if destJob.Name == job.Name {
			return
		}
	}

	p[repo] = append(p[repo], job)
}

type Periodics map[string]prowconfig.Periodic

// AddAll adds all jobs from a different instance.
// The method assumes two jobs with a matching name are identical,
// so if a periodic with a given name already exists, it
// is overridden.
func (p Periodics) AddAll(jobs Periodics) {
	for name, periodic := range jobs {
		p[name] = periodic
	}
}

// Add adds a job from a different instance.
// The method assumes two jobs with a matching name are identical,
// so if a periodic with a given name already exists, it
// is overridden.
func (p Periodics) Add(job prowconfig.Periodic) {
	p[job.Name] = job
}
