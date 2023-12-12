package jobruntestcaseanalyzer

import (
	"context"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v2"

	"k8s.io/apimachinery/pkg/util/sets"
	prowjobv1 "k8s.io/test-infra/prow/apis/prowjobs/v1"
	prowjobclientset "k8s.io/test-infra/prow/client/clientset/versioned"

	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorapi"
	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorlib"
	"github.com/openshift/ci-tools/pkg/junit"
)

type testIdentifier struct {
	testSuites []string
	testName   string
}

var (
	installTestGroup      = "install"
	installTestSuites     = []string{"cluster install"}
	installTest           = "install should succeed: overall"
	installTestIdentifier = testIdentifier{testSuites: installTestSuites, testName: installTest}

	overallTestGroup      = "overall"
	overallTestsSuite     = []string{"step graph"}
	overallTestsTest      = "Run multi-stage test test phase"
	overallTestIdentifier = testIdentifier{testSuites: overallTestsSuite, testName: overallTestsTest}
)

// JobGetter gets related jobs for further analysis
type JobGetter interface {
	GetJobs(ctx context.Context) ([]jobrunaggregatorapi.JobRow, error)
}

func NewTestCaseAnalyzerJobGetter(platform, infrastructure, network, testNameSuffix string,
	excludeJobNames, includeJobNames []string,
	jobGCSPrefixes *[]jobGCSPrefix, ciDataClient jobrunaggregatorlib.CIDataClient) *testCaseAnalyzerJobGetter {
	jobGetter := &testCaseAnalyzerJobGetter{
		platform:       platform,
		infrastructure: infrastructure,
		network:        network,
		testNameSuffix: testNameSuffix,
		jobGCSPrefixes: jobGCSPrefixes,
		ciDataClient:   ciDataClient,
		jobNames:       sets.Set[string]{},
	}
	if jobGCSPrefixes != nil && len(*jobGCSPrefixes) > 0 {
		for i := range *jobGCSPrefixes {
			jobGCSPrefix := (*jobGCSPrefixes)[i]
			jobGetter.jobNames.Insert(jobGCSPrefix.jobName)
		}
	}
	if len(excludeJobNames) > 0 {
		jobGetter.excludeJobNames = sets.Set[string]{}
		jobGetter.excludeJobNames.Insert(excludeJobNames...)
	}

	if len(includeJobNames) > 0 {
		jobGetter.includeJobNames = sets.Set[string]{}
		jobGetter.includeJobNames.Insert(includeJobNames...)
	}

	return jobGetter
}

type testCaseAnalyzerJobGetter struct {
	platform        string
	infrastructure  string
	network         string
	excludeJobNames sets.Set[string]
	includeJobNames sets.Set[string]
	testNameSuffix  string
	jobGCSPrefixes  *[]jobGCSPrefix
	ciDataClient    jobrunaggregatorlib.CIDataClient
	jobNames        sets.Set[string]
}

func (s *testCaseAnalyzerJobGetter) shouldAggregateJob(prowJob *prowjobv1.ProwJob) bool {
	jobName := prowJob.Annotations[jobrunaggregatorlib.ProwJobJobNameAnnotation]
	// if PR payload, only find the exact jobs
	if s.jobGCSPrefixes != nil && len(*s.jobGCSPrefixes) > 0 {
		if !s.jobNames.Has(jobName) {
			return false
		}
	} else {
		if len(s.platform) != 0 && !strings.Contains(strings.ToLower(jobName), s.platform) {
			return false
		}
		if len(s.network) != 0 {
			network := "sdn"
			if strings.Contains(strings.ToLower(jobName), "ovn") {
				network = "ovn"
			}
			if s.network != network {
				return false
			}
		}
		if len(s.infrastructure) != 0 && s.infrastructure != getJobInfrastructure(jobName) {
			return false
		}

		if !s.isJobNameIncluded(jobName) {
			return false
		}

		if s.isJobNameExcluded(jobName) {
			return false
		}
	}
	return true
}

