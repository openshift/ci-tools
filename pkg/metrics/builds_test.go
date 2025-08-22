package metrics

import (
	"context"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	buildapi "github.com/openshift/api/build/v1"
)

func Test_buildPlugin_Record(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := buildapi.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add buildapi scheme: %v", err)
	}

	tests := []struct {
		name           string
		providedBuilds []runtime.Object
		eventName      string
		eventNS        string
		forImage       string
		expectedEvents []MetricsEvent
	}{
		{
			name: "successful record",
			providedBuilds: []runtime.Object{
				&buildapi.Build{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-build",
						Namespace: "test-namespace",
					},
					Status: buildapi.BuildStatus{
						StartTimestamp:      &metav1.Time{Time: time.Date(2025, time.January, 1, 0, 0, 0, 0, time.UTC)},
						CompletionTimestamp: &metav1.Time{Time: time.Date(2025, time.January, 1, 0, 10, 0, 0, time.UTC)},
						Phase:               buildapi.BuildPhaseComplete,
						Reason:              "Succeeded",
					},
					Spec: buildapi.BuildSpec{
						CommonSpec: buildapi.CommonSpec{
							Output: buildapi.BuildOutput{
								To: &corev1.ObjectReference{
									Name: "test-output-image",
								},
							},
						},
					},
				},
			},
			eventName: "test-build",
			eventNS:   "test-namespace",
			forImage:  "tag-1",
			expectedEvents: []MetricsEvent{
				&BuildEvent{
					Namespace:       "test-namespace",
					Name:            "test-build",
					StartTime:       time.Date(2025, time.January, 1, 0, 0, 0, 0, time.UTC),
					CompletionTime:  time.Date(2025, time.January, 1, 0, 10, 0, 0, time.UTC),
					DurationSeconds: 600,
					Status:          string(buildapi.BuildPhaseComplete),
					Reason:          "Succeeded",
					OutputImage:     "test-output-image",
					ForImage:        "tag-1",
				},
			},
		},
		{
			name:           "build not found",
			providedBuilds: nil,
			eventName:      "nonexistent-build",
			eventNS:        "default",
			forImage:       "tag-2",
		},
		{
			name: "build with nil timestamps",
			providedBuilds: []runtime.Object{
				&buildapi.Build{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "build-with-nil-timestamps",
						Namespace: "test-namespace",
					},
					Status: buildapi.BuildStatus{
						StartTimestamp:      nil, // Nil timestamp
						CompletionTimestamp: nil, // Nil timestamp
						Phase:               buildapi.BuildPhasePending,
						Reason:              "Pending",
					},
					Spec: buildapi.BuildSpec{
						CommonSpec: buildapi.CommonSpec{
							Output: buildapi.BuildOutput{
								To: &corev1.ObjectReference{
									Name: "test-output-image",
								},
							},
						},
					},
				},
			},
			eventName: "build-with-nil-timestamps",
			eventNS:   "test-namespace",
			forImage:  "tag-3",
			expectedEvents: []MetricsEvent{
				&BuildEvent{
					Namespace:       "test-namespace",
					Name:            "build-with-nil-timestamps",
					StartTime:       time.Time{},
					CompletionTime:  time.Time{},
					DurationSeconds: 0,
					Status:          string(buildapi.BuildPhasePending),
					Reason:          "Pending",
					OutputImage:     "test-output-image",
					ForImage:        "tag-3",
				},
			},
		},
		{
			name: "build with nil output reference",
			providedBuilds: []runtime.Object{
				&buildapi.Build{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "build-with-nil-output",
						Namespace: "test-namespace",
					},
					Status: buildapi.BuildStatus{
						StartTimestamp:      &metav1.Time{Time: time.Date(2025, time.January, 1, 0, 0, 0, 0, time.UTC)},
						CompletionTimestamp: &metav1.Time{Time: time.Date(2025, time.January, 1, 0, 5, 0, 0, time.UTC)},
						Phase:               buildapi.BuildPhaseComplete,
						Reason:              "Succeeded",
					},
					Spec: buildapi.BuildSpec{
						CommonSpec: buildapi.CommonSpec{
							Output: buildapi.BuildOutput{
								To: nil,
							},
						},
					},
				},
			},
			eventName: "build-with-nil-output",
			eventNS:   "test-namespace",
			forImage:  "tag-4",
			expectedEvents: []MetricsEvent{
				&BuildEvent{
					Namespace:       "test-namespace",
					Name:            "build-with-nil-output",
					StartTime:       time.Date(2025, time.January, 1, 0, 0, 0, 0, time.UTC),
					CompletionTime:  time.Date(2025, time.January, 1, 0, 5, 0, 0, time.UTC),
					DurationSeconds: 300,
					Status:          string(buildapi.BuildPhaseComplete),
					Reason:          "Succeeded",
					OutputImage:     "",
					ForImage:        "tag-4",
				},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			bp := newBuildPlugin(fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(tc.providedBuilds...).Build(), context.Background())
			bp.Record(NewBuildEvent(tc.eventName, tc.eventNS, tc.forImage))
			if diff := cmp.Diff(tc.expectedEvents, bp.Events(), cmpopts.IgnoreFields(BuildEvent{}, "Timestamp")); diff != "" {
				t.Fatal(diff)
			}
		})
	}
}
