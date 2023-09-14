package jobrunaggregatorlib

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	prowv1 "k8s.io/test-infra/prow/apis/prowjobs/v1"
)

const fakeMatchingLabel = "fakeMatchingLabel"

func fakeProwJobMatcherFunc(job *prowv1.ProwJob) bool {
	if match, ok := job.Labels[fakeMatchingLabel]; ok && match == "match" {
		return true
	}
	return false
}

func TestAllProwJobFinished(t *testing.T) {
	tests := []struct {
		name               string
		allItems           []*prowv1.ProwJob
		ProwJobMatcherFunc ProwJobMatcherFunc
		result             bool
	}{
		{
			name:               "Single matched job completed test",
			ProwJobMatcherFunc: fakeProwJobMatcherFunc,
			allItems: []*prowv1.ProwJob{
				{
					Status: prowv1.ProwJobStatus{
						CompletionTime: &metav1.Time{
							Time: time.Now(),
						},
					},
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{
							prowJobJobRunIDLabel: "Job1",
							fakeMatchingLabel:    "match",
						},
					},
				},
			},
			result: true,
		},
		{
			name:               "Single matched job uncompleted test",
			ProwJobMatcherFunc: fakeProwJobMatcherFunc,
			allItems: []*prowv1.ProwJob{
				{
					Status: prowv1.ProwJobStatus{},
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{
							prowJobJobRunIDLabel: "Job1",
							fakeMatchingLabel:    "match",
						},
					},
				},
			},
			result: false,
		},
		{
			name:               "Single unmatched job completed test",
			ProwJobMatcherFunc: fakeProwJobMatcherFunc,
			allItems: []*prowv1.ProwJob{
				{
					Status: prowv1.ProwJobStatus{
						CompletionTime: &metav1.Time{
							Time: time.Now(),
						},
					},
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{
							prowJobJobRunIDLabel: "Job1",
							fakeMatchingLabel:    "unmatched",
						},
					},
				},
			},
			result: true,
		},
		{
			name:               "Single unmatched job uncompleted test",
			ProwJobMatcherFunc: fakeProwJobMatcherFunc,
			allItems: []*prowv1.ProwJob{
				{
					Status: prowv1.ProwJobStatus{},
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{
							prowJobJobRunIDLabel: "Job1",
							fakeMatchingLabel:    "unmatched",
						},
					},
				},
			},
			result: true,
		},
		{
			name:               "Multiple matched jobs completed test",
			ProwJobMatcherFunc: fakeProwJobMatcherFunc,
			allItems: []*prowv1.ProwJob{
				{
					Status: prowv1.ProwJobStatus{
						CompletionTime: &metav1.Time{
							Time: time.Now(),
						},
					},
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{
							prowJobJobRunIDLabel: "Job1",
							fakeMatchingLabel:    "match",
						},
					},
				},
				{
					Status: prowv1.ProwJobStatus{
						CompletionTime: &metav1.Time{
							Time: time.Now(),
						},
					},
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{
							prowJobJobRunIDLabel: "Job2",
							fakeMatchingLabel:    "unmatched",
						},
					},
				},
				{
					Status: prowv1.ProwJobStatus{
						CompletionTime: &metav1.Time{
							Time: time.Now(),
						},
					},
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{
							prowJobJobRunIDLabel: "Job3",
							fakeMatchingLabel:    "match",
						},
					},
				},
			},
			result: true,
		},
		{
			name:               "Multiple matched jobs uncompleted test",
			ProwJobMatcherFunc: fakeProwJobMatcherFunc,
			allItems: []*prowv1.ProwJob{
				{
					Status: prowv1.ProwJobStatus{},
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{
							prowJobJobRunIDLabel: "Job1",
							fakeMatchingLabel:    "match",
						},
					},
				},
				{
					Status: prowv1.ProwJobStatus{
						CompletionTime: &metav1.Time{
							Time: time.Now(),
						},
					},
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{
							prowJobJobRunIDLabel: "Job2",
							fakeMatchingLabel:    "unmatched",
						},
					},
				},
				{
					Status: prowv1.ProwJobStatus{
						CompletionTime: &metav1.Time{
							Time: time.Now(),
						},
					},
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{
							prowJobJobRunIDLabel: "Job3",
							fakeMatchingLabel:    "match",
						},
					},
				},
			},
			result: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			waiter := ClusterJobRunWaiter{
				TimeToStopWaiting:  time.Now(),
				ProwJobMatcherFunc: tt.ProwJobMatcherFunc,
			}
			result := waiter.allProwJobsFinished(tt.allItems)
			assert.Equal(t, tt.result, result, "Test %s expecting %v, got %v", tt.name, tt.result, result)
		})
	}
}