// GetJobs find all related jobs for the test case analyzer
// For PR payload, this contains jobs correspond to the list of jobGCSPreix passed
// For release-controller generated payload, this contains all jobs meeting selection criteria
// from command args.
func (s *testCaseAnalyzerJobGetter) GetJobs(ctx context.Context) ([]jobrunaggregatorapi.JobRow, error) {
	jobs, err := s.ciDataClient.ListAllJobs(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list all jobs: %w", err)
	}

	// if PR payload, only find the exact jobs
	if s.jobGCSPrefixes != nil && len(*s.jobGCSPrefixes) > 0 {
		jobs = s.filterJobsByNames(s.jobNames, jobs)
	} else {
		// Non PR payload, select by criteria
		jobs = s.filterJobsForPayload(jobs)
	}
	return jobs, nil
}

func getJobInfrastructure(name string) string {
	if strings.Contains(name, "upi") {
		return "upi"
	}
	return "ipi"
}

func (s *testCaseAnalyzerJobGetter) filterJobsForPayload(allJobs []jobrunaggregatorapi.JobRow) []jobrunaggregatorapi.JobRow {
	jobs := []jobrunaggregatorapi.JobRow{}
	for i := range allJobs {
		job := allJobs[i]
		if (len(s.platform) != 0 && job.Platform != s.platform) ||
			(len(s.network) != 0 && job.Network != s.network) ||
			(len(s.infrastructure) != 0 && s.infrastructure != getJobInfrastructure(job.JobName)) {
			continue
		}

		if !s.isJobNameIncluded(job.JobName) {
			continue
		}

		if s.isJobNameExcluded(job.JobName) {
			continue
		}

		jobs = append(jobs, job)
	}
	return jobs
}

// isJobNameIncluded checks to see the job name contains all strings defined in includeJobNames
func (s *testCaseAnalyzerJobGetter) isJobNameIncluded(jobName string) bool {

	if s.includeJobNames == nil {
		return true
	}

	for key := range s.includeJobNames {
		if !strings.Contains(jobName, key) {
			return false
		}
	}

	return true
}

func (s *testCaseAnalyzerJobGetter) isJobNameExcluded(jobName string) bool {

	if s.excludeJobNames == nil {
		return false
	}

	for key := range s.excludeJobNames {
		if strings.Contains(jobName, key) {
			return true
		}
	}

	return false
}

func (s *testCaseAnalyzerJobGetter) filterJobsByNames(jobNames sets.Set[string], allJobs []jobrunaggregatorapi.JobRow) []jobrunaggregatorapi.JobRow {
	ret := []jobrunaggregatorapi.JobRow{}
	for i := range allJobs {
		curr := allJobs[i]
		if jobNames.Has(curr.JobName) {
			ret = append(ret, curr)
		}
	}
	return ret
}

// TestCaseChecker checks if a test passes certain criteria across all job runs
type TestCaseChecker interface {
	// CheckTestCase returns a test suite based on whether a test has passed certain criteria across job runs
	CheckTestCase(ctx context.Context, jobRunJunits map[jobrunaggregatorapi.JobRunInfo]*junit.TestSuites) *junit.TestSuite
}

type minimumRequiredPassesTestCaseChecker struct {
	id testIdentifier
	// testNameSuffix is a string that will be appended to the test name for the test case to
	// be created. This might include variant info like platform, network and infrastructure etc.
	testNameSuffix         string
	requiredNumberOfPasses int
}

type testStatus int

const (
	testSkipped testStatus = iota
	testPassed
	testFailed
)

func getTestStatus(id testIdentifier, testSuite *junit.TestSuite) testStatus {
	if len(id.testSuites) == 0 || id.testSuites[0] != testSuite.Name {
		return testSkipped
	}
	// We have a top level suite match, search for test case
	if len(id.testSuites) == 1 {
		for _, testCase := range testSuite.TestCases {
			// Exact match will result in either pass or fail
			if testCase.Name == id.testName {
				if testCase.FailureOutput == nil {
					return testPassed
				} else {
					return testFailed
				}
			}
		}
		return testSkipped
	}
	// Search next level
	next := id
	next.testSuites = id.testSuites[1:]
	for _, childSuite := range testSuite.Children {
		if len(next.testSuites) > 0 && next.testSuites[0] == childSuite.Name {
			status := getTestStatus(next, childSuite)
			if status == testPassed || status == testFailed {
				return status
			}
		}
	}
	return testSkipped
}

