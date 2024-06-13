package helpdesk

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/sirupsen/logrus"

	helpdeskfaq "github.com/openshift/ci-tools/pkg/helpdesk-faq"
)

func TestFormatItemField(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "trim and remove beginning formatting",
			input:    " &gt; This is a question...any tips? ",
			expected: "This is a question...any tips?",
		},
		{
			name:     "multi-line question",
			input:    " &gt; This is a question\n&gt;...any tips? ",
			expected: "This is a question\n...any tips?",
		},
		{
			name:     "slack link formatting removed",
			input:    " &gt; This is a question containing a link: <https://github.com/openshift/release/pull/1234> ",
			expected: "This is a question containing a link: https://github.com/openshift/release/pull/1234",
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := formatItemField(tc.input)
			if diff := cmp.Diff(result, tc.expected); diff != "" {
				t.Fatalf("result doesn't match expected, diff: %s", diff)
			}
		})
	}
}

func TestRemoveReply(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name      string
		messageTs string
		replies   []helpdeskfaq.Reply
		expected  []helpdeskfaq.Reply
	}{
		{
			name:      "reply doesn't exist",
			messageTs: "2024-06-13T14:00:00Z",
			replies: []helpdeskfaq.Reply{
				{
					Author:    "U1234",
					Timestamp: "2024-06-14T14:00:00Z",
					Body:      "this is the only reply",
				},
			},
			expected: []helpdeskfaq.Reply{
				{
					Author:    "U1234",
					Timestamp: "2024-06-14T14:00:00Z",
					Body:      "this is the only reply",
				},
			},
		},
		{
			name:      "reply exists",
			messageTs: "2024-06-13T14:00:00Z",
			replies: []helpdeskfaq.Reply{
				{
					Author:    "U1234",
					Timestamp: "2024-06-13T14:00:00Z",
					Body:      "this is a reply",
				},
				{
					Author:    "U1234",
					Timestamp: "2024-06-14T14:00:00Z",
					Body:      "and another one",
				},
			},
			expected: []helpdeskfaq.Reply{
				{
					Author:    "U1234",
					Timestamp: "2024-06-14T14:00:00Z",
					Body:      "and another one",
				},
			},
		},
		{
			name:      "no replies",
			messageTs: "2024-06-13T14:00:00Z",
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := removeReply(tc.messageTs, tc.replies, logrus.NewEntry(logrus.New()))
			if diff := cmp.Diff(result, tc.expected); diff != "" {
				t.Fatalf("result doesn't match expected, diff: %s", diff)
			}
		})
	}
}
