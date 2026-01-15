package jobrunaggregatoranalyzer

import (
	"bytes"
	_ "embed"
	"fmt"
	"html/template"
	"regexp"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v2"

	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorlib"
	"github.com/openshift/ci-tools/pkg/junit"
)

var (
	//go:embed aggregation-testrun-summary.gohtml
	htmlTemplateString string

	summaryPassRegex           = regexp.MustCompile(`Passed (?P<passNum>[0-9]+) times, failed (?P<failedNum>[0-9]+) times\..*The historical pass rate is (?P<passHistoryPercent>[0-9]+)%\..*The required number of passes is (?P<requiredNum>[0-9]+)\.`)
	summaryQuantileRegex       = regexp.MustCompile(`Passed (?P<passNum>[0-9]+) times, failed (?P<failedNum>[0-9]+) times\..*\((?P<quantile>P[0-9]+)=(?P<quantileValue>.*).*requiredPasses=(?P<requiredNum>[0-9]+).*(?P<successes>successes=\[.*\]).*(?P<failures>failures=\[.*\])\).*`)
	summaryQuantileFailedRegex = regexp.MustCompile(`.*\((?P<quantile>P[0-9]+)=(?P<quantileValue>.*).*(?P<failures>failures=\[.*\])\).*`)
	quantileGraceRegex         = regexp.MustCompile(`.*\(grace=(?P<grace>[0-9]+)\)`)

	htmlTemplate = template.Must(
		template.
			New("spyglass_summary").
			Funcs(template.FuncMap{
				"isFailed":         func() testCaseFilterFunc { return isFailed },
				"isSkipped":        func() testCaseFilterFunc { return isSkipped },
				"isSuccess":        func() testCaseFilterFunc { return isSuccess },
				"infoForTestSuite": infoForTestSuite,
				"toLower":          strings.ToLower,
				"getJobRunNumber": func(jobRunIDToNumber map[string]int, jobRunID string) int {
					return jobRunIDToNumber[jobRunID]
				},
				"dict": func(values ...interface{}) (map[string]interface{}, error) {
					if len(values)%2 != 0 {
						return nil, fmt.Errorf("dict requires an even number of arguments")
					}
					dict := make(map[string]interface{}, len(values)/2)
					for i := 0; i < len(values); i += 2 {
						key, ok := values[i].(string)
						if !ok {
							return nil, fmt.Errorf("dict keys must be strings")
						}
						dict[key] = values[i+1]
					}
					return dict, nil
				},
				"mapHasKey": func(value map[string]string, key string) bool {
					_, found := value[key]
					return found
				},
				"quantileTimeDiff": func(a, b string) string {
					aDurrString := strings.Split(strings.TrimSpace(a), " ")[0]
					bDurrString := strings.Split(strings.TrimSpace(b), " ")[0]
					aGraceString := "0s"
					bGraceString := "0s"
					// Sometimes quantiles can include grace period, we add it if we find it
					if match := quantileGraceRegex.FindStringSubmatch(a); match != nil {
						for i, name := range quantileGraceRegex.SubexpNames() {
							if i != 0 && name != "" {
								aGraceString = fmt.Sprintf("%ss", match[i])
								break
							}
						}
					}
					if match := quantileGraceRegex.FindStringSubmatch(b); match != nil {
						for i, name := range quantileGraceRegex.SubexpNames() {
							if i != 0 && name != "" {
								bGraceString = fmt.Sprintf("%ss", match[i])
								break
							}
						}
					}
					aDurr, err := time.ParseDuration(aDurrString)
					if err != nil {
						fmt.Println(err)
						return a
					}
					aGrace, err := time.ParseDuration(aGraceString)
					if err != nil {
						fmt.Println(err)
						return a
					}
					bDurr, err := time.ParseDuration(bDurrString)
					if err != nil {
						fmt.Println(err)
						return a
					}
					bGrace, err := time.ParseDuration(bGraceString)
					if err != nil {
						fmt.Println(err)
						return a
					}
					return fmt.Sprintf("%.1fs", ((aDurr + aGrace) - (bDurr + bGrace)).Seconds())
				},
				"formatSummary": func(summary string) map[string]string {
					if match := summaryPassRegex.FindStringSubmatch(summary); match != nil {
						result := make(map[string]string)
						for i, name := range summaryPassRegex.SubexpNames() {
							if i != 0 && name != "" {
								result[name] = match[i]
							}
						}
						return result
					}
					if match := summaryQuantileRegex.FindStringSubmatch(summary); match != nil {
						result := make(map[string]string)
						for i, name := range summaryQuantileRegex.SubexpNames() {
							if i != 0 && name != "" {
								result[name] = match[i]
							}
						}
						return result
					}
					if match := summaryQuantileFailedRegex.FindStringSubmatch(summary); match != nil {
						result := make(map[string]string)
						for i, name := range summaryQuantileFailedRegex.SubexpNames() {
							if i != 0 && name != "" {
								result[name] = match[i]
							}
						}
						return result
					}
					return nil
				},
				"parseQuantileValues": func(quantileValues string) map[string]string {
					if quantileValues == "" {
						return nil
					}

					// Parse successes=[ jobId=sec ]
					cutValues, found := strings.CutPrefix(quantileValues, "successes=")
					if !found {
						cutValues, found = strings.CutPrefix(quantileValues, "failures=")
						if !found {
							return nil
						}
					}
					trimmedValue := strings.TrimPrefix(cutValues, "[")
					trimmedValue = strings.TrimSuffix(trimmedValue, "]")
					parsedTuples := strings.Split(trimmedValue, " ")
					var finalMappedValues = make(map[string]string)
					for _, tuple := range parsedTuples {
						values := strings.Split(tuple, "=")
						if len(values) == 2 {
							finalMappedValues[values[0]] = values[1]
						}
					}
					return finalMappedValues
				},
			}).
			Parse(htmlTemplateString),
	)
)