func (r minimumRequiredPassesTestCaseChecker) addTestResultToDetails(currDetails *jobrunaggregatorlib.TestCaseDetails,
	jobRun jobrunaggregatorapi.JobRunInfo, status testStatus) {
	switch status {
	case testPassed:
		currDetails.Passes = append(
			currDetails.Passes,
			jobrunaggregatorlib.TestCasePass{
				JobRunID:       jobRun.GetJobRunID(),
				HumanURL:       jobRun.GetHumanURL(),
				GCSArtifactURL: jobRun.GetGCSArtifactURL(),
			})
	case testFailed:
		currDetails.Failures = append(
			currDetails.Failures,
			jobrunaggregatorlib.TestCaseFailure{
				JobRunID:       jobRun.GetJobRunID(),
				HumanURL:       jobRun.GetHumanURL(),
				GCSArtifactURL: jobRun.GetGCSArtifactURL(),
			})
	default:
		currDetails.Skips = append(
			currDetails.Skips,
			jobrunaggregatorlib.TestCaseSkip{
				JobRunID:       jobRun.GetJobRunID(),
				HumanURL:       jobRun.GetHumanURL(),
				GCSArtifactURL: jobRun.GetGCSArtifactURL(),
			})
	}
}

// addToTestSuiteFromSuiteNames adds embedded TestSuite from array of suite names to top suite.
// It returns the bottom level suite for easy access for callers.
func addToTestSuiteFromSuiteNames(topSuite *junit.TestSuite, suiteNames []string) *junit.TestSuite {
	previousSuite := topSuite
	for _, suiteName := range suiteNames {
		currentSuite := &junit.TestSuite{
			Name:      suiteName,
			TestCases: []*junit.TestCase{},
		}
		previousSuite.Children = append(previousSuite.Children, currentSuite)
		previousSuite = currentSuite
	}
	return previousSuite
}

func updateTestCountsInSuite(suite *junit.TestSuite) {
	var numTests, numFailed uint
	for _, test := range suite.TestCases {
		numTests++
		if test.FailureOutput != nil {
			numFailed++
		}
	}
	for _, child := range suite.Children {
		updateTestCountsInSuite(child)
		numTests += child.NumTests
		numFailed += child.NumFailed
	}
	suite.NumTests = numTests
	suite.NumFailed = numFailed
}

// CheckTestCase returns a test case based on whether a test has passed certain criteria across job runs
func (r minimumRequiredPassesTestCaseChecker) CheckTestCase(ctx context.Context, jobRunJunits map[jobrunaggregatorapi.JobRunInfo]*junit.TestSuites) *junit.TestSuite {
	topSuite := &junit.TestSuite{
		Name:      "minimum-required-passes-checker",
		TestCases: []*junit.TestCase{},
	}
	bottomSuite := addToTestSuiteFromSuiteNames(topSuite, r.id.testSuites)

	testName := fmt.Sprintf("test '%s' has required number of successful passes across payload jobs", r.id.testName)
	if len(r.testNameSuffix) > 0 {
		testName += fmt.Sprintf(" for %s", r.testNameSuffix)
	}
	testCase := &junit.TestCase{
		Name: testName,
	}
	bottomSuite.TestCases = append(bottomSuite.TestCases, testCase)

	start := time.Now()
	successCount := 0
	currDetails := &jobrunaggregatorlib.TestCaseDetails{
		Name:          r.id.testName,
		TestSuiteName: strings.Join(r.id.testSuites, jobrunaggregatorlib.TestSuitesSeparator),
	}
	for jobRun, testSuites := range jobRunJunits {
		status := testSkipped
		// Now go through test suites check test result
		for _, testSuite := range testSuites.Suites {
			status = getTestStatus(r.id, testSuite)
			found := false
			switch status {
			case testPassed:
				found = true
				successCount++
			case testFailed:
				found = true
			}
			if found {
				break
			}
		}
		r.addTestResultToDetails(currDetails, jobRun, status)
	}
	currDetails.Summary = fmt.Sprintf("Total job runs: %d, passes: %d, failures: %d, skips %d", len(jobRunJunits), len(currDetails.Passes), len(currDetails.Failures), len(currDetails.Skips))
	detailsYaml, err := yaml.Marshal(currDetails)
	if err != nil {
		return nil
	}
	testCase.Duration = time.Since(start).Seconds()
	testCase.SystemOut = string(detailsYaml)
	if successCount < r.requiredNumberOfPasses {
		testCase.FailureOutput = &junit.FailureOutput{
			Message: fmt.Sprintf("required minimum successful count %d, got %d", r.requiredNumberOfPasses, successCount),
		}
	}
	updateTestCountsInSuite(topSuite)
	return topSuite
}

