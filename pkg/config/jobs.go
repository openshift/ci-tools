package config

import (
	prowconfig "k8s.io/test-infra/prow/config"
)

var (
	SourceTypeLabel string = "pj-rehearse.openshift.io/source-type"

	ChangedPresubmit       SourceType = "changedPresubmit"
	ChangedPeriodic        SourceType = "changedPeriodic"
	ChangedCiopConfig      SourceType = "changedCiopConfig"
	ChangedClusterProfile  SourceType = "changedClusterProfile"
	ChangedTemplate        SourceType = "changedTemplate"
	ChangedRegistryContent SourceType = "changedRegistryContent"
	Unknown                SourceType = "unknownSource"
)

type SourceType string

func GetSourceType(labels map[string]string) SourceType {
	sourceType, ok := labels[SourceTypeLabel]
	if !ok {
		return Unknown
	}

	switch sourceType {
	case "changedPresubmit":
		return ChangedPresubmit
	case "changedPeriodic":
		return ChangedPeriodic
	case "changedCiopConfig":
		return ChangedCiopConfig
	case "changedClusterProfile":
		return ChangedClusterProfile
	case "changedTemplate":
		return ChangedTemplate
	case "changedRegistryContent":
		return ChangedRegistryContent
	default:
		return Unknown
	}
}

func (sourceType SourceType) GetDisplayText() string {
	switch sourceType {
	case ChangedPresubmit:
		return "Presubmit changed"
	case ChangedPeriodic:
		return "Periodic changed"
	case ChangedCiopConfig:
		return "Ci-operator config changed"
	case ChangedClusterProfile:
		return "Cluster Profile changed"
	case ChangedTemplate:
		return "Template changed"
	case ChangedRegistryContent:
		return "Registry content changed"
	default:
		return "Unknown change occurred"
	}
}

type Presubmits map[string][]prowconfig.Presubmit

// AddAll adds all jobs from a different instance.
// The method assumes two jobs with a matching name for the same repository
// are identical, so if a presubmit with a given name already exists, it
// is kept as is.
func (p Presubmits) AddAll(jobs Presubmits, sourceType SourceType) {
	for repo := range jobs {
		if _, ok := p[repo]; !ok {
			p[repo] = []prowconfig.Presubmit{}
		}

		for _, sourceJob := range jobs[repo] {
			p.Add(repo, sourceJob, sourceType)
		}
	}
}

// Add a presubmit for a given repo.
// The method assumes two jobs with a matching name are identical, so if
// a presubmit with a given name already exists, it is kept as is.
func (p Presubmits) Add(repo string, job prowconfig.Presubmit, sourceType SourceType) {
	for _, destJob := range p[repo] {
		if destJob.Name == job.Name {
			return
		}
	}

	if len(job.Labels) == 0 {
		job.Labels = make(map[string]string)
	}

	if _, ok := job.Labels[SourceTypeLabel]; !ok {
		job.Labels[SourceTypeLabel] = string(sourceType)
	}
	p[repo] = append(p[repo], job)
}

type Periodics map[string]prowconfig.Periodic

// AddAll adds all jobs from a different instance.
// The method assumes two jobs with a matching name are identical,
// so if a periodic with a given name already exists, it
// is overridden.
func (p Periodics) AddAll(jobs Periodics, sourceType SourceType) {
	for _, job := range jobs {
		p.Add(job, sourceType)
	}
}

// Add adds a job from a different instance.
// The method assumes two jobs with a matching name are identical,
// so if a periodic with a given name already exists, it
// is overridden.
func (p Periodics) Add(job prowconfig.Periodic, sourceType SourceType) {
	if _, ok := p[job.Name]; ok {
		return
	}
	if len(job.Labels) == 0 {
		job.Labels = make(map[string]string)
	}
	if _, ok := job.Labels[SourceTypeLabel]; !ok {
		job.Labels[SourceTypeLabel] = string(sourceType)
	}
	p[job.Name] = job
}
