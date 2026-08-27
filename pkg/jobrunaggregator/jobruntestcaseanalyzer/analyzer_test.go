package jobruntestcaseanalyzer

import (
	"context"
	"errors"
	"testing"

	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"gopkg.in/yaml.v2"

	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorapi"
	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorlib"
	"github.com/openshift/ci-tools/pkg/junit"
)

func TestGetJobs(t *testing.T) {

	tests := map[string]struct {
		expectedJobNames sets.Set[string]
		filters          map[string][]string
	}{
		"test upgrade filter":   {expectedJobNames: sets.Set[string]{"periodic-ci-openshift-release-master-nightly-4.12-e2e-metal-ipi-sdn-serial-ipv4": sets.Empty{}, "periodic-ci-openshift-release-master-nightly-4.12-e2e-metal-ipi-serial-ovn-ipv6": sets.Empty{}}, filters: map[string][]string{"exclude-job-names": {"upgrade"}}},
		"test no filter":        {expectedJobNames: sets.Set[string]{"periodic-ci-openshift-release-master-nightly-4.12-e2e-metal-ipi-sdn-serial-ipv4": sets.Empty{}, "periodic-ci-openshift-release-master-nightly-4.12-e2e-metal-ipi-serial-ovn-ipv6": sets.Empty{}, "periodic-ci-openshift-release-master-nightly-4.12-e2e-metal-ipi-sdn-upgrade": sets.Empty{}}},
		"test multiple filters": {expectedJobNames: sets.Set[string]{"periodic-ci-openshift-release-master-nightly-4.12-e2e-metal-ipi-sdn-serial-ipv4": sets.Empty{}}, filters: map[string][]string{"exclude-job-names": {"upgrade", "ipv6"}}},
		"test include arg":      {expectedJobNames: sets.Set[string]{"periodic-ci-openshift-release-master-nightly-4.12-e2e-metal-ipi-serial-ovn-ipv6": sets.Empty{}}, filters: map[string][]string{"include-job-names": {"ipv6"}}},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {

			ctx := context.TODO()
			mockCtrl := gomock.NewController(t)
			defer mockCtrl.Finish()

			mockCIDataClient := jobrunaggregatorlib.NewMockCIDataClient(mockCtrl)
			mockCIDataClient.EXPECT().ListAllJobsWithVariants(ctx).Return(createJobs(), nil)

			jobGetter := &testCaseAnalyzerJobGetter{
				platform:       "metal",
				infrastructure: "ipi",
				network:        "sdn",
				ciDataClient:   mockCIDataClient,
				jobGCSPrefixes: &[]jobGCSPrefix{},
			}

			fs := pflag.NewFlagSet(t.Name(), pflag.ContinueOnError)
			args := make([]string, 0)

			for key, element := range tc.filters {
				for _, value := range element {
					args = append(args, "--"+key+"="+value)
				}
			}

			f := &JobRunsTestCaseAnalyzerFlags{}

			fs.StringArrayVar(&f.ExcludeJobNames, "exclude-job-names", f.ExcludeJobNames, "Applied only when --explicit-gcs-prefixes is not specified.  The flag can be specified multiple times to create a list of substrings used to filter JobNames from the analysis")
			fs.StringArrayVar(&f.IncludeJobNames, "include-job-names", f.IncludeJobNames, "Applied only when --explicit-gcs-prefixes is not specified.  The flag can be specified multiple times to create a list of substrings to include in matching JobNames for analysis")

			if err := fs.Parse(args); err != nil {
				t.Fatalf("%s flag set parse returned error %#v", name, err)
			}

			if len(f.ExcludeJobNames) > 0 {
				jobGetter.excludeJobNames = sets.Set[string]{}
				jobGetter.excludeJobNames.Insert(f.ExcludeJobNames...)
			}

			if len(f.IncludeJobNames) > 0 {
				jobGetter.includeJobNames = sets.Set[string]{}
				jobGetter.includeJobNames.Insert(f.IncludeJobNames...)
			}

			returnedJobs, err := jobGetter.GetJobs(ctx)

			if nil != err {
				t.Fatalf("%s returned error %#v", name, err)
			}

			if len(returnedJobs) == 0 {
				t.Fatalf("%s returned nil jobs", name)
			}

			for key := range tc.expectedJobNames {
				foundIt := false

				for _, job := range returnedJobs {

					if key == job.JobName {
						foundIt = true
					}
				}

				if !foundIt {
					t.Fatalf("%s expected job name '%s' not found", name, key)
				}

			}
		})
	}

}

