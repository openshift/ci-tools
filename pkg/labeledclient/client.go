package labeledclient

import (
	"context"
	"maps"

	authapi "k8s.io/api/authorization/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/steps"
)

// Wrap wraps the upstream client, adding labels to objects created or updated
func Wrap(upstream ctrlruntimeclient.Client, jobSpec *api.JobSpec) ctrlruntimeclient.Client {
	return &client{
		upstream: upstream,
		jobSpec:  jobSpec,
	}
}

// WrapWithWatch does the same as Wrap, but for clients that support watching
func WrapWithWatch(upstream ctrlruntimeclient.WithWatch, jobSpec *api.JobSpec) ctrlruntimeclient.WithWatch {
	return &clientWithWatch{
		client: &client{
			upstream: upstream,
			jobSpec:  jobSpec,
		},
		upstream: upstream,
	}
}

type client struct {
	upstream ctrlruntimeclient.Client
	jobSpec  *api.JobSpec
}

func (c *client) Get(ctx context.Context, key ctrlruntimeclient.ObjectKey, obj ctrlruntimeclient.Object, opts ...ctrlruntimeclient.GetOption) error {
	return c.upstream.Get(ctx, key, obj)
}

func (c *client) List(ctx context.Context, list ctrlruntimeclient.ObjectList, opts ...ctrlruntimeclient.ListOption) error {
	return c.upstream.List(ctx, list, opts...)
}

func (c *client) Create(ctx context.Context, obj ctrlruntimeclient.Object, opts ...ctrlruntimeclient.CreateOption) error {
	c.addLabels(obj)
	return c.upstream.Create(ctx, obj, opts...)
}

func (c *client) Delete(ctx context.Context, obj ctrlruntimeclient.Object, opts ...ctrlruntimeclient.DeleteOption) error {
	return c.upstream.Delete(ctx, obj, opts...)
}

func (c *client) Update(ctx context.Context, obj ctrlruntimeclient.Object, opts ...ctrlruntimeclient.UpdateOption) error {
	return c.upstream.Update(ctx, obj, opts...)
}

func (c *client) Patch(ctx context.Context, obj ctrlruntimeclient.Object, patch ctrlruntimeclient.Patch, opts ...ctrlruntimeclient.PatchOption) error {
	return c.upstream.Patch(ctx, obj, patch, opts...)
}

func (c *client) DeleteAllOf(ctx context.Context, obj ctrlruntimeclient.Object, opts ...ctrlruntimeclient.DeleteAllOfOption) error {
	return c.upstream.DeleteAllOf(ctx, obj, opts...)
}

func (c *client) Status() ctrlruntimeclient.StatusWriter {
	return c.upstream.Status()
}

func (c *client) Scheme() *runtime.Scheme {
	return c.upstream.Scheme()
}

func (c *client) RESTMapper() meta.RESTMapper {
	return c.upstream.RESTMapper()
}

func (c *client) GroupVersionKindFor(obj runtime.Object) (schema.GroupVersionKind, error) {
	return c.upstream.GroupVersionKindFor(obj)
}

func (c *client) IsObjectNamespaced(obj runtime.Object) (bool, error) {
	return c.upstream.IsObjectNamespaced(obj)
}

func (c *client) SubResource(subResource string) ctrlruntimeclient.SubResourceClient {
	return c.upstream.SubResource(subResource)
}

func (c *client) addLabels(obj ctrlruntimeclient.Object) {
	// SelfSubjectAccessReview & LocalSubjectAccessReview do not hold labels
	switch obj.(type) {
	case *authapi.SelfSubjectAccessReview, *authapi.LocalSubjectAccessReview:
	default:
		obj.SetLabels(steps.LabelsFor(c.jobSpec, maps.Clone(obj.GetLabels()), ""))
	}
}

type clientWithWatch struct {
	*client
	upstream ctrlruntimeclient.WithWatch
}

func (c *clientWithWatch) Watch(ctx context.Context, obj ctrlruntimeclient.ObjectList, opts ...ctrlruntimeclient.ListOption) (watch.Interface, error) {
	return c.upstream.Watch(ctx, obj, opts...)
}
