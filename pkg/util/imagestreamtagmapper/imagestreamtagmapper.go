package imagestreamtagmapper

import (
	"fmt"
	"reflect"

	imagev1 "github.com/openshift/api/image/v1"
	"github.com/sirupsen/logrus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// New returns a new ImageStreamTagMapper. Its purpose is to extract all ImageStreamTag events
// from an ImageStream watch. It ignores unchanged tags on Update events.
// If no additional filtering/mapping is required, upstream should just return its input.
func New(upstream func(reconcile.Request) []reconcile.Request) handler.EventHandler {
	return &imagestreamtagmapper{upstream: upstream}
}

type imagestreamtagmapper struct {
	upstream func(reconcile.Request) []reconcile.Request
}

func (m *imagestreamtagmapper) Create(e event.CreateEvent, q workqueue.RateLimitingInterface) {
	m.generic(e.Object, e.Meta, q)
}

func (m *imagestreamtagmapper) Update(e event.UpdateEvent, q workqueue.RateLimitingInterface) {
	oldStream, oldOK := e.ObjectOld.(*imagev1.ImageStream)
	newStream, newOK := e.ObjectNew.(*imagev1.ImageStream)
	if !oldOK || !newOK {
		logrus.WithFields(logrus.Fields{
			"old_type": fmt.Sprintf("%T", e.ObjectOld),
			"new_type": fmt.Sprintf("%T", e.ObjectNew),
		}).Error("Got object that was not an *imagev1.ImageStream")
		return
	}

	for _, newTag := range newStream.Status.Tags {
		if namedTagEventListHasElement(oldStream.Status.Tags, newTag) {
			continue
		}
		for _, request := range m.upstream(reconcile.Request{
			NamespacedName: types.NamespacedName{
				Namespace: e.MetaNew.GetNamespace(),
				Name:      e.MetaNew.GetName() + ":" + newTag.Tag,
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

func (m *imagestreamtagmapper) Delete(e event.DeleteEvent, q workqueue.RateLimitingInterface) {
	m.generic(e.Object, e.Meta, q)
}

func (m *imagestreamtagmapper) Generic(e event.GenericEvent, q workqueue.RateLimitingInterface) {
	m.generic(e.Object, e.Meta, q)
}

func (m *imagestreamtagmapper) generic(o runtime.Object, meta metav1.Object, q workqueue.RateLimitingInterface) {
	imageStream, ok := o.(*imagev1.ImageStream)
	if !ok {
		logrus.WithField("type", fmt.Sprintf("%T", o)).Error("Got object that was not an ImageStram")
		return
	}

	for _, imageStreamTag := range imageStream.Status.Tags {
		for _, request := range m.upstream(reconcile.Request{
			NamespacedName: types.NamespacedName{
				Namespace: meta.GetNamespace(),
				Name:      meta.GetName() + ":" + imageStreamTag.Tag,
			},
		}) {
			q.Add(request)
		}
	}
}