func createJobs() []jobrunaggregatorapi.JobRowWithVariants {
	jobs := make([]jobrunaggregatorapi.JobRowWithVariants, 3)
	jobs[0] = jobrunaggregatorapi.JobRowWithVariants{JobName: "periodic-ci-openshift-release-master-nightly-4.12-e2e-metal-ipi-sdn-upgrade", Platform: "metal", Network: "sdn"}
	jobs[1] = jobrunaggregatorapi.JobRowWithVariants{JobName: "periodic-ci-openshift-release-master-nightly-4.12-e2e-metal-ipi-sdn-serial-ipv4", Platform: "metal", Network: "sdn"}
	jobs[2] = jobrunaggregatorapi.JobRowWithVariants{JobName: "periodic-ci-openshift-release-master-nightly-4.12-e2e-metal-ipi-serial-ovn-ipv6", Platform: "metal", Network: "sdn"}

	return jobs
}

type analyzerTestJobRun struct {
	jobrunaggregatorapi.JobRunInfo
	id          string
	humanURL    string
	artifactURL string
	testSuites  *junit.TestSuites
	err         error
	events      *[]string
}

func (j *analyzerTestJobRun) GetJobRunID() string       { return j.id }
func (j *analyzerTestJobRun) GetHumanURL() string       { return j.humanURL }
func (j *analyzerTestJobRun) GetGCSArtifactURL() string { return j.artifactURL }

func (j *analyzerTestJobRun) GetCombinedJUnitTestSuites(context.Context) (*junit.TestSuites, error) {
	if j.events != nil {
		*j.events = append(*j.events, "get:"+j.id)
	}
	return j.testSuites, j.err
}

func (j *analyzerTestJobRun) ClearAllContent() {
	if j.events != nil {
		*j.events = append(*j.events, "clear:"+j.id)
	}
}

func nestedTestSuites(id testIdentifier, failure bool) *junit.TestSuites {
	testCase := &junit.TestCase{Name: id.testName}
	if failure {
		testCase.FailureOutput = &junit.FailureOutput{Message: "failed"}
	}

	child := &junit.TestSuite{Name: id.testSuites[len(id.testSuites)-1], TestCases: []*junit.TestCase{testCase}}
	for i := len(id.testSuites) - 2; i >= 0; i-- {
		child = &junit.TestSuite{Name: id.testSuites[i], Children: []*junit.TestSuite{child}}
	}
	return &junit.TestSuites{Suites: []*junit.TestSuite{child}}
}

