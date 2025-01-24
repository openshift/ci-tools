package imagestreamtagmapper_test

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	imagev1 "github.com/openshift/api/image/v1"

	"github.com/openshift/ci-tools/pkg/util/imagestreamtagmapper"
)

func TestImageStreamTagMapper(t *testing.T) {
	now := metav1.Now()
	upstream := func(r reconcile.Request) []reconcile.Request {
		return []reconcile.Request{
			{NamespacedName: types.NamespacedName{Namespace: "first_" + r.Namespace, Name: r.Name}},
			{NamespacedName: types.NamespacedName{Namespace: "second_" + r.Namespace, Name: r.Name}},
		}
	}
	testCases := []struct {
		name             string
		event            func() interface{}
		expectedRequests []string
	}{
		{
			name: "Create returns all tags",
			event: func() interface{} {
				imageStream := &imagev1.ImageStream{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "namespace",
						Name:      "name",
					},
					Status: imagev1.ImageStreamStatus{
						Tags: []imagev1.NamedTagEventList{{Tag: "1"}, {Tag: "2"}},
					},
				}
				return event.TypedCreateEvent[*imagev1.ImageStream]{Object: imageStream}
			},
			expectedRequests: []string{
				"first_namespace/name:1",
				"first_namespace/name:2",
				"second_namespace/name:1",
				"second_namespace/name:2",
			},
		},
		{
			name: "Update only returns changed tags",
			event: func() interface{} {
				imageStreamOld := &imagev1.ImageStream{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "namespace",
						Name:      "name",
					},
					Status: imagev1.ImageStreamStatus{
						Tags: []imagev1.NamedTagEventList{{Tag: "1"}, {Tag: "2"}},
					},
				}

				ImageStreamNew := imageStreamOld.DeepCopy()
				ImageStreamNew.Status.Tags[0].Items = []imagev1.TagEvent{{Image: "some-image"}}

				return event.TypedUpdateEvent[*imagev1.ImageStream]{
					ObjectOld: imageStreamOld,
					ObjectNew: ImageStreamNew,
				}
			},
			expectedRequests: []string{
				"first_namespace/name:1",
				"second_namespace/name:1",
			},
		},
		{
			name: "Delete returns all tags",
			event: func() interface{} {
				imageStream := &imagev1.ImageStream{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "namespace",
						Name:      "name",
					},
					Status: imagev1.ImageStreamStatus{
						Tags: []imagev1.NamedTagEventList{{Tag: "1"}, {Tag: "2"}},
					},
				}
				return event.TypedDeleteEvent[*imagev1.ImageStream]{Object: imageStream}
			},
			expectedRequests: []string{
				"first_namespace/name:1",
				"first_namespace/name:2",
				"second_namespace/name:1",
				"second_namespace/name:2",
			},
		},
		{
			name: "Generic returns all tags",
			event: func() interface{} {
				ImageStram := &imagev1.ImageStream{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "namespace",
						Name:      "name",
					},
					Status: imagev1.ImageStreamStatus{
						Tags: []imagev1.NamedTagEventList{{Tag: "1"}, {Tag: "2"}},
					},
				}
				return event.TypedGenericEvent[*imagev1.ImageStream]{Object: ImageStram}
			},
			expectedRequests: []string{
				"first_namespace/name:1",
				"first_namespace/name:2",
				"second_namespace/name:1",
				"second_namespace/name:2",
			},
		},
		{
			name: "DeletionTimestamp is defined: returns all tags",
			event: func() interface{} {
				imageStreamOld := &imagev1.ImageStream{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "namespace",
						Name:      "name",
					},
					Status: imagev1.ImageStreamStatus{
						Tags: []imagev1.NamedTagEventList{{Tag: "1"}, {Tag: "2"}},
					},
				}

				ImageStreamNew := imageStreamOld.DeepCopy()
				ImageStreamNew.DeletionTimestamp = &now

				return event.TypedUpdateEvent[*imagev1.ImageStream]{
					ObjectOld: imageStreamOld,
					ObjectNew: ImageStreamNew,
				}
			},
			expectedRequests: []string{
				"first_namespace/name:1",
				"second_namespace/name:1",
				"first_namespace/name:2",
				"second_namespace/name:2",
			},
		},
		{
			name: "isTag is soft-deleted: only deleted tags",
			event: func() interface{} {
				imageStreamOld := &imagev1.ImageStream{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "namespace",
						Name:      "name",
					},
					Status: imagev1.ImageStreamStatus{
						Tags: []imagev1.NamedTagEventList{{Tag: "1"}, {Tag: "2"}},
					},
				}

				ImageStreamNew := imageStreamOld.DeepCopy()
				ImageStreamNew.Spec = imagev1.ImageStreamSpec{
					Tags: []imagev1.TagReference{
						{
							Name:        "1",
							Annotations: map[string]string{"release.openshift.io/not-soft-delete": "some"},
						},
						{
							Name:        "2",
							Annotations: map[string]string{"release.openshift.io/soft-delete": "some"},
						},
					},
				}

				return event.TypedUpdateEvent[*imagev1.ImageStream]{
					ObjectOld: imageStreamOld,
					ObjectNew: ImageStreamNew,
				}
			},
			expectedRequests: []string{
				"first_namespace/name:2",
				"second_namespace/name:2",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {

			ctx := context.Background()
			mapper := imagestreamtagmapper.New(upstream)
			queue := &trackingWorkqueue{t: t}

			switch e := tc.event().(type) {
			case event.TypedCreateEvent[*imagev1.ImageStream]:
				mapper.Create(ctx, e, queue)
			case event.TypedUpdateEvent[*imagev1.ImageStream]:
				mapper.Update(ctx, e, queue)
			case event.TypedDeleteEvent[*imagev1.ImageStream]:
				mapper.Delete(ctx, e, queue)
			case event.TypedGenericEvent[*imagev1.ImageStream]:
				mapper.Generic(ctx, e, queue)
			default:
				t.Fatalf("got type that was not an event but a %T", e)
			}

			if actual := sets.New[string](tc.expectedRequests...); !actual.Equal(queue.received) {
				t.Errorf("actual events don't match expected, diff: %v", queue.received.Difference(actual))
			}
		})
	}
}

type trackingWorkqueue struct {
	t *testing.T
	workqueue.TypedRateLimitingInterface[reconcile.Request]
	received sets.Set[string]
}

func (t *trackingWorkqueue) Add(request reconcile.Request) {
	if t.received == nil {
		t.received = sets.Set[string]{}
	}
	t.received.Insert(request.String())
}
