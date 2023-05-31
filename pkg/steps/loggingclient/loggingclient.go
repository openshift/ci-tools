package loggingclient

import (
	"context"
	"fmt"
	"sync"

	"github.com/sirupsen/logrus"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
)

type LoggingClient interface {
	ctrlruntimeclient.WithWatch
	// Object contains the latest revision of each object the client has
	// seen. Calling this will reset the cache.
	Objects() []ctrlruntimeclient.Object
	// New returns a new instance of a LoggingClient whose objects will
	// not be logged in the parent.
	New() LoggingClient
}

func New(upstream ctrlruntimeclient.WithWatch) LoggingClient {
	return &loggingClient{
		upstream: upstream,
		logTo:    map[string]map[string]ctrlruntimeclient.Object{},
	}
}

type loggingClient struct {
	lock     sync.Mutex
	upstream ctrlruntimeclient.WithWatch
	logTo    map[string]map[string]ctrlruntimeclient.Object
}

func (lc *loggingClient) Get(ctx context.Context, key ctrlruntimeclient.ObjectKey, obj ctrlruntimeclient.Object, opts ...ctrlruntimeclient.GetOption) error {
	if err := lc.upstream.Get(ctx, key, obj); err != nil {
		return err
	}
	lc.logObject(obj)
	return nil
}

func (lc *loggingClient) List(ctx context.Context, list ctrlruntimeclient.ObjectList, opts ...ctrlruntimeclient.ListOption) error {
	// TODO: Should we support the logging here? We'd need to use reflect to get the Items field. Its reasonable safe as controller-runtime
	// itself will bail out if that is not present
	return lc.upstream.List(ctx, list, opts...)
}

func (lc *loggingClient) Create(ctx context.Context, obj ctrlruntimeclient.Object, opts ...ctrlruntimeclient.CreateOption) error {
	if err := lc.upstream.Create(ctx, obj, opts...); err != nil {
		return err
	}
	lc.logObject(obj)
	return nil
}

func (lc *loggingClient) Delete(ctx context.Context, obj ctrlruntimeclient.Object, opts ...ctrlruntimeclient.DeleteOption) error {
	// The delete call doesn't return the object, so we have to get it first to be able to log it
	name := ctrlruntimeclient.ObjectKey{Namespace: obj.GetNamespace(), Name: obj.GetName()}
	getObj := obj.DeepCopyObject().(ctrlruntimeclient.Object)
	if err := lc.upstream.Get(ctx, name, getObj); err != nil {
		return err
	}
	lc.logObject(getObj)

	if err := lc.upstream.Delete(ctx, obj, opts...); err != nil {
		return err
	}
	return nil
}

func (lc *loggingClient) Update(ctx context.Context, obj ctrlruntimeclient.Object, opts ...ctrlruntimeclient.UpdateOption) error {
	if err := lc.upstream.Update(ctx, obj, opts...); err != nil {
		return err
	}
	lc.logObject(obj)
	return nil
}

func (lc *loggingClient) Patch(ctx context.Context, obj ctrlruntimeclient.Object, patch ctrlruntimeclient.Patch, opts ...ctrlruntimeclient.PatchOption) error {
	if err := lc.upstream.Patch(ctx, obj, patch, opts...); err != nil {
		return err
	}
	lc.logObject(obj)
	return nil
}

func (lc *loggingClient) DeleteAllOf(ctx context.Context, obj ctrlruntimeclient.Object, opts ...ctrlruntimeclient.DeleteAllOfOption) error {
	return lc.upstream.DeleteAllOf(ctx, obj, opts...)
}

func (lc *loggingClient) Watch(ctx context.Context, obj ctrlruntimeclient.ObjectList, opts ...ctrlruntimeclient.ListOption) (watch.Interface, error) {
	return lc.upstream.Watch(ctx, obj, opts...)
}

func (lc *loggingClient) logObject(obj ctrlruntimeclient.Object) {
	gvk, err := apiutil.GVKForObject(obj, lc.Scheme())
	if err != nil {
		logrus.WithError(err).Errorf("Failed to get gvk for object %+v ", obj)
	}
	obj = obj.DeepCopyObject().(ctrlruntimeclient.Object)
	typeAccessor, err := meta.TypeAccessor(obj)
	if err != nil {
		logrus.WithError(err).Errorf("Failed to get type accessor for object %+v", obj)
	} else {
		typeAccessor.SetKind(gvk.Kind)
		typeAccessor.SetAPIVersion(gvk.GroupVersion().String())
	}
	lc.lock.Lock()
	defer lc.lock.Unlock()
	t := fmt.Sprintf("%T", obj)
	if _, ok := lc.logTo[t]; !ok {
		lc.logTo[t] = map[string]ctrlruntimeclient.Object{}
	}
	lc.logTo[t][obj.GetNamespace()+"/"+obj.GetName()] = obj.DeepCopyObject().(ctrlruntimeclient.Object)
}

func (lc *loggingClient) LogTo(logTo map[string]map[string]ctrlruntimeclient.Object) {
	lc.logTo = logTo
}

func (lc *loggingClient) Objects() []ctrlruntimeclient.Object {
	lc.lock.Lock()
	defer lc.lock.Unlock()
	var result []ctrlruntimeclient.Object
	for _, nameMap := range lc.logTo {
		for _, object := range nameMap {
			result = append(result, object)
		}
	}

	lc.logTo = map[string]map[string]ctrlruntimeclient.Object{}
	return result
}

func (lc *loggingClient) New() LoggingClient {
	return New(lc.upstream)
}

func (lc *loggingClient) Status() ctrlruntimeclient.StatusWriter {
	return lc.upstream.Status()
}

func (lc *loggingClient) Scheme() *runtime.Scheme {
	return lc.upstream.Scheme()
}

func (lc *loggingClient) RESTMapper() meta.RESTMapper {
	return lc.upstream.RESTMapper()
}

func (lc *loggingClient) GroupVersionKindFor(obj runtime.Object) (schema.GroupVersionKind, error) {
	return lc.upstream.GroupVersionKindFor(obj)
}

func (lc *loggingClient) IsObjectNamespaced(obj runtime.Object) (bool, error) {
	return lc.upstream.IsObjectNamespaced(obj)
}

func (lc *loggingClient) SubResource(subResource string) ctrlruntimeclient.SubResourceClient {
	return lc.upstream.SubResource(subResource)
}
