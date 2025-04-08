package client

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

var _ ctrlclient.WithWatch = &FakeClient{}

type fakeClientOptions struct {
	initObjs []ctrlclient.Object
}

type applyOptionFunc func(o *fakeClientOptions)

func (f applyOptionFunc) apply(o *fakeClientOptions) {
	f(o)
}

type FakeClientOption interface {
	apply(o *fakeClientOptions)
}

// WithInitObjects keeps tracks of initObjs objects before the client is even used.
func WithInitObjects(initObjs ...ctrlclient.Object) FakeClientOption {
	return applyOptionFunc(func(o *fakeClientOptions) {
		o.initObjs = append(o.initObjs, initObjs...)
	})
}

// FakeClient implements the sigs.k8s.io/controller-runtime/pkg/client.WithWatch interface.
// This client keeps track of all seen objects and returns them as a []unstructured.Unstructured
// slice via the Objects method.
type FakeClient struct {
	gvks   map[string]schema.GroupVersionKind
	scheme *runtime.Scheme
	ctrlclient.WithWatch
}

func (c *FakeClient) Objects() ([]unstructured.Unstructured, error) {
	objs := make([]unstructured.Unstructured, 0)

	for _, gvk := range c.gvks {
		ul := unstructured.UnstructuredList{}
		ul.SetAPIVersion(gvk.GroupVersion().String())
		ul.SetKind(gvk.Kind + "List")

		if err := c.List(context.TODO(), &ul); err != nil {
			return nil, fmt.Errorf("list %s: %w", gvk.String(), err)
		}

		objs = append(objs, ul.Items...)
	}

	slices.SortFunc(objs, func(a, b unstructured.Unstructured) int {
		aKey := a.GetAPIVersion() + a.GetKind() + a.GetNamespace() + a.GetName()
		bKey := b.GetAPIVersion() + b.GetKind() + b.GetNamespace() + b.GetName()
		return strings.Compare(aKey, bKey)
	})

	return objs, nil
}

func (c *FakeClient) addUniqueGVKFrom(obj ctrlclient.Object) error {
	gvk, err := apiutil.GVKForObject(obj, c.scheme)
	if err != nil {
		return fmt.Errorf("get GVK for %s/%s: %w", obj.GetNamespace(), obj.GetName(), err)
	}

	s := gvk.String()
	if _, ok := c.gvks[s]; !ok {
		c.gvks[s] = gvk
	}

	return nil
}

func NewFakeClient(upstreamClient ctrlclient.WithWatch, scheme *runtime.Scheme, opts ...FakeClientOption) *FakeClient {
	defOpts := &fakeClientOptions{initObjs: make([]ctrlclient.Object, 0)}
	for _, opt := range opts {
		opt.apply(defOpts)
	}

	c := &FakeClient{
		gvks:   make(map[string]schema.GroupVersionKind),
		scheme: scheme,
	}

	for _, obj := range defOpts.initObjs {
		if err := c.addUniqueGVKFrom(obj); err != nil {
			panic(fmt.Sprintf("add init object %s/%s: %s", obj.GetNamespace(), obj.GetName(), err))
		}
	}

	c.WithWatch = interceptor.NewClient(upstreamClient, interceptor.Funcs{
		Create: func(ctx context.Context, client ctrlclient.WithWatch, obj ctrlclient.Object, opts ...ctrlclient.CreateOption) error {
			if err := c.addUniqueGVKFrom(obj); err != nil {
				return err
			}
			return client.Create(ctx, obj, opts...)
		},
		Update: func(ctx context.Context, client ctrlclient.WithWatch, obj ctrlclient.Object, opts ...ctrlclient.UpdateOption) error {
			if err := c.addUniqueGVKFrom(obj); err != nil {
				return err
			}
			return client.Update(ctx, obj, opts...)
		},
		Patch: func(ctx context.Context, client ctrlclient.WithWatch, obj ctrlclient.Object, patch ctrlclient.Patch, opts ...ctrlclient.PatchOption) error {
			if err := c.addUniqueGVKFrom(obj); err != nil {
				return err
			}
			return client.Patch(ctx, obj, patch, opts...)
		},
	})

	return c
}
