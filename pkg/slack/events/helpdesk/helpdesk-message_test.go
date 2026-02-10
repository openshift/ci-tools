package helpdesk

import (
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/openshift/ci-tools/pkg/testhelper"
)

func TestGetPresentKeywords(t *testing.T) {
	retester := KeywordsListItem{
		Name:     "Retester",
		Link:     "https://docs.ci.openshift.org/architecture/retester/",
		Keywords: []string{"retester"},
	}

	stepExec := KeywordsListItem{
		Name:     "Step Execution Environment",
		Link:     "https://docs.ci.openshift.org/architecture/step-registry/#step-execution-environment",
		Keywords: []string{"variable"},
	}

	jobEnv := KeywordsListItem{
		Name:     "Job Environment Variables",
		Link:     "https://github.com/kubernetes/test-infra/blob/master/prow/jobs.md#job-environment-variables",
		Keywords: []string{"variable"},
	}

	testgrid := KeywordsListItem{
		Name:     "Add a Job to TestGrid",
		Link:     "https://docs.ci.openshift.org/how-tos/add-jobs-to-testgrid/",
		Keywords: []string{"testgrid"},
	}

	fakeKeywordsConfig := KeywordsConfig{[]KeywordsListItem{
		retester, stepExec, jobEnv, testgrid,
	}}

	var testCases = []struct {
		name     string
		message  string
		expected map[string]string
	}{
		{
			name:     "empty message",
			message:  "",
			expected: map[string]string{},
		},
		{
			name:     "no keywords present",
			message:  "message containing no known keywords",
			expected: map[string]string{},
		},
		{
			name:     "one keyword is present",
			message:  fmt.Sprintf("message with exactly %s keyword", retester.Keywords[0]),
			expected: map[string]string{retester.Name: retester.Link},
		},
		{
			name:     "two docs with the same keyword",
			message:  fmt.Sprintf("message with two doc links for one keyword: %s", jobEnv.Keywords[0]),
			expected: map[string]string{jobEnv.Name: jobEnv.Link, stepExec.Name: stepExec.Link},
		},
		{
			name:     "two different keywords present",
			message:  fmt.Sprintf("%s and %s - message with two doc links", retester.Keywords[0], testgrid.Keywords[0]),
			expected: map[string]string{retester.Name: retester.Link, testgrid.Name: testgrid.Link},
		},
		{
			name:     "keyword parsing is not case sensitive",
			message:  "Retester. Also testGrid.",
			expected: map[string]string{retester.Name: retester.Link, testgrid.Name: testgrid.Link},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			actual := getPresentKeywords(testCase.message, fakeKeywordsConfig)
			if diff := cmp.Diff(testCase.expected, actual); diff != "" {
				t.Fatalf("returned map doesn't match expected, diff: %s", diff)
			}
		})
	}
}

func TestGetContactedHelpdeskResponse(t *testing.T) {
	reviewRequestWorkflow := "1234"
	testCases := []struct {
		name  string
		botId string
		user  string
	}{
		{
			name:  "empty botId",
			botId: "",
			user:  "",
		},
		{
			name:  "botId like review workflow",
			botId: reviewRequestWorkflow,
			user:  "user",
		},
		{
			name:  "random botId",
			botId: "botId can be anything except review",
			user:  "user",
		},
		{
			name:  "user is present",
			botId: reviewRequestWorkflow,
			user:  "user",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			actual := getContactedHelpdeskResponse(testCase.botId, reviewRequestWorkflow, testCase.user)
			testhelper.CompareWithFixture(t, actual)
		})
	}
}
