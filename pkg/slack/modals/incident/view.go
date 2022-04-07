package incident

import (
	"fmt"
	"text/template"

	"github.com/sirupsen/logrus"
	"github.com/slack-go/slack"

	"github.com/openshift/ci-tools/pkg/jira"
	"github.com/openshift/ci-tools/pkg/slack/interactions"
	"github.com/openshift/ci-tools/pkg/slack/modals"
	"github.com/openshift/ci-tools/pkg/slack/modals/triage"
)

// Identifier is the view identifier for this modal
const Identifier modals.Identifier = "incident"

const (
	blockIdTriage     = "triage"
	blockIdSummary    = "summary"
	blockIdImpact     = "impact"
	blockIdAdditional = "additional"
	blockIdBugzilla   = "bugzilla"
	blockIdSelectors  = "selectors"
)

// View is the modal view for submitting a new incident tracker to Jira
func View() slack.ModalViewRequest {
	return slack.ModalViewRequest{
		Type:            slack.VTModal,
		PrivateMetadata: string(Identifier),
		Title:           &slack.TextBlockObject{Type: slack.PlainTextType, Text: "Document an Incident"},
		Close:           &slack.TextBlockObject{Type: slack.PlainTextType, Text: "Cancel"},
		Submit:          &slack.TextBlockObject{Type: slack.PlainTextType, Text: "Submit"},
		Blocks: slack.Blocks{BlockSet: []slack.Block{
			&slack.SectionBlock{
				Type: slack.MBTSection,
				Text: &slack.TextBlockObject{
					Type: slack.PlainTextType,
					Text: "Members of the Test Platform team can use this form to document incidents and automatically create incident cards in Jira.",
				},
			},
			&slack.SectionBlock{
				Type: slack.MBTSection,
				Text: &slack.TextBlockObject{
					Type: slack.PlainTextType,
					Text: "Users that wish to report an ongoing incident to engage the Test Platform Triage role should use the incident report form instead.",
				},
				Accessory: &slack.Accessory{
					ButtonElement: &slack.ButtonBlockElement{
						Type:  slack.METButton,
						Text:  &slack.TextBlockObject{Type: slack.PlainTextType, Text: "Triage an Incident"},
						Value: blockIdTriage,
					},
				},
			},
			&slack.DividerBlock{
				Type: slack.MBTDivider,
			},
			&slack.InputBlock{
				Type:    slack.MBTInput,
				BlockID: modals.BlockIdTitle,
				Label:   &slack.TextBlockObject{Type: slack.PlainTextType, Text: "Provide a title for this incident:"},
				Element: &slack.PlainTextInputBlockElement{Type: slack.METPlainTextInput},
			},
			&slack.InputBlock{
				Type:    slack.MBTInput,
				BlockID: blockIdSummary,
				Label:   &slack.TextBlockObject{Type: slack.PlainTextType, Text: "Summarize what is happening:"},
				Element: &slack.PlainTextInputBlockElement{Type: slack.METPlainTextInput, Multiline: true},
			},
			&slack.InputBlock{
				Type:    slack.MBTInput,
				BlockID: blockIdImpact,
				Label:   &slack.TextBlockObject{Type: slack.PlainTextType, Text: "Explain the impact:"},
				Element: &slack.PlainTextInputBlockElement{Type: slack.METPlainTextInput, Multiline: true},
			},
			&slack.InputBlock{
				Type:    slack.MBTInput,
				BlockID: blockIdBugzilla,
				Label:   &slack.TextBlockObject{Type: slack.PlainTextType, Text: "Link the Bugzilla bug:"},
				Element: &slack.PlainTextInputBlockElement{Type: slack.METPlainTextInput},
			},
			&slack.ActionBlock{
				Type:    slack.MBTAction,
				BlockID: blockIdSelectors,
				Elements: &slack.BlockElements{
					ElementSet: []slack.BlockElement{
						&slack.SelectBlockElement{
							Type:        slack.OptTypeChannels,
							Placeholder: &slack.TextBlockObject{Type: slack.PlainTextType, Text: "Select the incident channel..."},
						},
						&slack.SelectBlockElement{
							Type:        slack.OptTypeUser,
							Placeholder: &slack.TextBlockObject{Type: slack.PlainTextType, Text: "Select the subject matter expert..."},
						},
					},
				},
			},
			&slack.InputBlock{
				Type:     slack.MBTInput,
				BlockID:  blockIdAdditional,
				Optional: true,
				Label:    &slack.TextBlockObject{Type: slack.PlainTextType, Text: "Provide any additional information:"},
				Element:  &slack.PlainTextInputBlockElement{Type: slack.METPlainTextInput, Multiline: true},
			},
		}},
	}
}

