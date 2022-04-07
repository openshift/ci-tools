package consultation

import (
	"testing"

	jiraapi "github.com/andygrunwald/go-jira"
	"github.com/slack-go/slack"

	"github.com/openshift/ci-tools/pkg/jira"
	"github.com/openshift/ci-tools/pkg/slack/modals"
	"github.com/openshift/ci-tools/pkg/slack/modals/modaltesting"
)

func TestProcessSubmissionHandler(t *testing.T) {
	happyPath := modaltesting.SubmissionTestCase{
		Name: "happy path with additional details",
		Filer: jira.NewFake(map[jira.IssueRequest]jira.IssueResponse{
			{
				IssueType: "Story",
				Title:     "Please Help Me",
				Description: `h3. Requirement
I would like to do something really simple.

h3. Previous Efforts
I have tried nothing and looked at no docs.

h3. Acceptance Criteria
* something simple is done
* I didn't have to do anything to achieve it

h3. Additional Details
I'll bug you forever while we work on this.`,
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
					ViewID:     "V01CU3D2A73",
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
	modaltesting.ValidateSubmission(t, processSubmissionHandler(happyPath.Filer, happyPath.Updater), happyPath)
}

func TestIssueParameters(t *testing.T) {
	parameters := issueParameters()
	modaltesting.ValidateBlockIds(t, View(), parameters.Fields...)
	modaltesting.ValidateParameterProcessing(t, parameters, []modaltesting.ProcessTestCase{
		{
			Name:          "one acceptance criteria, no additional details",
			ExpectedTitle: "Give Me a Consult",
			ExpectedBody: `h3. Requirement
I need to order dinner.

h3. Previous Efforts
Dinner has to be tasty and I can't make up my mind. Help?

h3. Acceptance Criteria
* Dinner is ordered and tasty`,
		},
		{
			Name:          "many acceptance criteria, additional details",
			ExpectedTitle: "Please Help Me",
			ExpectedBody: `h3. Requirement
I would like to do something really simple.

h3. Previous Efforts
I have tried nothing and looked at no docs.

h3. Acceptance Criteria
* something simple is done
* I didn't have to do anything to achieve it

h3. Additional Details
I'll bug you forever while we work on this.`,
		},
	})
}
