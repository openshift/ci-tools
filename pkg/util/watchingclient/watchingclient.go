package watchingclient

import (
	"context"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/rest"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
)

type Client interface {
	ctrlruntimeclient.Client
	Watch(ctx context.Context, obj ctrlruntimeclient.ObjectList, opts ...ctrlruntimeclient.ListOption) (watch.Interface, error)
}

func New(cfg *rest.Config) (Client, error) {
	crClient, err := ctrlruntimeclient.New(cfg, ctrlruntimeclient.Options{})
	if err != nil {
		return nil, err
	}

	return &client{
		Client: crClient,
		restClientCache: &clientCache{
			config: cfg,
			scheme: crClient.Scheme(),
			mapper: crClient.RESTMapper(),
			codecs: serializer.NewCodecFactory(crClient.Scheme()),

			structuredResourceByType:   make(map[schema.GroupVersionKind]*resourceMeta),
			unstructuredResourceByType: make(map[schema.GroupVersionKind]*resourceMeta),
		},
		paramCodec: runtime.NewParameterCodec(crClient.Scheme()),
	}, nil
}

type client struct {
	ctrlruntimeclient.Client
	restClientCache *clientCache
	paramCodec      runtime.ParameterCodec
}

func (c *client) Watch(ctx context.Context, obj ctrlruntimeclient.ObjectList, opts ...ctrlruntimeclient.ListOption) (watch.Interface, error) {
	r, err := c.restClientCache.getResource(obj)
	if err != nil {
		return nil, err
	}
	listOpts := ctrlruntimeclient.ListOptions{}
	listOpts.ApplyOptions(opts)
	if listOpts.Raw == nil {
		listOpts.Raw = &metav1.ListOptions{}
	}
	listOpts.Raw.Watch = true
	return r.Get().
		NamespaceIfScoped(listOpts.Namespace, r.isNamespaced()).
		Resource(r.resource()).
		VersionedParams(listOpts.AsListOptions(), c.paramCodec).
		Watch(ctx)
}
