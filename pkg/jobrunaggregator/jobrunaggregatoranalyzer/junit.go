package jobrunaggregatoranalyzer

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v2"

	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorapi"
	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorlib"
	"github.com/openshift/ci-tools/pkg/junit"
)

type jobRunJunit struct {
	jobRun        jobrunaggregatorapi.JobRunInfo
	combinedJunit *junit.TestSuites
}

type jobRunJunitByJobRunID []*jobRunJunit

func (a jobRunJunitByJobRunID) Len() int      { return len(a) }
func (a jobRunJunitByJobRunID) Swap(i, j int) { a[i], a[j] = a[j], a[i] }
func (a jobRunJunitByJobRunID) Less(i, j int) bool {
	return strings.Compare(a[i].jobRun.GetJobRunID(), a[j].jobRun.GetJobRunID()) < 0
}

func newJobRunJunit(ctx context.Context, jobRun jobrunaggregatorapi.JobRunInfo) (*jobRunJunit, error) {
	testSuites, err := jobRun.GetCombinedJUnitTestSuites(ctx)
	if err != nil {
		return nil, fmt.Errorf("error getting content for jobrun/%v/%v: %w", jobRun.GetJobName(), jobRun.GetJobRunID(), err)
	}

	return &jobRunJunit{
		jobRun:        jobRun,
		combinedJunit: testSuites,
	}, nil
}

type aggregatedJobRunJunit struct {
	aggregationNameToJobRuns map[string][]*jobRunJunit

	combinedJunit *junit.TestSuites
}

func (a *aggregatedJobRunJunit) addJobRun(aggregationName string, currJunit *jobRunJunit) {
	if a.aggregationNameToJobRuns == nil {
		a.aggregationNameToJobRuns = map[string][]*jobRunJunit{}
	}
	a.aggregationNameToJobRuns[aggregationName] = append(a.aggregationNameToJobRuns[aggregationName], currJunit)

	// clear the aggregated result if we add a new job run. This ensures we recompute the next time the aggregated
	// result is requested.
	a.combinedJunit = nil
}

func (a *aggregatedJobRunJunit) aggregateAllJobRuns() (*junit.TestSuites, error) {
	if a.combinedJunit != nil {
		return a.combinedJunit, nil
	}

	// sort everything for stable results
	for _, aggregationName := range sets.StringKeySet(a.aggregationNameToJobRuns).List() {
		slice := a.aggregationNameToJobRuns[aggregationName]
		sort.Sort(jobRunJunitByJobRunID(slice))
		a.aggregationNameToJobRuns[aggregationName] = slice
	}

	combined := &junit.TestSuites{}
	for _, aggregationName := range sets.StringKeySet(a.aggregationNameToJobRuns).List() {
		jobRunJunits := a.aggregationNameToJobRuns[aggregationName]
		for _, currJobRunJunit := range jobRunJunits {
			rawBackendDisruptionData, err := currJobRunJunit.jobRun.GetOpenShiftTestsFilesWithPrefix(context.Background(), "backend-disruption")
			if err != nil {
				return nil, err
			}
			if len(rawBackendDisruptionData) == 0 {
				fmt.Fprintf(os.Stderr, "Could not fetch backend disruption data for %s", currJobRunJunit.jobRun.GetJobRunID())
				continue
			}

			disruptionData := jobrunaggregatorlib.GetServerAvailabilityResultsFromDirectData(rawBackendDisruptionData)
			if err != nil {
				return nil, err
			}
			if err := combineTestSuites(combined, disruptionData, currJobRunJunit.jobRun.GetJobName(), currJobRunJunit.jobRun.GetJobRunID(), currJobRunJunit.combinedJunit); err != nil {
				return nil, err
			}
		}
	}

	// after this we have all the affirmative passes, fails, and skips done, but we have not highlighted the job runs
	// that did not run a particular test.
	// TODO decide if we want this information.

	a.combinedJunit = combined
	return a.combinedJunit, nil
}

func combineTestSuites(combined *junit.TestSuites, disruptionData map[string]jobrunaggregatorlib.AvailabilityResult, jobName, toAddJobRunID string, toAdd *junit.TestSuites) error {
	for _, suiteToAdd := range toAdd.Suites {
		combinedSuite := ensureSuiteInSuites(combined, suiteToAdd.Name)
		if err := combineTestSuite(combinedSuite, disruptionData, jobName, toAddJobRunID, suiteToAdd); err != nil {
			return err
		}
	}
	return nil
}

func combineTestSuite(combined *junit.TestSuite, disruptionData map[string]jobrunaggregatorlib.AvailabilityResult, jobName, toAddJobRunID string, toAdd *junit.TestSuite) error {
	for _, testCaseToAdd := range toAdd.TestCases {
		combinedTestCase := ensureTestCaseInSuite(combined, testCaseToAdd.Name)
		if err := aggregateTestCase(combinedTestCase, disruptionData, jobName, toAddJobRunID, testCaseToAdd); err != nil {
			return err
		}
	}

	for _, suiteToAdd := range toAdd.Children {
		combinedSuite := ensureSuiteInSuite(combined, suiteToAdd.Name)
		if err := combineTestSuite(combinedSuite, disruptionData, jobName, toAddJobRunID, suiteToAdd); err != nil {
			return err
		}
	}

	return nil
}