// JobRunTestCaseAnalyzerOptions
// 1. either gets a list of jobs from big query that meet the passed criteria: platform, network, or uses the passed jobs
// 2. finds job runs for matching jobs for the specified payload tag
// 3. runs all test case checkers and constructs a synthetic junit
type JobRunTestCaseAnalyzerOptions struct {
	payloadTag string
	workingDir string
	// jobRunStartEstimate is used by job run locator to calculate the time window to search for job runs.
	jobRunStartEstimate time.Time
	timeout             time.Duration
	ciDataClient        jobrunaggregatorlib.CIDataClient
	ciGCSClient         jobrunaggregatorlib.CIGCSClient
	testCaseCheckers    []TestCaseChecker
	testNameSuffix      string
	payloadInvocationID string
	jobGCSPrefixes      *[]jobGCSPrefix
	jobGetter           JobGetter
	prowJobClient       *prowjobclientset.Clientset
	jobStateQuerySource string
	prowJobMatcherFunc  jobrunaggregatorlib.ProwJobMatcherFunc

	staticJobRunIdentifiers []jobrunaggregatorlib.JobRunIdentifier
	gcsBucket               string
}

func (o *JobRunTestCaseAnalyzerOptions) shouldAggregateJob(prowJob *prowjobv1.ProwJob) bool {
	// first level of match only matches names
	if !o.prowJobMatcherFunc(prowJob) {
		return false
	}
	// second level of match deal with payload or invocation ID
	var prowJobRunMatcherFunc jobrunaggregatorlib.ProwJobMatcherFunc
	jobName := prowJob.Annotations[jobrunaggregatorlib.ProwJobJobNameAnnotation]
	if len(o.payloadTag) > 0 {
		prowJobRunMatcherFunc = jobrunaggregatorlib.NewProwJobMatcherFuncForReleaseController(jobName, o.payloadTag)
	}
	if len(o.payloadInvocationID) > 0 {
		prowJobRunMatcherFunc = jobrunaggregatorlib.NewProwJobMatcherFuncForPR(jobName, o.payloadInvocationID, jobrunaggregatorlib.ProwJobPayloadInvocationIDLabel)
	}

	if prowJobRunMatcherFunc != nil {
		return prowJobRunMatcherFunc(prowJob)
	}
	return true
}

func (o *JobRunTestCaseAnalyzerOptions) findJobRunsWithRetry(ctx context.Context,
	jobName string, jobRunLocator jobrunaggregatorlib.JobRunLocator) ([]jobrunaggregatorapi.JobRunInfo, error) {

	// allow for the list of ids to be passed in
	if len(o.staticJobRunIdentifiers) > 0 {
		return o.loadStaticJobRuns(ctx, jobName, jobRunLocator)
	}

	errorsInARow := 0
	for {
		jobRuns, err := jobRunLocator.FindRelatedJobs(ctx)
		if err != nil {
			if errorsInARow > 20 {
				fmt.Printf("give up finding job runs for %s after retries: %v", jobName, err)
				return nil, err
			}
			errorsInARow++
			fmt.Printf("error finding job runs for %s: %v", jobName, err)
		} else {
			return jobRuns, nil
		}

		fmt.Printf("   waiting and will attempt to find related jobs for %s in a minute\n", jobName)
		select {
		case <-ctx.Done():
			// Simply return. Caller will check ctx and return error
			return nil, ctx.Err()
		case <-time.After(1 * time.Minute):
			continue
		}
	}
}

