package enhancement

import (
	"text/template"

	"github.com/slack-go/slack"

	localjira "github.com/openshift/ci-tools/pkg/jira"
	"github.com/openshift/ci-tools/pkg/slack/interactions"
	"github.com/openshift/ci-tools/pkg/slack/modals"
)

// Identifier is the view identifier for this modal
const Identifier modals.Identifier = "enhancement"

const (
	blockIdAsA                = "as_a"
	blockIdIWant              = "i_want"
	blockIdSoThat             = "so_that"
	blockIdSummary            = "summary"
	blockIdImpact             = "impact"
	blockIdAcceptanceCriteria = "acceptance_criteria"
	blockIdImplementation     = "implementation"
)

// View is the modal view for submitting a new enhancement card to Jira
func View() slack.ModalViewRequest {
	return slack.ModalViewRequest{
		Type:            slack.VTModal,
		PrivateMetadata: string(Identifier),
		Title:           &slack.TextBlockObject{Type: slack.PlainTextType, Text: "Request an Enhancement"},
		Close:           &slack.TextBlockObject{Type: slack.PlainTextType, Text: "Cancel"},
		Submit:          &slack.TextBlockObject{Type: slack.PlainTextType, Text: "Submit"},
		Blocks: slack.Blocks{BlockSet: []slack.Block{
			&slack.SectionBlock{
				Type: slack.MBTSection,
				Text: &slack.TextBlockObject{
					Type: slack.PlainTextType,
					Text: "The Test Platform team is committed to improving the developer productivity across the OpenShift organization. Use this form to request enhancements or new features to improve your development workflows.",
				},
			},
			&slack.DividerBlock{
				Type: slack.MBTDivider,
			},
			&slack.InputBlock{
				Type:    slack.MBTInput,
				BlockID: modals.BlockIdTitle,
				Label:   &slack.TextBlockObject{Type: slack.PlainTextType, Text: "Provide a title for this enhancement:"},
				Element: &slack.PlainTextInputBlockElement{Type: slack.METPlainTextInput},
			},
			&slack.SectionBlock{
				Type: slack.MBTSection,
				Text: &slack.TextBlockObject{
					Type: slack.PlainTextType,
					Text: "Provide a user story.",
				},
			},
			&slack.InputBlock{
				Type:    slack.MBTInput,
				BlockID: blockIdAsA,
				Label:   &slack.TextBlockObject{Type: slack.PlainTextType, Text: "As a..."},
				Element: &slack.PlainTextInputBlockElement{Type: slack.METPlainTextInput},
			},
			&slack.InputBlock{
				Type:    slack.MBTInput,
				BlockID: blockIdIWant,
				Label:   &slack.TextBlockObject{Type: slack.PlainTextType, Text: "I want..."},
				Element: &slack.PlainTextInputBlockElement{Type: slack.METPlainTextInput},
			},
			&slack.InputBlock{
				Type:    slack.MBTInput,
				BlockID: blockIdSoThat,
				Label:   &slack.TextBlockObject{Type: slack.PlainTextType, Text: "So that..."},
				Element: &slack.PlainTextInputBlockElement{Type: slack.METPlainTextInput},
			},
			&slack.InputBlock{
				Type:    slack.MBTInput,
				BlockID: blockIdSummary,
				Label:   &slack.TextBlockObject{Type: slack.PlainTextType, Text: "Provide context on why this is a need and any specifics on the requirement:"},
				Element: &slack.PlainTextInputBlockElement{Type: slack.METPlainTextInput, Multiline: true},
			},
			&slack.InputBlock{
				Type:    slack.MBTInput,
				BlockID: blockIdImpact,
				Label:   &slack.TextBlockObject{Type: slack.PlainTextType, Text: "Explain how many developers would be impacted by this enhancement and to what extent their workflows would be improved:"},
				Element: &slack.PlainTextInputBlockElement{Type: slack.METPlainTextInput, Multiline: true},
			},
			&slack.InputBlock{
				Type:    slack.MBTInput,
				BlockID: blockIdAcceptanceCriteria,
				Label:   &slack.TextBlockObject{Type: slack.PlainTextType, Text: "Provide acceptance criteria (one per line) for this feature, focusing on what is to be achieved, not how:"},
				Element: &slack.PlainTextInputBlockElement{Type: slack.METPlainTextInput, Multiline: true},
			},
			&slack.InputBlock{
				Type:     slack.MBTInput,
				BlockID:  blockIdImplementation,
				Optional: true,
				Label:    &slack.TextBlockObject{Type: slack.PlainTextType, Text: "Provide any implementation notes:"},
				Element:  &slack.PlainTextInputBlockElement{Type: slack.METPlainTextInput, Multiline: true},
			},
		}},
	}
}

func issueParameters() modals.JiraIssueParameters {
	return modals.JiraIssueParameters{
		Id:        Identifier,
		IssueType: localjira.IssueTypeStory,
		Template: template.Must(template.New(string(Identifier)).Funcs(modals.BulletListFunc()).Parse(`h3. Overview
As a {{ .` + blockIdAsA + ` }}
I want {{ .` + blockIdIWant + ` }}
So that {{ .` + blockIdSoThat + ` }}

h3. Summary
{{ .` + blockIdSummary + ` }}

h3. Impact
{{ .` + blockIdImpact + ` }}

h3. Acceptance Criteria
{{ toBulletList .` + blockIdAcceptanceCriteria + ` }}

{{- if .` + blockIdImplementation + ` }}

h3. Implementation Details
{{ .` + blockIdImplementation + ` }}
{{- end }}`)),
		Fields: []string{modals.BlockIdTitle, blockIdAsA, blockIdIWant, blockIdSoThat, blockIdSummary, blockIdImpact, blockIdAcceptanceCriteria, blockIdImplementation},
		CustomFields: map[string]interface{}{
			localjira.ActivityTypeFieldID: localjira.ActivityTypeEnhancement,
		},
	}
}

// processSubmissionHandler files a Jira issue for this form
func processSubmissionHandler(filer localjira.IssueFiler, updater modals.ViewUpdater) interactions.Handler {
	return modals.ToJiraIssue(issueParameters(), filer, updater)
}

// Register creates a registration entry for the enhancment request form
func Register(filer localjira.IssueFiler, client *slack.Client) *modals.FlowWithViewAndFollowUps {
	return modals.ForView(Identifier, View()).WithFollowUps(map[slack.InteractionType]interactions.Handler{
		slack.InteractionTypeViewSubmission: processSubmissionHandler(filer, client),
	})
}
