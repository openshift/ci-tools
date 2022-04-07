package incident

import (
	"errors"
	"testing"

	jiraapi "github.com/andygrunwald/go-jira"
	"github.com/slack-go/slack"

	"github.com/openshift/ci-tools/pkg/jira"
	"github.com/openshift/ci-tools/pkg/slack/modals"
	"github.com/openshift/ci-tools/pkg/slack/modals/modaltesting"
)

type userResponse struct {
	user *slack.User
	err  error
}

type conversationResponse struct {
	channel *slack.Channel
	err     error
}

type fakeClient struct {
	userBehavior  map[string]userResponse
	unwantedUsers []string

	conversationBehavior   map[string]conversationResponse
	unwantedConverstations []string

	*modals.FakeViewUpdater
}

func (f *fakeClient) GetUserInfo(user string) (*slack.User, error) {
	response, registered := f.userBehavior[user]
	if !registered {
		f.unwantedUsers = append(f.unwantedUsers, user)
		return nil, errors.New("no such issue request behavior in fake")
	}
	delete(f.userBehavior, user)
	return response.user, response.err
}

func (f *fakeClient) GetConversationInfo(channelID string, includeLocale bool) (*slack.Channel, error) {
	response, registered := f.conversationBehavior[channelID]
	if !registered {
		f.unwantedConverstations = append(f.unwantedConverstations, channelID)
		return nil, errors.New("no such issue request behavior in fake")
	}
	delete(f.conversationBehavior, channelID)
	return response.channel, response.err
}

func (f *fakeClient) Validate(t *testing.T) {
	for user := range f.userBehavior {
		t.Errorf("fake info getter did not get user request: %v", user)
	}
	for _, user := range f.unwantedUsers {
		t.Errorf("fake info getter got unwanted user request: %v", user)
	}
	for conversation := range f.conversationBehavior {
		t.Errorf("fake info getter did not get conversation request: %v", conversation)
	}
	for _, conversation := range f.unwantedConverstations {
		t.Errorf("fake info getter got unwanted conversation request: %v", conversation)
	}
}

func TestProcessSubmissionHandler(t *testing.T) {
	fake := &fakeClient{
		userBehavior: map[string]userResponse{
			"U01AZU9H0BF": {user: &slack.User{RealName: "The Dude", ID: "U01AZU9H0BF"}},
		},
		unwantedUsers: []string{},
		conversationBehavior: map[string]conversationResponse{
			"C01B31AT7K4": {channel: &slack.Channel{GroupConversation: slack.GroupConversation{Name: "secret", Conversation: slack.Conversation{ID: "C01B31AT7K4"}}}},
		},
		FakeViewUpdater: modals.NewFake([]modals.ViewUpdate{
			{
				ViewUpdateRequest: modals.ViewUpdateRequest{
					View:       modals.JiraView("WHOA-123"),
					ExternalID: "",
					Hash:       "", // updated unconditionally, should be empty
					ViewID:     "V01C0PGEYS3",
				},
				ViewUpdateResponse: modals.ViewUpdateResponse{
					Response: &slack.ViewResponse{},
					Error:    nil,
				},
			},
		}),
	}
	happyPath := modaltesting.SubmissionTestCase{
		Name: "happy path with additional details",
		Filer: jira.NewFake(map[jira.IssueRequest]jira.IssueResponse{
			{
				IssueType: "Story",
				Title:     "api.ci Is Broken Again",
				Description: `h3. Summary
The bootstrap node auto-approver is down for the fiftieth time.

||Name||Link||
|Slack Incident Channel|[#secret|https://coreos.slack.com/archives/C01B31AT7K4]|
|Tracking Bugzilla Bug(s)|https://bugzilla.redhat.com/show_bug.cgi?id=123123123213123|
|SME Name|[The Dude|https://coreos.slack.com/team/U01AZU9H0BF]|

h3. Impact
Nodes are not ready and no jobs run.

h3. Additional Details
So surprising!`,
				Reporter: "U01B31ARZDG",
			}: {
				Issue: &jiraapi.Issue{Key: "WHOA-123"},
				Error: nil,
			},
		}),
		Updater:         fake.FakeViewUpdater,
		ExpectedPayload: []byte(`{"response_action":"update","view":{"type":"modal","title":{"type":"plain_text","text":"Creating Jira Issue..."},"blocks":[{"type":"section","text":{"type":"mrkdwn","text":"A Jira issue is being filed, please do not close this window..."}}],"private_metadata":"jira_pending"}}`),
		ExpectedError:   false,
	}
	modaltesting.ValidateSubmission(t, processSubmissionHandler(happyPath.Filer, fake), happyPath)
	fake.Validate(t)
}

func TestIssueParameters(t *testing.T) {
	fake := &fakeClient{
		userBehavior: map[string]userResponse{
			"U01AZU9H0BF": {err: errors.New("oops")},
			"U01AZU9H0BG": {user: &slack.User{RealName: "The Dude", ID: "U01AZU9H0BG"}},
		},
		unwantedUsers: []string{},
		conversationBehavior: map[string]conversationResponse{
			"C01B31AT7K4": {err: errors.New("oops")},
			"C01B31AT7K5": {channel: &slack.Channel{GroupConversation: slack.GroupConversation{Name: "secret", Conversation: slack.Conversation{ID: "C01B31AT7K5"}}}},
		},
		unwantedConverstations: []string{},
	}
	parameters := issueParameters(fake)
	modaltesting.ValidateBlockIds(t, View(), parameters.Fields...)
	modaltesting.ValidateParameterProcessing(t, parameters, []modaltesting.ProcessTestCase{
		{
			Name:          "can't search for user or channel",
			ExpectedTitle: "api.ci Is Broken Again",
			ExpectedBody: `h3. Summary
The bootstrap node auto-approver is down for the fiftieth time.

||Name||Link||
|Slack Incident Channel|[channel|https://coreos.slack.com/archives/C01B31AT7K4]|
|Tracking Bugzilla Bug(s)|https://bugzilla.redhat.com/show_bug.cgi?id=123123123213123|
|SME Name|[user profile|https://coreos.slack.com/team/U01AZU9H0BF]|

h3. Impact
Nodes are not ready and no jobs run.

h3. Additional Details
So surprising!`,
		},
		{
			Name:          "can search for user or channel",
			ExpectedTitle: "api.ci Is Broken Again",
			ExpectedBody: `h3. Summary
The bootstrap node auto-approver is down for the fiftieth time.

||Name||Link||
|Slack Incident Channel|[#secret|https://coreos.slack.com/archives/C01B31AT7K5]|
|Tracking Bugzilla Bug(s)|https://bugzilla.redhat.com/show_bug.cgi?id=123123123213123|
|SME Name|[The Dude|https://coreos.slack.com/team/U01AZU9H0BG]|

h3. Impact
Nodes are not ready and no jobs run.

h3. Additional Details
So surprising!`,
		},
	})
	fake.Validate(t)
}
