package watchingclient

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
)

func New(upstream ctrlruntimeclient.Client, cfg *rest.Config, watchFor ...runtime.Object) (ctrlruntimeclient.Client, error) {
	watchForMap := make(map[schema.GroupVersionKind]struct{}, len(watchFor))
	for _, obj := range watchFor {
		gvk, err := apiutil.GVKForObject(obj, upstream.Scheme())
		if err != nil {
			return nil, fmt.Errorf("faield to get gvk for %T: %w", obj, err)
		}
		watchForMap[gvk] = struct{}{}
	}
	return &watchingClient{
		cfg:        cfg,
		Client:     upstream,
		watchFor:   watchForMap,
		codecs:     serializer.NewCodecFactory(upstream.Scheme()),
		paramCodec: runtime.NewParameterCodec(upstream.Scheme()),
		watches:    map[gvkNamespacedName]func(obj ctrlruntimeclient.Object) error{},
	}, nil
}

type gvkNamespacedName struct {
	gvk             schema.GroupVersionKind
	namespace, name string
}

type watchingClient struct {
	cfg *rest.Config
	ctrlruntimeclient.Client
	watchFor map[schema.GroupVersionKind]struct{}

	codecs      serializer.CodecFactory
	paramCodec  runtime.ParameterCodec
	watchesLock sync.RWMutex
	watches     map[gvkNamespacedName]func(obj ctrlruntimeclient.Object) error
}

func (w *watchingClient) Get(ctx context.Context, key ctrlruntimeclient.ObjectKey, obj ctrlruntimeclient.Object) error {
	gvk, err := apiutil.GVKForObject(obj, w.Scheme())
	if err != nil {
		return err
	}
	if _, useWatch := w.watchFor[gvk]; !useWatch {
		return w.Client.Get(ctx, key, obj)
	}

	obj.SetNamespace(key.Namespace)
	obj.SetName(key.Name)
	retriever, err := w.cacheRetriever(obj, gvk)
	if err != nil {
		return err
	}
	return retriever(obj)
}

func (w *watchingClient) cacheRetriever(obj ctrlruntimeclient.Object, gvk schema.GroupVersionKind) (func(obj ctrlruntimeclient.Object) error, error) {
	cacheKey := gvkNamespacedName{gvk: gvk, namespace: obj.GetNamespace(), name: obj.GetName()}
	if found := func() func(obj ctrlruntimeclient.Object) error {
		w.watchesLock.RLock()
		defer w.watchesLock.RUnlock()
		return w.watches[cacheKey]
	}(); found != nil {
		return found, nil
	}

	w.watchesLock.Lock()
	defer w.watchesLock.Unlock()
	// Check again
	if found := w.watches[cacheKey]; found != nil {
		return found, nil
	}

	mapping, err := w.RESTMapper().RESTMapping(gvk.GroupKind(), gvk.Version)
	if err != nil {
		return nil, err
	}

	client, err := apiutil.RESTClientForGVK(gvk, false, w.cfg, w.codecs)
	if err != nil {
		return nil, err
	}

	listGVK := gvk.GroupVersion().WithKind(gvk.Kind + "List")
	listObj, err := w.Scheme().New(listGVK)
	if err != nil {
		return nil, err
	}

	ctx := context.TODO()
	var namespace string
	if mapping.Scope.Name() != meta.RESTScopeNameRoot {
		namespace = obj.GetNamespace()
	}
	lw := &cache.ListWatch{
		ListFunc: func(opts metav1.ListOptions) (object runtime.Object, e error) {
			res := listObj.DeepCopyObject()
			opts.FieldSelector = fields.OneTermEqualSelector("metadata.name", obj.GetName()).String()
			err := client.Get().Namespace(namespace).Resource(mapping.Resource.Resource).VersionedParams(&opts, w.paramCodec).Do(ctx).Into(res)
			return res, err
		},
		WatchFunc: func(opts metav1.ListOptions) (i watch.Interface, e error) {
			opts.FieldSelector = fields.OneTermEqualSelector("metadata.name", obj.GetName()).String()
			opts.Watch = true
			return client.Get().Namespace(namespace).Resource(mapping.Resource.Resource).VersionedParams(&opts, w.paramCodec).Watch(ctx)
		},
	}

	informer := cache.NewSharedIndexInformer(lw, obj, 0, cache.Indexers{
		cache.NamespaceIndex: cache.MetaNamespaceIndexFunc,
	})

	// Should we stop them N time after the last get request?
	go informer.Run(make(chan struct{}))

	cacheSyncCtx, cacheSyncCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cacheSyncCancel()
	if synced := cache.WaitForCacheSync(cacheSyncCtx.Done(), informer.HasSynced); !synced {
		return nil, errors.New("informer failed to sync in time")
	}

	key := obj.GetName()
	if namespace != "" {
		key = namespace + "/" + key
	}

	w.watches[cacheKey] = func(target ctrlruntimeclient.Object) error {
		fromStore, exists, err := informer.GetIndexer().GetByKey(key)
		if err != nil {
			return fmt.Errorf("failed to get key %s from indexer: %w", key, err)
		}
		if !exists {
			return apierrors.NewNotFound(schema.GroupResource{Group: gvk.Group, Resource: gvk.Kind}, key)
		}
		obj := fromStore.(runtime.Object).DeepCopyObject()
		targetVal := reflect.ValueOf(target)
		objVal := reflect.ValueOf(obj)
		if !objVal.Type().AssignableTo(targetVal.Type()) {
			return fmt.Errorf("cache had type %s, but %s was asked for", objVal.Type(), targetVal.Type())
		}
		reflect.Indirect(targetVal).Set(reflect.Indirect(objVal))
		target.GetObjectKind().SetGroupVersionKind(gvk)
		return nil
	}

	return w.watches[cacheKey], nil
}