func TestMinimumRequiredPassesTestCaseCheckerIncrementalFold(t *testing.T) {
	id := testIdentifier{testSuites: []string{"outer", "inner"}, testName: "target test"}
	jobRuns := []*analyzerTestJobRun{
		{id: "pass", humanURL: "https://prow/pass", artifactURL: "https://gcs/pass", testSuites: nestedTestSuites(id, false)},
		{id: "fail", humanURL: "https://prow/fail", artifactURL: "https://gcs/fail", testSuites: nestedTestSuites(id, true)},
		{id: "skip", humanURL: "https://prow/skip", artifactURL: "https://gcs/skip", testSuites: &junit.TestSuites{Suites: []*junit.TestSuite{{Name: "other"}}}},
	}

	for _, tc := range []struct {
		name                  string
		requiredPasses        int
		expectedFailureOutput *junit.FailureOutput
	}{
		{name: "equivalent passing result", requiredPasses: 1},
		{name: "equivalent failing result", requiredPasses: 2, expectedFailureOutput: &junit.FailureOutput{Message: "required minimum successful count 2, got 1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			checker := newMinimumRequiredPassesTestCaseChecker(id, "aws-ovn", tc.requiredPasses)
			for _, jobRun := range jobRuns {
				checker.AddJobRun(jobRun, jobRun.testSuites)
			}

			suite := checker.TestSuite()
			require.NotNil(t, suite)
			assert.Equal(t, uint(1), suite.NumTests)
			if tc.expectedFailureOutput == nil {
				assert.Zero(t, suite.NumFailed)
			} else {
				assert.Equal(t, uint(1), suite.NumFailed)
			}
			require.Len(t, suite.Children, 1)
			require.Len(t, suite.Children[0].Children, 1)
			require.Len(t, suite.Children[0].Children[0].TestCases, 1)
			testCase := suite.Children[0].Children[0].TestCases[0]
			assert.Equal(t, "test 'target test' has required number of successful passes across payload jobs for aws-ovn", testCase.Name)
			assert.Equal(t, tc.expectedFailureOutput, testCase.FailureOutput)

			details := &jobrunaggregatorlib.TestCaseDetails{}
			require.NoError(t, yaml.Unmarshal([]byte(testCase.SystemOut), details))
			assert.Equal(t, "target test", details.Name)
			assert.Equal(t, "outer"+jobrunaggregatorlib.TestSuitesSeparator+"inner", details.TestSuiteName)
			assert.Equal(t, "Total job runs: 3, passes: 1, failures: 1, skips 1", details.Summary)
			require.Len(t, details.Passes, 1)
			require.Len(t, details.Failures, 1)
			require.Len(t, details.Skips, 1)
			assert.Equal(t, "pass", details.Passes[0].JobRunID)
			assert.Equal(t, "https://prow/pass", details.Passes[0].HumanURL)
			assert.Equal(t, "https://gcs/pass", details.Passes[0].GCSArtifactURL)
			assert.Equal(t, "fail", details.Failures[0].JobRunID)
			assert.Equal(t, "skip", details.Skips[0].JobRunID)
		})
	}
}

type recordingTestCaseChecker struct {
	events *[]string
	added  []string
}

func (c *recordingTestCaseChecker) AddJobRun(jobRun jobrunaggregatorapi.JobRunInfo, _ *junit.TestSuites) {
	id := jobRun.GetJobRunID()
	c.added = append(c.added, id)
	*c.events = append(*c.events, "add:"+id)
}

func (c *recordingTestCaseChecker) TestSuite() *junit.TestSuite {
	*c.events = append(*c.events, "suite")
	return &junit.TestSuite{Name: "recording", NumTests: uint(len(c.added))}
}

func TestRunTestCaseCheckersProcessesAndCleansIncrementally(t *testing.T) {
	events := []string{}
	checker := &recordingTestCaseChecker{events: &events}
	options := &JobRunTestCaseAnalyzerOptions{testCaseCheckers: []TestCaseChecker{checker}}
	suites := &junit.TestSuites{Suites: []*junit.TestSuite{{Name: "suite"}}}
	finished := &analyzerTestJobRun{id: "finished", testSuites: suites, events: &events}
	readError := &analyzerTestJobRun{id: "read-error", err: errors.New("could not read junit"), events: &events}
	unfinished := &analyzerTestJobRun{id: "unfinished", testSuites: suites, events: &events}

	got := options.runTestCaseCheckers(context.Background(), []jobrunaggregatorapi.JobRunInfo{finished, readError}, []jobrunaggregatorapi.JobRunInfo{unfinished})

	assert.Equal(t, []string{
		"get:finished", "add:finished", "clear:finished",
		"get:read-error",
		"get:unfinished", "add:unfinished", "clear:unfinished",
		"suite",
	}, events, "each successful job run must be folded and cleared before the next is fetched; read errors are skipped")
	assert.Equal(t, []string{"finished", "unfinished"}, checker.added)
	assert.Equal(t, "payload-cross-jobs", got.Name)
	assert.Equal(t, uint(2), got.NumTests)
	assert.Zero(t, got.NumFailed)
	require.Len(t, got.Children, 1)
}
