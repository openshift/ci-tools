package bumper

import (
	"github.com/openshift/ci-tools/pkg/api"
)

type ReleaseConfig struct {
	Name                       string                         `json:"name,omitempty"`
	To                         string                         `json:"to,omitempty"`
	Message                    string                         `json:"message,omitempty"`
	MirrorPrefix               string                         `json:"mirrorPrefix,omitempty"`
	Expires                    string                         `json:"expires,omitempty"`
	MaxUnreadyReleases         int                            `json:"maxUnreadyReleases,omitempty"`
	MinCreationIntervalSeconds int                            `json:"minCreationIntervalSeconds,omitempty"`
	ReferenceMode              string                         `json:"referenceMode,omitempty"`
	PullSecretName             string                         `json:"pullSecretName,omitempty"`
	Hide                       bool                           `json:"hide,omitempty"`
	EndOfLife                  bool                           `json:"endOfLife,omitempty"`
	As                         string                         `json:"as,omitempty"`
	OverrideCLIImage           string                         `json:"overrideCLIImage,omitempty"`
	Check                      map[string]ReleaseCheck        `json:"check"`
	Publish                    map[string]ReleasePublish      `json:"publish"`
	Verify                     map[string]ReleaseVerification `json:"verify"`
	Periodic                   map[string]ReleasePeriodic     `json:"periodic,omitempty"`
}

type CheckConsistentImages struct {
	Parent string `json:"parent,omitempty"`
}

type ReleaseCheck struct {
	ConsistentImages *CheckConsistentImages `json:"consistentImages,omitempty"`
}

type PublishTagReference struct {
	Name string `json:"name,omitempty"`
}

type PublishStreamReference struct {
	Name        string   `json:"name,omitempty"`
	Namespace   string   `json:"namespace,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	ExcludeTags []string `json:"excludeTags,omitempty"`
}

type VerifyBugsTagInfo struct {
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name,omitempty"`
	Tag       string `json:"tag,omitempty"`
}

type PublishVerifyBugs struct {
	PreviousReleaseTag *VerifyBugsTagInfo `json:"previousReleaseTag,omitempty"`
}

type ReleasePublish struct {
	Disabled       bool                    `json:"disabled,omitempty"`
	TagRef         *PublishTagReference    `json:"tagRef,omitempty"`
	ImageStreamRef *PublishStreamReference `json:"imageStreamRef,omitempty"`
	VerifyBugs     *PublishVerifyBugs      `json:"verifyBugs,omitempty"`
}

type ReleasePeriodic struct {
	Interval           string               `json:"interval,omitempty"`
	Cron               string               `json:"cron,omitempty"`
	Upgrade            bool                 `json:"upgrade,omitempty"`
	UpgradeFrom        string               `json:"upgradeFrom,omitempty"`
	UpgradeFromRelease *UpgradeRelease      `json:"upgradeFromRelease,omitempty"`
	ProwJob            *ProwJobVerification `json:"prowJob,omitempty"`
}

type UpgradeVersionBounds struct {
	Lower string `json:"lower,omitempty"`
	Upper string `json:"upper,omitempty"`
}

type UpgradePrerelease struct {
	VersionBounds UpgradeVersionBounds `json:"version_bounds,omitempty"`
}

type UpgradeCandidate struct {
	Stream   string `json:"stream,omitempty"`
	Version  string `json:"version,omitempty"`
	Relative int    `json:"relative,omitempty"`
}

type UpgradeRelease struct {
	Candidate  *UpgradeCandidate  `json:"candidate,omitempty"`
	Prerelease *UpgradePrerelease `json:"prerelease,omitempty"`
	Official   *api.Release       `json:"release,omitempty"`
}

type AggregatedProwJobVerification struct {
	ProwJob          *ProwJobVerification `json:"prowJob,omitempty"`
	AnalysisJobCount int                  `json:"analysisJobCount,omitempty"`
}

type ReleaseVerification struct {
	MaxRetries         int                            `json:"maxRetries,omitempty"`
	Optional           bool                           `json:"optional,omitempty"`
	ProwJob            *ProwJobVerification           `json:"prowJob,omitempty"`
	Disabled           bool                           `json:"disabled,omitempty"`
	Upgrade            bool                           `json:"upgrade,omitempty"`
	UpgradeFrom        string                         `json:"upgradeFrom,omitempty"`
	UpgradeFromRelease *UpgradeRelease                `json:"upgradeFromRelease,omitempty"`
	AggregatedProwJob  *AggregatedProwJobVerification `json:"aggregatedProwJob,omitempty"`
}

type ProwJobVerification struct {
	Name string `json:"name,omitempty"`
}
