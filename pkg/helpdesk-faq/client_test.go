package helpdesk_faq

import (
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/sirupsen/logrus"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	fakectrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/openshift/ci-tools/pkg/testhelper"
)

func TestSortReplies(t *testing.T) {
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
	for i := range testCases {
		tc := testCases[i]
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			client := ConfigMapClient{}
			client.sortReplies(&tc.item)
			if diff := cmp.Diff(tc.item, tc.expected); diff != "" {
				t.Fatalf("item doesn't match expected, diff: %s", diff)
			}
		})
	}
}

func TestConvertDataToSortedSlice(t *testing.T) {
	testCases := []struct {
		name     string
		data     map[string]string
		expected []string
	}{
		{
			name: "basic",
			data: map[string]string{
				"1718288199.952979": "1",
				"1718288180.952979": "2",
				"1718288192.952979": "3",
				"1718288180.952989": "4",
			},
			expected: []string{"1", "3", "4", "2"},
		},
	}
	for i := range testCases {
		tc := testCases[i]
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			result := convertDataToSortedSlice(tc.data)
			if diff := cmp.Diff(result, tc.expected); diff != "" {
				t.Fatalf("result doesn't match expected, diff: %s", diff)
			}
		})
	}
}

func TestConfigMapClient_GetSerializedFaqItems(t *testing.T) {
	namespace := "ci"
	testCases := []struct {
		name        string
		configMap   v1.ConfigMap
		expected    []string
		expectedErr error
	}{
		{
			name: "basic",
			configMap: v1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      faqConfigMap,
					Namespace: namespace,
				},
				Data: map[string]string{
					"1718288199.952979": "json representation of faq-item",
					"1718288180.952979": "json representation of another faq-item",
				},
			},
			expected: []string{"json representation of faq-item", "json representation of another faq-item"},
		},
		{
			name:        "config map doesn't exist",
			expectedErr: errors.New("failed to get configMap helpdesk-faq: configmaps \"helpdesk-faq\" not found"),
		},
	}
	for i := range testCases {
		tc := testCases[i]
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fakeKubeClient := fakectrlruntimeclient.NewClientBuilder().WithRuntimeObjects(&tc.configMap).Build()
			client := NewCMClient(fakeKubeClient, namespace, logrus.NewEntry(logrus.StandardLogger()))
			result, err := client.GetSerializedFAQItems()
			if diff := cmp.Diff(err, tc.expectedErr, testhelper.EquateErrorMessage); diff != "" {
				t.Fatalf("error doesn't match expectedErr, diff: %s", diff)
			}
			if diff := cmp.Diff(result, tc.expected); diff != "" {
				t.Fatalf("result doesn't match expected, diff: %s", diff)
			}
		})
	}
}

func TestConfigMapClient_GetFAQItemIfExists(t *testing.T) {
	namespace := "ci"
	testCases := []struct {
		name        string
		configMap   v1.ConfigMap
		timestamp   string
		expected    *FaqItem
		expectedErr error
	}{
		{
			name: "faq item exists",
			configMap: v1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      faqConfigMap,
					Namespace: namespace,
				},
				Data: map[string]string{
					"1718288199.952979": "{\"question\":{\"author\":\"SOMEUSER\",\"topic\":\"Other Topic\",\"subject\":\"A really good question\",\"body\":\"This is super important\"},\"timestamp\":\"1718288199.952979\",\"thread_link\":\"https://some-slack-link.com\",\"contributing_info\":[{\"author\":\"SOMEUSER\",\"timestamp\":\"1718288230.952979\",\"body\":\"this is important\"}],\"answers\":[{\"author\":\"SOMEHELPFULPERSON\",\"timestamp\":\"1718288238.952979\",\"body\":\"do it like this...\"}]}",
					"1718288180.952979": "json representation of another faq-item",
				},
			},
			timestamp: "1718288199.952979",
			expected: &FaqItem{
				Question:         Question{Author: "SOMEUSER", Topic: "Other Topic", Subject: "A really good question", Body: "This is super important"},
				Timestamp:        "1718288199.952979",
				ThreadLink:       "https://some-slack-link.com",
				ContributingInfo: []Reply{{Author: "SOMEUSER", Timestamp: "1718288230.952979", Body: "this is important"}},
				Answers:          []Reply{{Author: "SOMEHELPFULPERSON", Timestamp: "1718288238.952979", Body: "do it like this..."}},
			},
		},
		{
			name: "faq item doesn't exist",
			configMap: v1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      faqConfigMap,
					Namespace: namespace,
				},
				Data: map[string]string{
					"1718288180.952979": "json representation of a faq-item",
				},
			},
			timestamp: "1718288199.952979",
		},
		{
			name: "improper formatting",
			configMap: v1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      faqConfigMap,
					Namespace: namespace,
				},
				Data: map[string]string{
					"1718288199.952979": "poorly formatted item",
				},
			},
			timestamp:   "1718288199.952979",
			expectedErr: errors.New("unable to unmarshall faqItem: invalid character 'p' looking for beginning of value"),
		},
	}
	for i := range testCases {
		tc := testCases[i]
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fakeKubeClient := fakectrlruntimeclient.NewClientBuilder().WithRuntimeObjects(&tc.configMap).Build()
			client := NewCMClient(fakeKubeClient, namespace, logrus.NewEntry(logrus.StandardLogger()))
			result, err := client.GetFAQItemIfExists(tc.timestamp)
			if diff := cmp.Diff(err, tc.expectedErr, testhelper.EquateErrorMessage); diff != "" {
				t.Fatalf("error doesn't match expectedErr, diff: %s", diff)
			}
			if diff := cmp.Diff(result, tc.expected); diff != "" {
				t.Fatalf("result doesn't match expected, diff: %s", diff)
			}
		})
	}
}

