package jobrunaggregatorlib

// TestSuitesSeparator defines the separator to use when combine multiple level of suite names
var TestSuitesSeparator = "|||"

type TestCaseDetails struct {
	Name          string
	TestSuiteName string
	// Summary is filled in during the pass/fail calculation
	Summary string

	Passes   []TestCasePass
	Failures []TestCaseFailure
	Skips    []TestCaseSkip

	Unfinished    []TestCaseIncomplete `yaml:"unfinished,omitempty"`
	MissingJUnits []TestCaseIncomplete `yaml:"missingJUnits,omitempty"`
}

type TestCasePass struct {
	JobRunID       string
	HumanURL       string
	GCSArtifactURL string
}

type TestCaseFailure struct {
	JobRunID       string
	HumanURL       string
	GCSArtifactURL string
}

type TestCaseSkip struct {
	JobRunID       string
	HumanURL       string
	GCSArtifactURL string
}

type TestCaseIncomplete struct {
	JobRunID       string
	HumanURL       string
	GCSArtifactURL string
}

// TestCaseNeverExecuted is retained for compatibility with consumers of this package.
type TestCaseNeverExecuted struct {
	JobRunID       string
	HumanURL       string
	GCSArtifactURL string
}
