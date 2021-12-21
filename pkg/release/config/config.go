package config

import (
	"github.com/openshift/ci-tools/pkg/api"
)

// Config is a subset of fields from the release controller's config
type Config struct {
	Name    string                `json:"name,omitempty"`
	Publish Publish               `json:"publish"`
	Verify  map[string]VerifyItem `json:"verify,omitempty"`
}

type Publish struct {
	MirrorToOrigin MirrorToOrigin `json:"mirror-to-origin"`
}

type MirrorToOrigin struct {
	ImageStreamRef ImageStreamRef `json:"imageStreamRef"`
}

type ImageStreamRef struct {
	Namespace   string   `json:"namespace"`
	Name        string   `json:"name"`
	ExcludeTags []string `json:"excludeTags,omitempty"`
}

type Job struct {
	Name                 string            `json:"name"`
	Annotations          map[string]string `json:"annotations"`
	api.MetadataWithTest `json:",inline"`

	AggregatedCount int `json:"-"`
}

type AggregatedJob struct {
	ProwJob          *Job `json:"prowJob,omitempty"`
	AnalysisJobCount int  `json:"analysisJobCount"`
}

type VerifyItem struct {
	Optional          bool           `json:"optional"`
	Upgrade           bool           `json:"upgrade"`
	ProwJob           Job            `json:"prowJob"`
	AggregatedProwJob *AggregatedJob `json:"aggregatedProwJob,omitempty"`
}
