package jobrunaggregatoranalyzer

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v2"

	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorlib"
	"github.com/openshift/ci-tools/pkg/junit"
)

// if someone has the HTML skills, making this a mini-test grid would be awesome.
func htmlForTestRuns(jobName string, suite *junit.TestSuite) string {
	html := `<!DOCTYPE html>
<html>
<body>
`
	failedHTML := htmlForTestSuite(jobName, []string{}, suite, failedOnly)
	if len(failedHTML) > 0 {
		html += `
<h2>Failed Tests</h2>
<ol>
`
		html += failedHTML
		html += `
</ol>
<br/>
`
	}

	html += `
<h2>Skipped Tests</h2>
<ol>
`
	html += htmlForTestSuite(jobName, []string{}, suite, skippedOnly)
	html += `
</ol>
<br/>
`

	html += `
<h2>Passed Tests</h2>
<ol>
`
	html += htmlForTestSuite(jobName, []string{}, suite, successOnly)
	html += `
</ol>
<br/>
`

	html += `
</body>
</html>`

	return html
}

type testCaseFilterFunc func(*junit.TestCase) bool

func failedOnly(testCase *junit.TestCase) bool {
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

func successOnly(testCase *junit.TestCase) bool {
	return !failedOnly(testCase)
}

func skippedOnly(testCase *junit.TestCase) bool {
	return testCase.SkipMessage != nil
}

func htmlForTestSuite(jobName string, parents []string, suite *junit.TestSuite, filter testCaseFilterFunc) string {
	htmls := []string{}
	currSuite := parents
	if len(suite.Name) > 0 {
		currSuite = append(currSuite, suite.Name)
	}
	for _, testCase := range suite.TestCases {
		curr := htmlForTestCase(jobName, currSuite, testCase, filter)
		if len(curr) > 0 {
			htmls = append(htmls, curr)
		}
	}

	for _, child := range suite.Children {
		curr := htmlForTestSuite(jobName, currSuite, child, filter)
		if len(curr) > 0 {
			htmls = append(htmls, curr)
		}
	}
	return strings.Join(htmls, "\n")
}

func htmlForTestCase(jobName string, parents []string, testCase *junit.TestCase, filter testCaseFilterFunc) string {
	if !filter(testCase) {
		return ""
	}
	var status string
	switch {
	case testCase.SkipMessage != nil:
		status = "Skipped"
	case failedOnly(testCase):
		status = "Failed"
	default:
		status = "Passed"
	}

	var failureHTML string
	var flakeHTML string
	currDetails := &jobrunaggregatorlib.TestCaseDetails{}
	_ = yaml.Unmarshal([]byte(testCase.SystemOut), currDetails)

	if len(currDetails.Failures) == 0 && !strings.Contains(currDetails.Summary, ": we require at least") {
		return ""
	}

	// a job can have failed runs and still not be failed because it has flaked.
	failedJobRuns := getFailedJobNames(currDetails)
	if len(failedJobRuns) > 0 {
		failureHTML = "<p><ol>\n"
		for _, currFailure := range currDetails.Failures {
			if !failedJobRuns.Has(currFailure.JobRunID) {
				// if we are here, we flaked, we didn't fail
				continue
			}
			failureHTML += `<li>`
			failureHTML += fmt.Sprintf(`<a target="_blank" href="%s">Failure - %s/%s</a>`, currFailure.HumanURL, jobName, currFailure.JobRunID)
			failureHTML += "</li>\n"
		}
		failureHTML += "</ol></p>\n"
	}
	if len(failedJobRuns) != len(currDetails.Failures) {
		// this means we have flaked
		flakeHTML = "<p><ol>\n"
		seen := sets.Set[string]{}
		for _, currFailure := range currDetails.Failures {
			if seen.Has(currFailure.JobRunID) {
				continue
			}
			if failedJobRuns.Has(currFailure.JobRunID) {
				// if we are here, we failed, we didn't flake
				continue
			}
			flakeHTML += `<li>`
			flakeHTML += fmt.Sprintf(`<a target="_blank" href="%s">Flake - %s/%s</a>`, currFailure.HumanURL, jobName, currFailure.JobRunID)
			flakeHTML += "</li>\n"
			seen.Insert(currFailure.JobRunID)
		}
		flakeHTML += "</ol></p>\n"

	}

	html := "<li>\n"
	html += fmt.Sprintf(`
%s: suite=[%s], <b>%v</b>
<p>%v</p>
%v
%v
`,
		status,
		strings.Join(parents, "    "),
		testCase.Name,
		currDetails.Summary,
		failureHTML,
		flakeHTML)
	html += "</li>\n<br/>\n"

	return html
}
