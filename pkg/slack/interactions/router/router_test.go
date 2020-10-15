package router

import (
	"testing"

	"github.com/slack-go/slack"

	"github.com/openshift/ci-tools/pkg/slack/modals/modaltesting"
)

func TestIsMessageButtonPress(t *testing.T) {
	var testCases = []struct {
		name     string
		expected bool
	}{
		{
			name:     "button press in a message",
			expected: true,
		},
		{
			name:     "button press in a modal",
			expected: false,
		},
		{
			name:     "not a button press",
			expected: false,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			var callback slack.InteractionCallback
			modaltesting.ReadCallbackFixture(t, &callback)
			if actual, expected := isMessageButtonPress(&callback), testCase.expected; actual != expected {
				t.Errorf("%s: did not correctly determine if callback was a button press in a message, expected %v", testCase.name, expected)
			}
		})
	}
}