func (o *JobRunTestCaseAnalyzerOptions) loadStaticJobRuns(ctx context.Context, jobName string, jobRunLocator jobrunaggregatorlib.JobRunLocator) ([]jobrunaggregatorapi.JobRunInfo, error) {
	var outputRuns []jobrunaggregatorapi.JobRunInfo
	for _, jobRun := range o.staticJobRunIdentifiers {
		if jobRun.JobName != jobName {
			continue
		}

		jobRun, err := jobRunLocator.FindJob(ctx, jobRun.JobRunID)
		if err != nil {
			return nil, err
		}
		if jobRun != nil {
			outputRuns = append(outputRuns, jobRun)
		}
	}
	return outputRuns, nil
}

func (o *JobRunTestCaseAnalyzerOptions) loadStaticJobs() []jobrunaggregatorapi.JobRow {
	rows := make([]jobrunaggregatorapi.JobRow, 0)
	uniqueNames := sets.Set[string]{}

	for _, r := range o.staticJobRunIdentifiers {
		// only one row per unique job name
		if !uniqueNames.Has(r.JobName) {
			// we only care about returning JobName
			rows = append(rows, jobrunaggregatorapi.JobRow{JobName: r.JobName})
			uniqueNames.Insert(r.JobName)
		}
	}

	return rows
}

func (o *JobRunTestCaseAnalyzerOptions) GetRelatedJobRunsFromIdentifiers(ctx context.Context, jobRunIdentifiers []jobrunaggregatorlib.JobRunIdentifier) ([]jobrunaggregatorapi.JobRunInfo, error) {
	o.staticJobRunIdentifiers = jobRunIdentifiers
	return o.GetRelatedJobRuns(ctx)
}

// GetRelatedJobRuns gets all related job runs for analysis
func (o *JobRunTestCaseAnalyzerOptions) GetRelatedJobRuns(ctx context.Context) ([]jobrunaggregatorapi.JobRunInfo, error) {
	var jobRunsToReturn []jobrunaggregatorapi.JobRunInfo
	var jobs []jobrunaggregatorapi.JobRow
	var err error

	// allow for the list of ids to be passed in
	if len(o.staticJobRunIdentifiers) > 0 {
		jobs = o.loadStaticJobs()
	} else {
		jobs, err = o.jobGetter.GetJobs(ctx)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to get related jobs: %w", err)
	}

	waitGroup := sync.WaitGroup{}
	resultCh := make(chan []jobrunaggregatorapi.JobRunInfo, len(jobs))
	for i := range jobs {
		job := jobs[i]
		var jobRunLocator jobrunaggregatorlib.JobRunLocator

		if len(o.payloadTag) > 0 {
			jobRunLocator = jobrunaggregatorlib.NewPayloadAnalysisJobLocatorForReleaseController(
				job.JobName,
				o.payloadTag,
				o.jobRunStartEstimate,
				o.ciDataClient,
				o.ciGCSClient,
				o.gcsBucket,
			)
		}
		if len(o.payloadInvocationID) > 0 {
			jobRunLocator = jobrunaggregatorlib.NewPayloadAnalysisJobLocatorForPR(
				job.JobName,
				o.payloadInvocationID,
				jobrunaggregatorlib.ProwJobPayloadInvocationIDLabel,
				o.jobRunStartEstimate,
				o.ciDataClient,
				o.ciGCSClient,
				o.gcsBucket,
				(*o.jobGCSPrefixes)[i].gcsPrefix,
			)
		}

		fmt.Printf("  launching findJobRunsWithRetry for %q\n", job.JobName)

		waitGroup.Add(1)

		go func() {
			defer waitGroup.Done()
			jobRuns, err := o.findJobRunsWithRetry(ctx, job.JobName, jobRunLocator)
			if err == nil {
				resultCh <- jobRuns
			}
		}()
	}
	waitGroup.Wait()
	close(resultCh)

	// drain the result channel first
	for jobRuns := range resultCh {
		jobRunsToReturn = append(jobRunsToReturn, jobRuns...)
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
		break
	}
	return jobRunsToReturn, nil
}

