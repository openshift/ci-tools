package namespacewrapper

import (
	"context"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func New(upstream ctrlruntimeclient.Client, namespace string) ctrlruntimeclient.Client {
	return &namespacewrapingClient{upstream: upstream, namespace: namespace}
}

type namespacewrapingClient struct {
	upstream  ctrlruntimeclient.Client
	namespace string
}

func (n *namespacewrapingClient) Get(ctx context.Context, key ctrlruntimeclient.ObjectKey, obj runtime.Object) error {
	key.Namespace = n.namespace
	return n.upstream.Get(ctx, key, obj)
}

func (n *namespacewrapingClient) List(ctx context.Context, list runtime.Object, opts ...ctrlruntimeclient.ListOption) error {
	opts = append(opts, ctrlruntimeclient.InNamespace(n.namespace))
	return n.upstream.List(ctx, list, opts...)
}

func (n *namespacewrapingClient) Create(ctx context.Context, obj runtime.Object, opts ...ctrlruntimeclient.CreateOption) error {
	obj.(metav1.Object).SetNamespace(n.namespace)
	return n.upstream.Create(ctx, obj, opts...)
}

func (n *namespacewrapingClient) Delete(ctx context.Context, obj runtime.Object, opts ...ctrlruntimeclient.DeleteOption) error {
	obj.(metav1.Object).SetNamespace(n.namespace)
	return n.upstream.Delete(ctx, obj, opts...)
}

func (n *namespacewrapingClient) Update(ctx context.Context, obj runtime.Object, opts ...ctrlruntimeclient.UpdateOption) error {
	obj.(metav1.Object).SetNamespace(n.namespace)
	return n.upstream.Update(ctx, obj, opts...)
}

func (n *namespacewrapingClient) Patch(ctx context.Context, obj runtime.Object, patch ctrlruntimeclient.Patch, opts ...ctrlruntimeclient.PatchOption) error {
	obj.(metav1.Object).SetNamespace(n.namespace)
	return n.upstream.Patch(ctx, obj, patch, opts...)
}

func (n *namespacewrapingClient) DeleteAllOf(ctx context.Context, obj runtime.Object, opts ...ctrlruntimeclient.DeleteAllOfOption) error {
	opts = append(opts, ctrlruntimeclient.InNamespace(n.namespace))
	return n.upstream.DeleteAllOf(ctx, obj, opts...)
}

func (n *namespacewrapingClient) Status() ctrlruntimeclient.StatusWriter {
	return &statusNamespaceWrapper{upstream: n.upstream.Status(), namespace: n.namespace}
}

type statusNamespaceWrapper struct {
	upstream  ctrlruntimeclient.StatusWriter
	namespace string
}

func (n *statusNamespaceWrapper) Update(ctx context.Context, obj runtime.Object, opts ...ctrlruntimeclient.UpdateOption) error {
	obj.(metav1.Object).SetNamespace(n.namespace)
	return n.upstream.Update(ctx, obj, opts...)
}

func (n *statusNamespaceWrapper) Patch(ctx context.Context, obj runtime.Object, patch ctrlruntimeclient.Patch, opts ...ctrlruntimeclient.PatchOption) error {
	obj.(metav1.Object).SetNamespace(n.namespace)
	return n.upstream.Patch(ctx, obj, patch, opts...)
}