func TestConfigMapClient_UpsertItem(t *testing.T) {
	namespace := "ci"
	testCases := []struct {
		name        string
		configMap   v1.ConfigMap
		item        FaqItem
		expectedErr error
	}{
		{
			name: "new item",
			configMap: v1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      faqConfigMap,
					Namespace: namespace,
				},
				Data: map[string]string{
					"1718288180.952979": "json representation of a faq-item",
				},
			},
			item: FaqItem{
				Question:         Question{Author: "SOMEUSER", Topic: "Other Topic", Subject: "A really good question", Body: "This is super important"},
				Timestamp:        "1718288199.952979",
				ThreadLink:       "https://some-slack-link.com",
				ContributingInfo: []Reply{{Author: "SOMEUSER", Timestamp: "1718288230.952979", Body: "this is important"}},
				Answers:          []Reply{{Author: "SOMEHELPFULPERSON", Timestamp: "1718288238.952979", Body: "do it like this..."}},
			},
		},
		{
			name: "modified item",
			configMap: v1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      faqConfigMap,
					Namespace: namespace,
				},
				Data: map[string]string{
					"1718288199.952979": "{\"question\":{\"author\":\"SOMEUSER\",\"topic\":\"Other Topic\",\"subject\":\"A really good question\",\"body\":\"This is super important\"},\"timestamp\":\"1718288199.952979\",\"thread_link\":\"https://some-slack-link.com\",\"contributing_info\":[{\"author\":\"SOMEUSER\",\"timestamp\":\"1718288230.952979\",\"body\":\"this is important\"}],\"answers\":[{\"author\":\"SOMEHELPFULPERSON\",\"timestamp\":\"1718288238.952979\",\"body\":\"do it like this...\"}]}",
					"1718288180.952979": "json representation of another faq-item",
				},
			},
			item: FaqItem{
				Question:   Question{Author: "SOMEUSER", Topic: "Other Topic", Subject: "A really good question", Body: "This is super important"},
				Timestamp:  "1718288199.952979",
				ThreadLink: "https://some-slack-link.com",
				Answers: []Reply{
					{Author: "SOMEHELPFULPERSON", Timestamp: "1718288238.952979", Body: "do it like this..."},
					{Author: "SOMEHELPFULPERSON", Timestamp: "1718288269.952979", Body: "with the exact right command"},
				},
			},
		},
	}
	for i := range testCases {
		tc := testCases[i]
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fakeKubeClient := fakectrlruntimeclient.NewClientBuilder().WithRuntimeObjects(&tc.configMap).Build()
			client := NewCMClient(fakeKubeClient, namespace, logrus.NewEntry(logrus.StandardLogger()))
			err := client.UpsertItem(tc.item)
			if diff := cmp.Diff(err, tc.expectedErr, testhelper.EquateErrorMessage); diff != "" {
				t.Fatalf("error doesn't match expectedErr, diff: %s", diff)
			}
			resultingItem, err := client.GetFAQItemIfExists(tc.item.Timestamp)
			if err != nil {
				t.Fatalf("error getting resulting item")
			}
			if diff := cmp.Diff(resultingItem, &tc.item); diff != "" {
				t.Fatalf("resultingItem doesn't match item, diff: %s", diff)
			}
		})
	}
}

func TestConfigMapClient_RemoveItem(t *testing.T) {
	namespace := "ci"
	testCases := []struct {
		name        string
		configMap   v1.ConfigMap
		timestamp   string
		expectedErr error
	}{
		{
			name: "faq item exists",
			configMap: v1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      faqConfigMap,
					Namespace: namespace,
				},
				Data: map[string]string{
					"1718288199.952979": "some json faq-item",
					"1718288180.952979": "json representation of another faq-item",
				},
			},
			timestamp: "1718288199.952979",
		},
		{
			name: "faq item doesn't exist",
			configMap: v1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      faqConfigMap,
					Namespace: namespace,
				},
				Data: map[string]string{
					"1718288180.952979": "json representation of a faq-item",
				},
			},
			timestamp: "1718288199.952979",
		},
	}
	for i := range testCases {
		tc := testCases[i]
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fakeKubeClient := fakectrlruntimeclient.NewClientBuilder().WithRuntimeObjects(&tc.configMap).Build()
			client := NewCMClient(fakeKubeClient, namespace, logrus.NewEntry(logrus.StandardLogger()))
			err := client.RemoveItem(tc.timestamp)
			if diff := cmp.Diff(err, tc.expectedErr, testhelper.EquateErrorMessage); diff != "" {
				t.Fatalf("error doesn't match expectedErr, diff: %s", diff)
			}
			resultingItem, err := client.GetFAQItemIfExists(tc.timestamp)
			if err != nil {
				t.Fatalf("error getting resulting item")
			}
			if resultingItem != nil {
				t.Fatalf("resultingItem shouldn't exist")
			}
		})
	}
}
