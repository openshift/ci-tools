package helpdesk_faq

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestSortReplies(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name     string
		item     FaqItem
		expected FaqItem
	}{
		{
			name:     "one answer",
			item:     FaqItem{Answers: []Reply{{Timestamp: "1718288128.952979"}}},
			expected: FaqItem{Answers: []Reply{{Timestamp: "1718288128.952979"}}},
		},
		{
			name:     "multiple answers out of order",
			item:     FaqItem{Answers: []Reply{{Timestamp: "1718288129.952979"}, {Timestamp: "1718288118.234567"}, {Timestamp: "1718288112.952976"}}},
			expected: FaqItem{Answers: []Reply{{Timestamp: "1718288112.952976"}, {Timestamp: "1718288118.234567"}, {Timestamp: "1718288129.952979"}}},
		},
		{
			name:     "multiple contributing info out of order",
			item:     FaqItem{ContributingInfo: []Reply{{Timestamp: "1718288199.952979"}, {Timestamp: "1718288180.952979"}, {Timestamp: "1718288192.952979"}}},
			expected: FaqItem{ContributingInfo: []Reply{{Timestamp: "1718288180.952979"}, {Timestamp: "1718288192.952979"}, {Timestamp: "1718288199.952979"}}},
		},
		{
			name: "both out of order",
			item: FaqItem{
				ContributingInfo: []Reply{{Timestamp: "1718288199.952979"}, {Timestamp: "1718288180.952979"}, {Timestamp: "1718288192.952979"}},
				Answers:          []Reply{{Timestamp: "1718288129.952979"}, {Timestamp: "1718288118.234567"}, {Timestamp: "1718288112.952976"}},
			},
			expected: FaqItem{
				ContributingInfo: []Reply{{Timestamp: "1718288180.952979"}, {Timestamp: "1718288192.952979"}, {Timestamp: "1718288199.952979"}},
				Answers:          []Reply{{Timestamp: "1718288112.952976"}, {Timestamp: "1718288118.234567"}, {Timestamp: "1718288129.952979"}},
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			client := ConfigMapClient{}
			client.sortReplies(&tc.item)
			if diff := cmp.Diff(tc.item, tc.expected); diff != "" {
				t.Fatalf("item doesn't match expected, diff: %s", diff)
			}
		})
	}
}
