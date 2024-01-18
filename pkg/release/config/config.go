package config

import (
	"fmt"
	"regexp"
	"strconv"

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

// AdditionalPR is a formatted string that takes the form "org/repo#number"
type AdditionalPR string

const additionalPRPattern = `^([\w-]+)/([\w-]+)#(\d+)$`

func (pr AdditionalPR) GetOrgRepoAndNumber() (string, string, int, error) {
	re := regexp.MustCompile(additionalPRPattern)
	matches := re.FindStringSubmatch(string(pr))
	if len(matches) == 4 {
		org := matches[1]
		repo := matches[2]
		number, err := strconv.Atoi(matches[3])
		if err != nil {
			return "", "", 0, fmt.Errorf("unable to get additional pr number from string: %s: %w", pr, err)
		}
		return org, repo, number, nil
	} else {
		return "", "", 0, fmt.Errorf("string: %s doesn't match expected format: org/repo#number", pr)
	}
}

type Job struct {
	Name                 string            `json:"name"`
	Annotations          map[string]string `json:"annotations"`
	api.MetadataWithTest `json:",inline"`

	WithPRs         []AdditionalPR `json:"with-prs"`
	AggregatedCount int            `json:"-"`
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
