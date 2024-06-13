package helpdesk_faq

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestReplyExists(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name      string
		timestamp string
		faqItem   FaqItem
		expected  bool
	}{
		{
			name:      "doesn't exist",
			timestamp: "2024-06-13T14:00:00Z",
			faqItem: FaqItem{
				Answers: []Reply{
					{
						Timestamp: "2024-05-13T14:00:00Z",
					},
				},
				ContributingInfo: []Reply{
					{
						Timestamp: "2024-05-14T14:00:00Z",
					},
				},
			},
			expected: false,
		},
		{
			name:      "exists as answer",
			timestamp: "2024-06-13T14:00:00Z",
			faqItem: FaqItem{
				Answers: []Reply{
					{
						Timestamp: "2024-06-13T14:00:00Z",
					},
				},
			},
			expected: true,
		},
		{
			name:      "exists as contributing info",
			timestamp: "2024-06-13T14:00:00Z",
			faqItem: FaqItem{
				ContributingInfo: []Reply{
					{
						Timestamp: "2024-06-13T14:00:00Z",
					},
				},
			},
			expected: true,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := tc.faqItem.ReplyExists(tc.timestamp)
			if diff := cmp.Diff(tc.expected, result); diff != "" {
				t.Fatalf("expected doesn't match result, diff: %s", diff)
			}
		})
	}
}