// if someone has the HTML skills, making this a mini-test grid would be awesome.
func htmlForTestRuns(jobName string, suite *junit.TestSuite) (string, error) {
	// Collect and number all job runs
	allJobRunIDs := collectAllJobRunIDs(suite)
	jobRunIDToNumber := make(map[string]int)
	for i, jobRunID := range allJobRunIDs {
		jobRunIDToNumber[jobRunID] = i + 1
	}

	data := struct {
		JobName          string
		Suite            *junit.TestSuite
		InitialParents   []string
		JobRunIDToNumber map[string]int
	}{
		JobName:          jobName,
		Suite:            suite,
		InitialParents:   []string{},
		JobRunIDToNumber: jobRunIDToNumber,
	}
	buff := bytes.Buffer{}
	err := htmlTemplate.Execute(&buff, data)
	return buff.String(), err
}

type testCaseFilterFunc func(*junit.TestCase) bool

func isFailed(testCase *junit.TestCase) bool {
	if strings.Contains(testCase.SystemOut, ": we require at least") {
		return true
	}
	if testCase.FailureOutput == nil {
		return false
	}
	if len(testCase.FailureOutput.Output) == 0 && len(testCase.FailureOutput.Message) == 0 {
		return false
	}
	return true
}

func isSuccess(testCase *junit.TestCase) bool {
	return !isFailed(testCase)
}

func isSkipped(testCase *junit.TestCase) bool {
	return testCase.SkipMessage != nil
}

func infoForTestSuite(jobName string, parents []string, suite *junit.TestSuite, filter testCaseFilterFunc) []TestInfo {
	data := []TestInfo{}
	currSuite := parents
	if len(suite.Name) > 0 {
		currSuite = append(currSuite, suite.Name)
	}
	for _, testCase := range suite.TestCases {
		if info := infoForTestCase(jobName, currSuite, testCase, filter); info != nil {
			data = append(data, *info)
		}
	}

	for _, child := range suite.Children {
		curr := infoForTestSuite(jobName, currSuite, child, filter)
		data = append(data, curr...)
	}
	return data
}

