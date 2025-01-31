package imagestreamtagmapper

import (
	"context"
	"fmt"
	"reflect"

	"github.com/sirupsen/logrus"

	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/util/workqueue"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	imagev1 "github.com/openshift/api/image/v1"

	"github.com/openshift/ci-tools/pkg/api"
)

// New returns a new ImageStreamTagMapper. Its purpose is to extract all ImageStreamTag events
// from an ImageStream watch. It ignores unchanged tags on Update events.
// If no additional filtering/mapping is required, upstream should just return its input.
func New(upstream func(reconcile.Request) []reconcile.Request) handler.TypedEventHandler[*imagev1.ImageStream, reconcile.Request] {
	return &imagestreamtagmapper{upstream: upstream}
}

type imagestreamtagmapper struct {
	upstream func(reconcile.Request) []reconcile.Request
}

func (m *imagestreamtagmapper) Create(ctx context.Context, e event.TypedCreateEvent[*imagev1.ImageStream], q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	m.generic(e.Object, q)
}

func (m *imagestreamtagmapper) Update(ctx context.Context, e event.TypedUpdateEvent[*imagev1.ImageStream], q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	oldStream := e.ObjectOld
	newStream := e.ObjectNew

	deletedISTags := sets.New[string]()
	for _, tag := range newStream.Spec.Tags {
		if tag.Annotations == nil {
			continue
		}
		if _, ok := tag.Annotations[api.ReleaseAnnotationSoftDelete]; ok {
			deletedISTags.Insert(tag.Name)
		}
	}

	isDeleted := newStream.DeletionTimestamp != nil
	for _, newTag := range newStream.Status.Tags {
		if !isDeleted && !deletedISTags.Has(newTag.Tag) && namedTagEventListHasElement(oldStream.Status.Tags, newTag) {
			continue
		}
		for _, request := range m.upstream(reconcile.Request{
			NamespacedName: types.NamespacedName{
				Namespace: e.ObjectNew.GetNamespace(),
				Name:      e.ObjectNew.GetName() + ":" + newTag.Tag,
			},
		}) {
			q.Add(request)
		}
	}
}

func namedTagEventListHasElement(slice []imagev1.NamedTagEventList, element imagev1.NamedTagEventList) bool {
	for _, item := range slice {
		if reflect.DeepEqual(item, element) {
			return true
		}
	}
	return false
}

func (m *imagestreamtagmapper) Delete(ctx context.Context, e event.TypedDeleteEvent[*imagev1.ImageStream], q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	m.generic(e.Object, q)
}

func (m *imagestreamtagmapper) Generic(ctx context.Context, e event.TypedGenericEvent[*imagev1.ImageStream], q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	m.generic(e.Object, q)
}

func (m *imagestreamtagmapper) generic(o ctrlruntimeclient.Object, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	imageStream, ok := o.(*imagev1.ImageStream)
	if !ok {
		logrus.WithField("type", fmt.Sprintf("%T", o)).Error("Got object that was not an ImageStram")
		return
	}

	for _, imageStreamTag := range imageStream.Status.Tags {
		for _, request := range m.upstream(reconcile.Request{
			NamespacedName: types.NamespacedName{
				Namespace: o.GetNamespace(),
				Name:      o.GetName() + ":" + imageStreamTag.Tag,
			},
		}) {
			q.Add(request)
		}
	}
}
