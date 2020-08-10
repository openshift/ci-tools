package imagestreamtagmapper_test

import (
	"testing"

	imagev1 "github.com/openshift/api/image/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/openshift/ci-tools/pkg/util/imagestreamtagmapper"
)

func TestImageStreamTagMapper(t *testing.T) {
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
				return event.CreateEvent{Meta: imageStream, Object: imageStream}
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

				return event.UpdateEvent{
					MetaOld:   imageStreamOld,
					ObjectOld: imageStreamOld,
					MetaNew:   ImageStreamNew,
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
				return event.DeleteEvent{Meta: imageStream, Object: imageStream}
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
				return event.GenericEvent{Meta: ImageStram, Object: ImageStram}
			},
			expectedRequests: []string{
				"first_namespace/name:1",
				"first_namespace/name:2",
				"second_namespace/name:1",
				"second_namespace/name:2",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {

			mapper := imagestreamtagmapper.New(upstream)
			queue := &trackingWorkqueue{t: t}

			switch e := tc.event().(type) {
			case event.CreateEvent:
				mapper.Create(e, queue)
			case event.UpdateEvent:
				mapper.Update(e, queue)
			case event.DeleteEvent:
				mapper.Delete(e, queue)
			case event.GenericEvent:
				mapper.Generic(e, queue)
			default:
				t.Fatalf("got type that was not an event but a %T", e)
			}

			if actual := sets.NewString(tc.expectedRequests...); !actual.Equal(queue.received) {
				t.Errorf("actual events don't match expected, diff: %v", queue.received.Difference(actual))
			}
		})
	}
}

type trackingWorkqueue struct {
	t *testing.T
	workqueue.RateLimitingInterface
	received sets.String
}

func (t *trackingWorkqueue) Add(item interface{}) {
	request, ok := item.(reconcile.Request)
	if !ok {
		t.t.Fatalf("workqueue got item that was not reconcile.Request but %T", item)
	}
	if t.received == nil {
		t.received = sets.String{}
	}
	t.received.Insert(request.String())
}