func infoForTestCase(jobName string, parents []string, testCase *junit.TestCase, filter testCaseFilterFunc) *TestInfo {
	if !filter(testCase) {
		return nil
	}
	testInfo := TestInfo{
		Name:    testCase.Name,
		Parents: parents,
	}
	switch {
	case testCase.SkipMessage != nil:
		testInfo.Status = "Skipped"
	case isFailed(testCase):
		testInfo.Status = "Failed"
	default:
		testInfo.Status = "Passed"
	}

	currDetails := &jobrunaggregatorlib.TestCaseDetails{}
	_ = yaml.Unmarshal([]byte(testCase.SystemOut), currDetails)

	if len(currDetails.Failures) == 0 && !strings.Contains(currDetails.Summary, ": we require at least") {
		return nil
	}

	testInfo.Summary = currDetails.Summary

	// a job can have failed runs and still not be failed because it has flaked.
	failedJobRuns := getFailedJobNames(currDetails)
	if len(failedJobRuns) > 0 {
		for _, currFailure := range currDetails.Failures {
			if !failedJobRuns.Has(currFailure.JobRunID) {
				// if we are here, we flaked, we didn't fail
				continue
			}
			testInfo.JobRuns = append(testInfo.JobRuns, JobRunInfo{
				JobName:  jobName,
				HumanURL: currFailure.HumanURL,
				JobRunID: currFailure.JobRunID,
				Status:   "Failure",
			})
		}
	}
	if len(failedJobRuns) != len(currDetails.Failures) {
		seen := sets.Set[string]{}
		for _, currFailure := range currDetails.Failures {
			if seen.Has(currFailure.JobRunID) {
				continue
			}
			if failedJobRuns.Has(currFailure.JobRunID) {
				// if we are here, we failed, we didn't flake
				continue
			}
			testInfo.JobRuns = append(testInfo.JobRuns, JobRunInfo{
				JobName:  jobName,
				HumanURL: currFailure.HumanURL,
				JobRunID: currFailure.JobRunID,
				Status:   "Flake",
			})
			seen.Insert(currFailure.JobRunID)
		}
	}

	return &testInfo
}

// collectAllJobRunIDs traverses the test suite tree and collects all unique JobRunIDs
func collectAllJobRunIDs(suite *junit.TestSuite) []string {
	jobRunIDSet := sets.Set[string]{}
	collectJobRunIDsFromSuite(suite, jobRunIDSet)

	// Sort alphabetically for stable ordering
	jobRunIDs := sets.List(jobRunIDSet)
	sort.Strings(jobRunIDs)

	return jobRunIDs
}

// collectJobRunIDsFromSuite recursively extracts JobRunIDs from test cases
func collectJobRunIDsFromSuite(suite *junit.TestSuite, jobRunIDs sets.Set[string]) {
	// Extract JobRunIDs from test case details (passes, failures, skips)
	for _, testCase := range suite.TestCases {
		currDetails := &jobrunaggregatorlib.TestCaseDetails{}
		if len(testCase.SystemOut) > 0 {
			_ = yaml.Unmarshal([]byte(testCase.SystemOut), currDetails)
		}

		for _, failure := range currDetails.Failures {
			jobRunIDs.Insert(failure.JobRunID)
		}
		for _, pass := range currDetails.Passes {
			jobRunIDs.Insert(pass.JobRunID)
		}
		for _, skip := range currDetails.Skips {
			jobRunIDs.Insert(skip.JobRunID)
		}
	}

	// Recursively process child suites
	for _, child := range suite.Children {
		collectJobRunIDsFromSuite(child, jobRunIDs)
	}
}