func findSuiteInSuites(o *junit.TestSuites, name string) *junit.TestSuite {
	for i := range o.Suites {
		suite := o.Suites[i]
		if suite.Name == name {
			return suite
		}
	}
	return nil
}

func ensureSuiteInSuites(o *junit.TestSuites, name string) *junit.TestSuite {
	if existing := findSuiteInSuites(o, name); existing != nil {
		return existing
	}

	ret := &junit.TestSuite{Name: name}
	o.Suites = append(o.Suites, ret)
	return ret
}

func findSuiteInSuite(o *junit.TestSuite, name string) *junit.TestSuite {
	for i := range o.Children {
		suite := o.Children[i]
		if suite.Name == name {
			return suite
		}
	}
	return nil
}

func ensureSuiteInSuite(o *junit.TestSuite, name string) *junit.TestSuite {
	if existing := findSuiteInSuite(o, name); existing != nil {
		return existing
	}

	ret := &junit.TestSuite{Name: name}
	o.Children = append(o.Children, ret)
	return ret
}

func findTestCaseInSuite(o *junit.TestSuite, name string) *junit.TestCase {
	for i := range o.TestCases {
		testCase := o.TestCases[i]
		if testCase.Name == name {
			return testCase
		}
	}
	return nil
}

func ensureTestCaseInSuite(o *junit.TestSuite, name string) *junit.TestCase {
	if existing := findTestCaseInSuite(o, name); existing != nil {
		return existing
	}

	ret := &junit.TestCase{Name: name}
	o.TestCases = append(o.TestCases, ret)
	return ret
}

func aggregateTestCase(combined *junit.TestCase, disruptionData map[string]jobrunaggregatorlib.AvailabilityResult, jobName, toAddJobRunID string, toAdd *junit.TestCase) error {
	currDetails := &TestCaseDetails{
		Name: toAdd.Name,
	}

	if len(combined.SystemOut) > 0 {
		if err := yaml.Unmarshal([]byte(combined.SystemOut), currDetails); err != nil {
			return err
		}
	}

	switch {
	case jobrunaggregatorlib.IsDisruptionTest(toAdd.Name):
		backend := jobrunaggregatorlib.GetBackendName(toAdd.Name)
		secondsUnavailable := 0
		if availability, ok := disruptionData[backend]; ok {
			secondsUnavailable = availability.SecondsUnavailable
		} else if toAdd.FailureOutput != nil {
			// Fallback to junit disruption data
			seconds, err := jobrunaggregatorlib.GetOutageSecondsFromMessage(toAdd.FailureOutput.Output)
			if err != nil {
				return err
			}
			secondsUnavailable = seconds
		}

		currDetails.Disruption = append(currDetails.Disruption,
			TestCaseDisruption{
				JobRunID:          toAddJobRunID,
				HumanURL:          jobrunaggregatorapi.GetHumanURL(jobName, toAddJobRunID),
				GCSArtifactURL:    jobrunaggregatorapi.GetGCSArtifactURL(jobName, toAddJobRunID),
				DisruptionSeconds: secondsUnavailable,
			})
	case toAdd.FailureOutput != nil:
		humanURL := jobrunaggregatorapi.GetHumanURL(jobName, toAddJobRunID)
		currDetails.Failures = append(
			currDetails.Failures,
			TestCaseFailure{
				JobRunID:       toAddJobRunID,
				HumanURL:       humanURL,
				GCSArtifactURL: jobrunaggregatorapi.GetGCSArtifactURL(jobName, toAddJobRunID),
			})
	case toAdd.SkipMessage != nil:
		currDetails.Skips = append(
			currDetails.Skips,
			TestCaseSkip{
				JobRunID:       toAddJobRunID,
				HumanURL:       jobrunaggregatorapi.GetHumanURL(jobName, toAddJobRunID),
				GCSArtifactURL: jobrunaggregatorapi.GetGCSArtifactURL(jobName, toAddJobRunID),
			})
	default:
		currDetails.Passes = append(
			currDetails.Passes,
			TestCasePass{
				JobRunID:       toAddJobRunID,
				HumanURL:       jobrunaggregatorapi.GetHumanURL(jobName, toAddJobRunID),
				GCSArtifactURL: jobrunaggregatorapi.GetGCSArtifactURL(jobName, toAddJobRunID),
			})

	}

	detailsYaml, err := yaml.Marshal(currDetails)
	if err != nil {
		return nil
	}
	combined.SystemOut = string(detailsYaml)
	return nil
}

type TestCaseDetails struct {
	Name string
	// Summary is filled in during the pass/fail calculation
	Summary string

	Passes     []TestCasePass
	Failures   []TestCaseFailure
	Skips      []TestCaseSkip
	Disruption []TestCaseDisruption
	//NeverExecuted []TestCaseNeverExecuted
}

type TestCasePass struct {
	JobRunID       string
	HumanURL       string
	GCSArtifactURL string
}

type TestCaseDisruption struct {
	JobRunID          string
	HumanURL          string
	GCSArtifactURL    string
	DisruptionSeconds int
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

type TestCaseNeverExecuted struct {
	JobRunID       string
	HumanURL       string
	GCSArtifactURL string
}
