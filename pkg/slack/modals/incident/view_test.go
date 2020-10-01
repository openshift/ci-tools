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
		Name:     "happy path with additional details",
		Callback: []byte(`{"type":"view_submission","token":"Rn1sVT099UjUeKclB3PkLT8t","callback_id":"","response_url":"","trigger_id":"1416811719878.1377252349923.7aa108d6c2287f7dcc2e6b381c31faaf","action_ts":"","team":{"id":"T01B37EA9T5","name":"","domain":"dptp-robot-testing"},"channel":{"id":"","created":0,"is_open":false,"is_group":false,"is_shared":false,"is_im":false,"is_ext_shared":false,"is_org_shared":false,"is_pending_ext_shared":false,"is_private":false,"is_mpim":false,"unlinked":0,"name_normalized":"","num_members":0,"priority":0,"user":"","name":"","creator":"","is_archived":false,"members":null,"topic":{"value":"","creator":"","last_set":0},"purpose":{"value":"","creator":"","last_set":0},"is_channel":false,"is_general":false,"is_member":false,"locale":""},"user":{"id":"U01B31ARZDG","team_id":"T01B37EA9T5","name":"skuznets","deleted":false,"color":"","real_name":"","tz_label":"","tz_offset":0,"profile":{"first_name":"","last_name":"","real_name":"","real_name_normalized":"","display_name":"","display_name_normalized":"","email":"","skype":"","phone":"","image_24":"","image_32":"","image_48":"","image_72":"","image_192":"","image_original":"","title":"","status_expiration":0,"team":"","fields":[]},"is_bot":false,"is_admin":false,"is_owner":false,"is_primary_owner":false,"is_restricted":false,"is_ultra_restricted":false,"is_stranger":false,"is_app_user":false,"is_invited_user":false,"has_2fa":false,"has_files":false,"presence":"","locale":"","updated":0,"enterprise_user":{"id":"","enterprise_id":"","enterprise_name":"","is_admin":false,"is_owner":false,"teams":null}},"original_message":{"replace_original":false,"delete_original":false,"blocks":null},"message":{"replace_original":false,"delete_original":false,"blocks":null},"name":"","value":"","message_ts":"","attachment_id":"","actions":[],"view":{"ok":false,"error":"","id":"V01C0PGEYS3","team_id":"T01B37EA9T5","type":"modal","title":{"type":"plain_text","text":"Document an Incident","emoji":true},"close":{"type":"plain_text","text":"Cancel","emoji":true},"submit":{"type":"plain_text","text":"Submit","emoji":true},"blocks":[{"type":"section","text":{"type":"plain_text","text":"Members of the Test Platform team can use this form to document incidents and automatically create incident cards in Jira.","emoji":true},"block_id":"sOd"},{"type":"section","text":{"type":"plain_text","text":"Users that wish to report an ongoing incident to engage the Test Platform Triage role should use the incident report form instead.","emoji":true},"block_id":"y0q","accessory":{"type":"button","text":{"type":"plain_text","text":"Triage an Incident","emoji":true},"action_id":"=zRGG","value":"triage"}},{"type":"divider","block_id":"h8c"},{"type":"input","block_id":"title","label":{"type":"plain_text","text":"Provide a title for this incident:","emoji":true},"element":{"type":"plain_text_input","action_id":"fm9x"}},{"type":"input","block_id":"summary","label":{"type":"plain_text","text":"Summarize what is happening:","emoji":true},"element":{"type":"plain_text_input","action_id":"vZs","multiline":true}},{"type":"input","block_id":"impact","label":{"type":"plain_text","text":"Explain the impact:","emoji":true},"element":{"type":"plain_text_input","action_id":"mSYg","multiline":true}},{"type":"input","block_id":"bugzilla","label":{"type":"plain_text","text":"Link the Bugzilla bug:","emoji":true},"element":{"type":"plain_text_input","action_id":"up8"}},{"type":"actions","block_id":"selectors","elements":[{"type":"channels_select","placeholder":{"type":"plain_text","text":"Select the incident channel...","emoji":true},"action_id":"XeNO"},{"type":"users_select","placeholder":{"type":"plain_text","text":"Select the subject matter expert...","emoji":true},"action_id":"Os7y"}]},{"type":"input","block_id":"additional","label":{"type":"plain_text","text":"Provide any additional information:","emoji":true},"element":{"type":"plain_text_input","action_id":"3rP+","multiline":true},"optional":true}],"private_metadata":"incident","callback_id":"","state":{"values":{"additional":{"3rP+":{"action_id":"","block_id":"","type":"plain_text_input","text":{"type":"","text":""},"value":"So surprising!","action_ts":"","selected_option":{"text":null,"value":""},"selected_options":null,"selected_user":"","selected_users":null,"selected_channel":"","selected_channels":null,"selected_conversation":"","selected_conversations":null,"selected_date":"","initial_option":{"text":null,"value":""},"initial_user":"","initial_channel":"","initial_conversation":"","initial_date":""}},"bugzilla":{"up8":{"action_id":"","block_id":"","type":"plain_text_input","text":{"type":"","text":""},"value":"https://bugzilla.redhat.com/show_bug.cgi?id=123123123213123","action_ts":"","selected_option":{"text":null,"value":""},"selected_options":null,"selected_user":"","selected_users":null,"selected_channel":"","selected_channels":null,"selected_conversation":"","selected_conversations":null,"selected_date":"","initial_option":{"text":null,"value":""},"initial_user":"","initial_channel":"","initial_conversation":"","initial_date":""}},"impact":{"mSYg":{"action_id":"","block_id":"","type":"plain_text_input","text":{"type":"","text":""},"value":"Nodes are not ready and no jobs run.","action_ts":"","selected_option":{"text":null,"value":""},"selected_options":null,"selected_user":"","selected_users":null,"selected_channel":"","selected_channels":null,"selected_conversation":"","selected_conversations":null,"selected_date":"","initial_option":{"text":null,"value":""},"initial_user":"","initial_channel":"","initial_conversation":"","initial_date":""}},"selectors":{"Os7y":{"action_id":"","block_id":"","type":"users_select","text":{"type":"","text":""},"value":"","action_ts":"","selected_option":{"text":null,"value":""},"selected_options":null,"selected_user":"U01AZU9H0BF","selected_users":null,"selected_channel":"","selected_channels":null,"selected_conversation":"","selected_conversations":null,"selected_date":"","initial_option":{"text":null,"value":""},"initial_user":"","initial_channel":"","initial_conversation":"","initial_date":""},"XeNO":{"action_id":"","block_id":"","type":"channels_select","text":{"type":"","text":""},"value":"","action_ts":"","selected_option":{"text":null,"value":""},"selected_options":null,"selected_user":"","selected_users":null,"selected_channel":"C01B31AT7K4","selected_channels":null,"selected_conversation":"","selected_conversations":null,"selected_date":"","initial_option":{"text":null,"value":""},"initial_user":"","initial_channel":"","initial_conversation":"","initial_date":""}},"summary":{"vZs":{"action_id":"","block_id":"","type":"plain_text_input","text":{"type":"","text":""},"value":"The bootstrap node auto-approver is down for the fiftieth time.","action_ts":"","selected_option":{"text":null,"value":""},"selected_options":null,"selected_user":"","selected_users":null,"selected_channel":"","selected_channels":null,"selected_conversation":"","selected_conversations":null,"selected_date":"","initial_option":{"text":null,"value":""},"initial_user":"","initial_channel":"","initial_conversation":"","initial_date":""}},"title":{"fm9x":{"action_id":"","block_id":"","type":"plain_text_input","text":{"type":"","text":""},"value":"api.ci Is Broken Again","action_ts":"","selected_option":{"text":null,"value":""},"selected_options":null,"selected_user":"","selected_users":null,"selected_channel":"","selected_channels":null,"selected_conversation":"","selected_conversations":null,"selected_date":"","initial_option":{"text":null,"value":""},"initial_user":"","initial_channel":"","initial_conversation":"","initial_date":""}}}},"hash":"1602540226.eEIQjfOU","clear_on_close":false,"notify_on_close":false,"root_view_id":"V01C0PGEYS3","previous_view_id":"","app_id":"A01BJF00CAD","external_id":"","bot_id":"B01B63T6ZFD"},"action_id":"","api_app_id":"A01BJF00CAD","block_id":"","container":{"type":"","view_id":"","message_ts":"","attachment_id":0,"channel_id":"","is_ephemeral":false,"is_app_unfurl":false},"submission":null,"hash":"","is_cleared":false}`),
		Filer: jira.NewFake(map[jira.IssueRequest]jira.IssueResponse{
			{
				IssueType: "Incident",
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
			Callback:      []byte(`{"type":"view_submission","token":"Rn1sVT099UjUeKclB3PkLT8t","callback_id":"","response_url":"","trigger_id":"1416811719878.1377252349923.7aa108d6c2287f7dcc2e6b381c31faaf","action_ts":"","team":{"id":"T01B37EA9T5","name":"","domain":"dptp-robot-testing"},"channel":{"id":"","created":0,"is_open":false,"is_group":false,"is_shared":false,"is_im":false,"is_ext_shared":false,"is_org_shared":false,"is_pending_ext_shared":false,"is_private":false,"is_mpim":false,"unlinked":0,"name_normalized":"","num_members":0,"priority":0,"user":"","name":"","creator":"","is_archived":false,"members":null,"topic":{"value":"","creator":"","last_set":0},"purpose":{"value":"","creator":"","last_set":0},"is_channel":false,"is_general":false,"is_member":false,"locale":""},"user":{"id":"U01B31ARZDG","team_id":"T01B37EA9T5","name":"skuznets","deleted":false,"color":"","real_name":"","tz_label":"","tz_offset":0,"profile":{"first_name":"","last_name":"","real_name":"","real_name_normalized":"","display_name":"","display_name_normalized":"","email":"","skype":"","phone":"","image_24":"","image_32":"","image_48":"","image_72":"","image_192":"","image_original":"","title":"","status_expiration":0,"team":"","fields":[]},"is_bot":false,"is_admin":false,"is_owner":false,"is_primary_owner":false,"is_restricted":false,"is_ultra_restricted":false,"is_stranger":false,"is_app_user":false,"is_invited_user":false,"has_2fa":false,"has_files":false,"presence":"","locale":"","updated":0,"enterprise_user":{"id":"","enterprise_id":"","enterprise_name":"","is_admin":false,"is_owner":false,"teams":null}},"original_message":{"replace_original":false,"delete_original":false,"blocks":null},"message":{"replace_original":false,"delete_original":false,"blocks":null},"name":"","value":"","message_ts":"","attachment_id":"","actions":[],"view":{"ok":false,"error":"","id":"V01C0PGEYS3","team_id":"T01B37EA9T5","type":"modal","title":{"type":"plain_text","text":"Document an Incident","emoji":true},"close":{"type":"plain_text","text":"Cancel","emoji":true},"submit":{"type":"plain_text","text":"Submit","emoji":true},"blocks":[{"type":"section","text":{"type":"plain_text","text":"Members of the Test Platform team can use this form to document incidents and automatically create incident cards in Jira.","emoji":true},"block_id":"sOd"},{"type":"section","text":{"type":"plain_text","text":"Users that wish to report an ongoing incident to engage the Test Platform Triage role should use the incident report form instead.","emoji":true},"block_id":"y0q","accessory":{"type":"button","text":{"type":"plain_text","text":"Triage an Incident","emoji":true},"action_id":"=zRGG","value":"triage"}},{"type":"divider","block_id":"h8c"},{"type":"input","block_id":"title","label":{"type":"plain_text","text":"Provide a title for this incident:","emoji":true},"element":{"type":"plain_text_input","action_id":"fm9x"}},{"type":"input","block_id":"summary","label":{"type":"plain_text","text":"Summarize what is happening:","emoji":true},"element":{"type":"plain_text_input","action_id":"vZs","multiline":true}},{"type":"input","block_id":"impact","label":{"type":"plain_text","text":"Explain the impact:","emoji":true},"element":{"type":"plain_text_input","action_id":"mSYg","multiline":true}},{"type":"input","block_id":"bugzilla","label":{"type":"plain_text","text":"Link the Bugzilla bug:","emoji":true},"element":{"type":"plain_text_input","action_id":"up8"}},{"type":"actions","block_id":"selectors","elements":[{"type":"channels_select","placeholder":{"type":"plain_text","text":"Select the incident channel...","emoji":true},"action_id":"XeNO"},{"type":"users_select","placeholder":{"type":"plain_text","text":"Select the subject matter expert...","emoji":true},"action_id":"Os7y"}]},{"type":"input","block_id":"additional","label":{"type":"plain_text","text":"Provide any additional information:","emoji":true},"element":{"type":"plain_text_input","action_id":"3rP+","multiline":true},"optional":true}],"private_metadata":"incident","callback_id":"","state":{"values":{"additional":{"3rP+":{"action_id":"","block_id":"","type":"plain_text_input","text":{"type":"","text":""},"value":"So surprising!","action_ts":"","selected_option":{"text":null,"value":""},"selected_options":null,"selected_user":"","selected_users":null,"selected_channel":"","selected_channels":null,"selected_conversation":"","selected_conversations":null,"selected_date":"","initial_option":{"text":null,"value":""},"initial_user":"","initial_channel":"","initial_conversation":"","initial_date":""}},"bugzilla":{"up8":{"action_id":"","block_id":"","type":"plain_text_input","text":{"type":"","text":""},"value":"https://bugzilla.redhat.com/show_bug.cgi?id=123123123213123","action_ts":"","selected_option":{"text":null,"value":""},"selected_options":null,"selected_user":"","selected_users":null,"selected_channel":"","selected_channels":null,"selected_conversation":"","selected_conversations":null,"selected_date":"","initial_option":{"text":null,"value":""},"initial_user":"","initial_channel":"","initial_conversation":"","initial_date":""}},"impact":{"mSYg":{"action_id":"","block_id":"","type":"plain_text_input","text":{"type":"","text":""},"value":"Nodes are not ready and no jobs run.","action_ts":"","selected_option":{"text":null,"value":""},"selected_options":null,"selected_user":"","selected_users":null,"selected_channel":"","selected_channels":null,"selected_conversation":"","selected_conversations":null,"selected_date":"","initial_option":{"text":null,"value":""},"initial_user":"","initial_channel":"","initial_conversation":"","initial_date":""}},"selectors":{"Os7y":{"action_id":"","block_id":"","type":"users_select","text":{"type":"","text":""},"value":"","action_ts":"","selected_option":{"text":null,"value":""},"selected_options":null,"selected_user":"U01AZU9H0BF","selected_users":null,"selected_channel":"","selected_channels":null,"selected_conversation":"","selected_conversations":null,"selected_date":"","initial_option":{"text":null,"value":""},"initial_user":"","initial_channel":"","initial_conversation":"","initial_date":""},"XeNO":{"action_id":"","block_id":"","type":"channels_select","text":{"type":"","text":""},"value":"","action_ts":"","selected_option":{"text":null,"value":""},"selected_options":null,"selected_user":"","selected_users":null,"selected_channel":"C01B31AT7K4","selected_channels":null,"selected_conversation":"","selected_conversations":null,"selected_date":"","initial_option":{"text":null,"value":""},"initial_user":"","initial_channel":"","initial_conversation":"","initial_date":""}},"summary":{"vZs":{"action_id":"","block_id":"","type":"plain_text_input","text":{"type":"","text":""},"value":"The bootstrap node auto-approver is down for the fiftieth time.","action_ts":"","selected_option":{"text":null,"value":""},"selected_options":null,"selected_user":"","selected_users":null,"selected_channel":"","selected_channels":null,"selected_conversation":"","selected_conversations":null,"selected_date":"","initial_option":{"text":null,"value":""},"initial_user":"","initial_channel":"","initial_conversation":"","initial_date":""}},"title":{"fm9x":{"action_id":"","block_id":"","type":"plain_text_input","text":{"type":"","text":""},"value":"api.ci Is Broken Again","action_ts":"","selected_option":{"text":null,"value":""},"selected_options":null,"selected_user":"","selected_users":null,"selected_channel":"","selected_channels":null,"selected_conversation":"","selected_conversations":null,"selected_date":"","initial_option":{"text":null,"value":""},"initial_user":"","initial_channel":"","initial_conversation":"","initial_date":""}}}},"hash":"1602540226.eEIQjfOU","clear_on_close":false,"notify_on_close":false,"root_view_id":"V01C0PGEYS3","previous_view_id":"","app_id":"A01BJF00CAD","external_id":"","bot_id":"B01B63T6ZFD"},"action_id":"","api_app_id":"A01BJF00CAD","block_id":"","container":{"type":"","view_id":"","message_ts":"","attachment_id":0,"channel_id":"","is_ephemeral":false,"is_app_unfurl":false},"submission":null,"hash":"","is_cleared":false}`),
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
			Callback:      []byte(`{"type":"view_submission","token":"Rn1sVT099UjUeKclB3PkLT8t","callback_id":"","response_url":"","trigger_id":"1416811719878.1377252349923.7aa108d6c2287f7dcc2e6b381c31faaf","action_ts":"","team":{"id":"T01B37EA9T5","name":"","domain":"dptp-robot-testing"},"channel":{"id":"","created":0,"is_open":false,"is_group":false,"is_shared":false,"is_im":false,"is_ext_shared":false,"is_org_shared":false,"is_pending_ext_shared":false,"is_private":false,"is_mpim":false,"unlinked":0,"name_normalized":"","num_members":0,"priority":0,"user":"","name":"","creator":"","is_archived":false,"members":null,"topic":{"value":"","creator":"","last_set":0},"purpose":{"value":"","creator":"","last_set":0},"is_channel":false,"is_general":false,"is_member":false,"locale":""},"user":{"id":"U01B31ARZDG","team_id":"T01B37EA9T5","name":"skuznets","deleted":false,"color":"","real_name":"","tz_label":"","tz_offset":0,"profile":{"first_name":"","last_name":"","real_name":"","real_name_normalized":"","display_name":"","display_name_normalized":"","email":"","skype":"","phone":"","image_24":"","image_32":"","image_48":"","image_72":"","image_192":"","image_original":"","title":"","status_expiration":0,"team":"","fields":[]},"is_bot":false,"is_admin":false,"is_owner":false,"is_primary_owner":false,"is_restricted":false,"is_ultra_restricted":false,"is_stranger":false,"is_app_user":false,"is_invited_user":false,"has_2fa":false,"has_files":false,"presence":"","locale":"","updated":0,"enterprise_user":{"id":"","enterprise_id":"","enterprise_name":"","is_admin":false,"is_owner":false,"teams":null}},"original_message":{"replace_original":false,"delete_original":false,"blocks":null},"message":{"replace_original":false,"delete_original":false,"blocks":null},"name":"","value":"","message_ts":"","attachment_id":"","actions":[],"view":{"ok":false,"error":"","id":"V01C0PGEYS3","team_id":"T01B37EA9T5","type":"modal","title":{"type":"plain_text","text":"Document an Incident","emoji":true},"close":{"type":"plain_text","text":"Cancel","emoji":true},"submit":{"type":"plain_text","text":"Submit","emoji":true},"blocks":[{"type":"section","text":{"type":"plain_text","text":"Members of the Test Platform team can use this form to document incidents and automatically create incident cards in Jira.","emoji":true},"block_id":"sOd"},{"type":"section","text":{"type":"plain_text","text":"Users that wish to report an ongoing incident to engage the Test Platform Triage role should use the incident report form instead.","emoji":true},"block_id":"y0q","accessory":{"type":"button","text":{"type":"plain_text","text":"Triage an Incident","emoji":true},"action_id":"=zRGG","value":"triage"}},{"type":"divider","block_id":"h8c"},{"type":"input","block_id":"title","label":{"type":"plain_text","text":"Provide a title for this incident:","emoji":true},"element":{"type":"plain_text_input","action_id":"fm9x"}},{"type":"input","block_id":"summary","label":{"type":"plain_text","text":"Summarize what is happening:","emoji":true},"element":{"type":"plain_text_input","action_id":"vZs","multiline":true}},{"type":"input","block_id":"impact","label":{"type":"plain_text","text":"Explain the impact:","emoji":true},"element":{"type":"plain_text_input","action_id":"mSYg","multiline":true}},{"type":"input","block_id":"bugzilla","label":{"type":"plain_text","text":"Link the Bugzilla bug:","emoji":true},"element":{"type":"plain_text_input","action_id":"up8"}},{"type":"actions","block_id":"selectors","elements":[{"type":"channels_select","placeholder":{"type":"plain_text","text":"Select the incident channel...","emoji":true},"action_id":"XeNO"},{"type":"users_select","placeholder":{"type":"plain_text","text":"Select the subject matter expert...","emoji":true},"action_id":"Os7y"}]},{"type":"input","block_id":"additional","label":{"type":"plain_text","text":"Provide any additional information:","emoji":true},"element":{"type":"plain_text_input","action_id":"3rP+","multiline":true},"optional":true}],"private_metadata":"incident","callback_id":"","state":{"values":{"additional":{"3rP+":{"action_id":"","block_id":"","type":"plain_text_input","text":{"type":"","text":""},"value":"So surprising!","action_ts":"","selected_option":{"text":null,"value":""},"selected_options":null,"selected_user":"","selected_users":null,"selected_channel":"","selected_channels":null,"selected_conversation":"","selected_conversations":null,"selected_date":"","initial_option":{"text":null,"value":""},"initial_user":"","initial_channel":"","initial_conversation":"","initial_date":""}},"bugzilla":{"up8":{"action_id":"","block_id":"","type":"plain_text_input","text":{"type":"","text":""},"value":"https://bugzilla.redhat.com/show_bug.cgi?id=123123123213123","action_ts":"","selected_option":{"text":null,"value":""},"selected_options":null,"selected_user":"","selected_users":null,"selected_channel":"","selected_channels":null,"selected_conversation":"","selected_conversations":null,"selected_date":"","initial_option":{"text":null,"value":""},"initial_user":"","initial_channel":"","initial_conversation":"","initial_date":""}},"impact":{"mSYg":{"action_id":"","block_id":"","type":"plain_text_input","text":{"type":"","text":""},"value":"Nodes are not ready and no jobs run.","action_ts":"","selected_option":{"text":null,"value":""},"selected_options":null,"selected_user":"","selected_users":null,"selected_channel":"","selected_channels":null,"selected_conversation":"","selected_conversations":null,"selected_date":"","initial_option":{"text":null,"value":""},"initial_user":"","initial_channel":"","initial_conversation":"","initial_date":""}},"selectors":{"Os7y":{"action_id":"","block_id":"","type":"users_select","text":{"type":"","text":""},"value":"","action_ts":"","selected_option":{"text":null,"value":""},"selected_options":null,"selected_user":"U01AZU9H0BG","selected_users":null,"selected_channel":"","selected_channels":null,"selected_conversation":"","selected_conversations":null,"selected_date":"","initial_option":{"text":null,"value":""},"initial_user":"","initial_channel":"","initial_conversation":"","initial_date":""},"XeNO":{"action_id":"","block_id":"","type":"channels_select","text":{"type":"","text":""},"value":"","action_ts":"","selected_option":{"text":null,"value":""},"selected_options":null,"selected_user":"","selected_users":null,"selected_channel":"C01B31AT7K5","selected_channels":null,"selected_conversation":"","selected_conversations":null,"selected_date":"","initial_option":{"text":null,"value":""},"initial_user":"","initial_channel":"","initial_conversation":"","initial_date":""}},"summary":{"vZs":{"action_id":"","block_id":"","type":"plain_text_input","text":{"type":"","text":""},"value":"The bootstrap node auto-approver is down for the fiftieth time.","action_ts":"","selected_option":{"text":null,"value":""},"selected_options":null,"selected_user":"","selected_users":null,"selected_channel":"","selected_channels":null,"selected_conversation":"","selected_conversations":null,"selected_date":"","initial_option":{"text":null,"value":""},"initial_user":"","initial_channel":"","initial_conversation":"","initial_date":""}},"title":{"fm9x":{"action_id":"","block_id":"","type":"plain_text_input","text":{"type":"","text":""},"value":"api.ci Is Broken Again","action_ts":"","selected_option":{"text":null,"value":""},"selected_options":null,"selected_user":"","selected_users":null,"selected_channel":"","selected_channels":null,"selected_conversation":"","selected_conversations":null,"selected_date":"","initial_option":{"text":null,"value":""},"initial_user":"","initial_channel":"","initial_conversation":"","initial_date":""}}}},"hash":"1602540226.eEIQjfOU","clear_on_close":false,"notify_on_close":false,"root_view_id":"V01C0PGEYS3","previous_view_id":"","app_id":"A01BJF00CAD","external_id":"","bot_id":"B01B63T6ZFD"},"action_id":"","api_app_id":"A01BJF00CAD","block_id":"","container":{"type":"","view_id":"","message_ts":"","attachment_id":0,"channel_id":"","is_ephemeral":false,"is_app_unfurl":false},"submission":null,"hash":"","is_cleared":false}`),
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