func (o *JobRunTestCaseAnalyzerOptions) runTestCaseCheckers(ctx context.Context,
	finishedJobRuns []jobrunaggregatorapi.JobRunInfo, unfinishedJobRuns []jobrunaggregatorapi.JobRunInfo) *junit.TestSuite {
	suiteName := "payload-cross-jobs"
	topSuite := &junit.TestSuite{
		Name:      suiteName,
		TestCases: []*junit.TestCase{},
	}

	allJobRuns := append(finishedJobRuns, unfinishedJobRuns...)
	jobRunJunitMap := map[jobrunaggregatorapi.JobRunInfo]*junit.TestSuites{}
	for i := range allJobRuns {
		jobRun := allJobRuns[i]

		testSuites, err := jobRun.GetCombinedJUnitTestSuites(ctx)
		if err != nil {
			continue
		}
		jobRunJunitMap[jobRun] = testSuites
	}
	for _, checker := range o.testCaseCheckers {
		testSuite := checker.CheckTestCase(ctx, jobRunJunitMap)
		topSuite.Children = append(topSuite.Children, testSuite)
		topSuite.NumTests += testSuite.NumTests
		topSuite.NumFailed += testSuite.NumFailed
	}
	return topSuite
}

func (o *JobRunTestCaseAnalyzerOptions) Run(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, o.timeout)
	defer cancel()

	matchID := o.payloadTag
	if len(matchID) == 0 {
		matchID = o.payloadInvocationID
	}

	outputDir := filepath.Join(o.workingDir, matchID)
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("error creating output directory %q: %w", outputDir, err)
	}

	// if it hasn't been more than two hours since the jobRuns started, the list isn't complete.
	readyAt := o.jobRunStartEstimate.Add(2 * time.Hour)

	durationToWait := o.timeout - 20*time.Minute
	timeToStopWaiting := o.jobRunStartEstimate.Add(durationToWait)

	fmt.Printf("Analyzing test status for job runs for %q.  now=%v, ReadyAt=%v, timeToStopWaiting=%v.\n", matchID, time.Now(), readyAt, timeToStopWaiting)

	err := jobrunaggregatorlib.WaitUntilTime(ctx, readyAt)
	if err != nil {
		return err
	}

	var jobRunWaiter jobrunaggregatorlib.JobRunWaiter
	if o.jobStateQuerySource == jobrunaggregatorlib.JobStateQuerySourceBigQuery || o.prowJobClient == nil {
		jobRunWaiter = &jobrunaggregatorlib.BigQueryJobRunWaiter{JobRunGetter: o, TimeToStopWaiting: timeToStopWaiting}
	} else {
		jobRunWaiter = &jobrunaggregatorlib.ClusterJobRunWaiter{
			ProwJobClient:      o.prowJobClient,
			TimeToStopWaiting:  timeToStopWaiting,
			ProwJobMatcherFunc: o.shouldAggregateJob,
		}
	}

	finishedJobRuns, unfinishedJobRuns, _, _, err := jobrunaggregatorlib.WaitAndGetAllFinishedJobRuns(ctx, o, jobRunWaiter, outputDir, o.testNameSuffix)
	if err != nil {
		return err
	}

	testSuite := o.runTestCaseCheckers(ctx, finishedJobRuns, unfinishedJobRuns)
	jobrunaggregatorlib.OutputTestCaseFailures([]string{"root"}, testSuite)

	// Done with all tests
	junitXML, err := xml.Marshal(testSuite)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(outputDir, "junit-test-case-analysis.xml"), junitXML, 0644); err != nil {
		return err
	}
	if testSuite.NumFailed > 0 {
		return fmt.Errorf("some test checker failed,  see above for details")
	}
	return nil
}
