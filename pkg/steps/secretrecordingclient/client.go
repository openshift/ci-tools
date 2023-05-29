package secretrecordingclient

import (
	"context"
	"sort"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/secrets"
)

// Wrap wraps the upstream client, allowing us to intercept any secrets it might interact with.
// This allows us to keep an up-to-date view of all of the secret data we may have come into contact
// with while executing, and ensure that we censor all of that data before outputting it.
func Wrap(upstream ctrlruntimeclient.WithWatch, censor *secrets.DynamicCensor) ctrlruntimeclient.WithWatch {
	return &client{
		upstream: upstream,
		censor:   censor,
	}
}

// client updates the censor when it interacts with secrets on the cluster
type client struct {
	upstream ctrlruntimeclient.WithWatch
	censor   *secrets.DynamicCensor
}

func (c *client) Get(ctx context.Context, key ctrlruntimeclient.ObjectKey, obj ctrlruntimeclient.Object, opts ...ctrlruntimeclient.GetOption) error {
	if err := c.upstream.Get(ctx, key, obj); err != nil {
		return err
	}
	c.record(obj)
	return nil
}

func (c *client) List(ctx context.Context, list ctrlruntimeclient.ObjectList, opts ...ctrlruntimeclient.ListOption) error {
	if err := c.upstream.List(ctx, list, opts...); err != nil {
		return err
	}
	c.recordList(list)
	return nil
}

func (c *client) Create(ctx context.Context, obj ctrlruntimeclient.Object, opts ...ctrlruntimeclient.CreateOption) error {
	if err := c.upstream.Create(ctx, obj, opts...); err != nil {
		return err
	}
	c.record(obj)
	return nil
}

func (c *client) Delete(ctx context.Context, obj ctrlruntimeclient.Object, opts ...ctrlruntimeclient.DeleteOption) error {
	return c.upstream.Delete(ctx, obj, opts...)
}

func (c *client) Update(ctx context.Context, obj ctrlruntimeclient.Object, opts ...ctrlruntimeclient.UpdateOption) error {
	if err := c.upstream.Update(ctx, obj, opts...); err != nil {
		return err
	}
	c.record(obj)
	return nil
}

func (c *client) Patch(ctx context.Context, obj ctrlruntimeclient.Object, patch ctrlruntimeclient.Patch, opts ...ctrlruntimeclient.PatchOption) error {
	if err := c.upstream.Patch(ctx, obj, patch, opts...); err != nil {
		return err
	}
	c.record(obj)
	return nil
}

func (c *client) DeleteAllOf(ctx context.Context, obj ctrlruntimeclient.Object, opts ...ctrlruntimeclient.DeleteAllOfOption) error {
	return c.upstream.DeleteAllOf(ctx, obj, opts...)
}

func (c *client) Watch(ctx context.Context, obj ctrlruntimeclient.ObjectList, opts ...ctrlruntimeclient.ListOption) (watch.Interface, error) {
	return c.upstream.Watch(ctx, obj, opts...)
}

func (c *client) recordList(obj ctrlruntimeclient.ObjectList) {
	// this code will not handle unstructured.Unstructured, but we do not
	// think that the controller-runtime client will ever give us that
	secretList, ok := obj.(*v1.SecretList)
	if !ok {
		return
	}
	for i := range secretList.Items {
		c.recordSecret(&secretList.Items[i])
	}
}

func (c *client) record(obj ctrlruntimeclient.Object) {
	// this code will not handle unstructured.Unstructured, but we do not
	// think that the controller-runtime client will ever give us that
	secret, ok := obj.(*v1.Secret)
	if !ok {
		return
	}
	c.recordSecret(secret)
}

func (c *client) recordSecret(secret *v1.Secret) {
	c.censor.AddSecrets(valuesToCensor(secret)...)
}

func valuesToCensor(secret *v1.Secret) []string {
	if _, skip := secret.Labels[api.SkipCensoringLabel]; skip {
		return nil
	}
	isServiceAccountCredential := secret.Type == v1.SecretTypeServiceAccountToken
	var values []string
	for key, value := range secret.Data {
		if isServiceAccountCredential && key == "namespace" {
			// this will in no case be a useful thing to censor
			continue
		}
		values = append(values, string(value))
	}
	for key, value := range secret.StringData {
		if isServiceAccountCredential && key == "namespace" {
			// this will in no case be a useful thing to censor
			continue
		}
		values = append(values, value)
	}
	for _, key := range []string{"openshift.io/token-secret.value", "kubectl.kubernetes.io/last-applied-configuration"} {
		if _, ok := secret.Annotations[key]; ok {
			values = append(values, secret.Annotations[key])
		}
	}
	sort.Slice(values, func(i, j int) bool {
		return values[i] < values[j]
	})
	return values
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
