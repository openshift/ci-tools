package bug

import (
	"testing"

	jiraapi "github.com/andygrunwald/go-jira"
	"github.com/sirupsen/logrus"
	"github.com/slack-go/slack"

	"github.com/openshift/ci-tools/pkg/jira"
	"github.com/openshift/ci-tools/pkg/slack/interactions"
	"github.com/openshift/ci-tools/pkg/slack/modals"
	"github.com/openshift/ci-tools/pkg/slack/modals/modaltesting"
	"github.com/openshift/ci-tools/pkg/testhelper"
)

func TestValidateSubmissionHandler(t *testing.T) {
	var testCases = []struct {
		name            string
		expectedHandled bool
		expectedError   bool
	}{
		{
			name:            "valid because specific component chosen",
			expectedHandled: false,
			expectedError:   false,
		},
		{
			name:            "valid because other component chosen and written in",
			expectedHandled: false,
			expectedError:   false,
		},
		{
			name:            "invalid because other component chosen and not written in",
			expectedHandled: true,
			expectedError:   false,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			var callback slack.InteractionCallback
			modaltesting.ReadCallbackFixture(t, &callback)
			handled, out, err := validateSubmissionHandler().Handle(&callback, logrus.WithField("test", testCase.name))
			if expected, actual := testCase.expectedHandled, handled; expected != actual {
				t.Errorf("%s: expected handled %v but got %v", testCase.name, expected, actual)
			}
			testhelper.CompareWithFixture(t, out)
			if testCase.expectedError && err == nil {
				t.Errorf("%s: expected an error but got none", testCase.name)
			}
			if !testCase.expectedError && err != nil {
				t.Errorf("%s: expected no error but got one: %v", testCase.name, err)
			}
		})
	}
}

func TestProcessSubmissionHandler(t *testing.T) {
	happyPath := modaltesting.SubmissionTestCase{
		Name: "happy path with custom component",
		Filer: jira.NewFake(map[jira.IssueRequest]jira.IssueResponse{
			{
				IssueType: "Bug",
				Title:     "My Title",
				Description: `h3. Symptomatic Behavior
Something wrong!

h3. Expected Behavior
Something right!

h3. Impact
I'm on fire.

h3. Category
Other: My Component

h3. How to Reproduce
Every time, just push the button.`,
				Reporter: "U01B31ARZDG",
			}: {
				Issue: &jiraapi.Issue{Key: "WHOA-123"},
				Error: nil,
			},
		}),
		Updater: modals.NewFake([]modals.ViewUpdate{
			{
				ViewUpdateRequest: modals.ViewUpdateRequest{
					View:       modals.JiraView("WHOA-123"),
					ExternalID: "",
					Hash:       "", // updated unconditionally, should be empty
					ViewID:     "V01BYJ3JXN3",
				},
				ViewUpdateResponse: modals.ViewUpdateResponse{
					Response: &slack.ViewResponse{},
					Error:    nil,
				},
			},
		}),
		ExpectedPayload: []byte(`{"response_action":"update","view":{"type":"modal","title":{"type":"plain_text","text":"Creating Jira Issue..."},"blocks":[{"type":"section","text":{"type":"mrkdwn","text":"A Jira issue is being filed, please do not close this window..."}}],"private_metadata":"jira_pending"}}`),
		ExpectedError:   false,
	}
	modaltesting.ValidateSubmission(t, interactions.HandlerFromPartial(processSubmissionHandler(happyPath.Filer, happyPath.Updater)), happyPath)
}

func TestIssueParameters(t *testing.T) {
	parameters := issueParameters()
	modaltesting.ValidateBlockIds(t, View(), parameters.Fields...)
	modaltesting.ValidateParameterProcessing(t, parameters, []modaltesting.ProcessTestCase{
		{
			Name:          "custom component",
			ExpectedTitle: "My Title",
			ExpectedBody: `h3. Symptomatic Behavior
Something wrong!

h3. Expected Behavior
Something right!

h3. Impact
I'm on fire.

h3. Category
Other: My Component

h3. How to Reproduce
Every time, just push the button.`,
		},
		{
			Name:          "extant component",
			ExpectedTitle: "My Title",
			ExpectedBody: `h3. Symptomatic Behavior
Something wrong!

h3. Expected Behavior
Something right!

h3. Impact
I'm on fire.

h3. Category
Release Controller

h3. How to Reproduce
Every time, just push the button.`,
		},
	})
}
