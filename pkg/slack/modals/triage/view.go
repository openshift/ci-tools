package triage

import (
	"github.com/slack-go/slack"

	"github.com/openshift/ci-tools/pkg/jira"
	"github.com/openshift/ci-tools/pkg/slack/interactions"
	"github.com/openshift/ci-tools/pkg/slack/modals"
)

// Identifier is the view identifier for this modal
const Identifier modals.Identifier = "triage"

// View is the modal view for submitting a new request to the triage engineer
func View() slack.ModalViewRequest {
	return slack.ModalViewRequest{
		Type:            slack.VTModal,
		PrivateMetadata: string(Identifier),
		Title:           &slack.TextBlockObject{Type: slack.PlainTextType, Text: "Triage an Incident"},
		Close:           &slack.TextBlockObject{Type: slack.PlainTextType, Text: "Cancel"},
		Submit:          &slack.TextBlockObject{Type: slack.PlainTextType, Text: "Submit"},
		Blocks: slack.Blocks{BlockSet: []slack.Block{
			&slack.SectionBlock{
				Type: slack.MBTSection,
				Text: &slack.TextBlockObject{
					Type: slack.PlainTextType,
					Text: "This feature is not yet implemented.",
				},
			},
		}},
	}
}

// Register creates a registration entry for the triage engineer engagement form
func Register(filer jira.IssueFiler, client *slack.Client) *modals.FlowWithViewAndFollowUps {
	return modals.ForView(Identifier, View()).WithFollowUps(map[slack.InteractionType]interactions.Handler{})
}
