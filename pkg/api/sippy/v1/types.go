package v1

type SippyConfig struct {
	Prow     ProwConfig               `yaml:"prow"`
	Releases map[string]ReleaseConfig `yaml:"releases"`
}

type ProwConfig struct {
	// URL to the prowjob.js endpoint of the prow instance.
	URL string `yaml:"url"`
}

type ReleaseConfig struct {
	// Jobs is a set of jobs that should be considered part of the release.
	Jobs map[string]bool `yaml:"jobs,omitempty"`

	// Regexp is a list of regular expressions that match a job to a release.
	Regexp []string `yaml:"regexp,omitempty"`
}