// triageButtonHandler redirects the user to the triage flow
// if they request it via button push
func triageButtonHandler(updater modals.ViewUpdater) interactions.Handler {
	return interactions.HandlerFromPartial(modals.UpdateViewForButtonPress(string(Identifier)+".bug", blockIdTriage, updater, triage.View()))
}

type slackClient interface {
	infoGetter
	modals.ViewUpdater
}

func issueParameters(client infoGetter) modals.JiraIssueParameters {
	return modals.JiraIssueParameters{
		Id:        Identifier,
		IssueType: jira.IssueTypeStory,
		Template: template.Must(template.New(string(Identifier)).Funcs(slackEntityFormatFuncs(client)).Parse(`h3. Summary
{{ .` + blockIdSummary + ` }}

||Name||Link||
|Slack Incident Channel|{{ slackChannelLink .` + blockIdSelectors + `_channels_select }}|
|Tracking Bugzilla Bug(s)|{{ .` + blockIdBugzilla + ` }}|
|SME Name|{{ slackUserLink .` + blockIdSelectors + `_users_select }}|

h3. Impact
{{ .` + blockIdImpact + ` }}

{{- if .` + blockIdAdditional + ` }}

h3. Additional Details
{{ .` + blockIdAdditional + ` }}
{{- end }}`)),
		Fields: []string{modals.BlockIdTitle, blockIdSummary, blockIdSelectors, blockIdImpact, blockIdBugzilla, blockIdAdditional},
	}
}

type infoGetter interface {
	GetUserInfo(user string) (*slack.User, error)
	GetConversationInfo(channelID string, includeLocale bool) (*slack.Channel, error)
}

func slackEntityFormatFuncs(client infoGetter) template.FuncMap {
	return template.FuncMap{
		"slackUserLink": func(input string) string {
			user, err := client.GetUserInfo(input)
			if err != nil {
				logrus.WithError(err).Warn("Could not look up user-provided Slack user ID for pretty printing.")
				return fmt.Sprintf("[user profile|https://coreos.slack.com/team/%s]", input)
			}
			return fmt.Sprintf("[%s|https://coreos.slack.com/team/%s]", user.RealName, user.ID)
		},
		"slackChannelLink": func(input string) string {
			channel, err := client.GetConversationInfo(input, false)
			if err != nil {
				logrus.WithError(err).Warn("Could not look up user-provided Slack channel ID for pretty printing.")
				return fmt.Sprintf("[channel|https://coreos.slack.com/archives/%s]", input)
			}
			return fmt.Sprintf("[#%s|https://coreos.slack.com/archives/%s]", channel.Name, channel.ID)
		},
	}
}

// processSubmissionHandler files a Jira issue for this form
func processSubmissionHandler(filer jira.IssueFiler, client slackClient) interactions.Handler {
	return modals.ToJiraIssue(issueParameters(client), filer, client)
}

// Register creates a registration entry for the incident report form
func Register(filer jira.IssueFiler, client *slack.Client) *modals.FlowWithViewAndFollowUps {
	return modals.ForView(Identifier, View()).WithFollowUps(map[slack.InteractionType]interactions.Handler{
		slack.InteractionTypeBlockActions:   triageButtonHandler(client),
		slack.InteractionTypeViewSubmission: processSubmissionHandler(filer, client), // TODO: ensure only DPTP can submit this form
	})
}
