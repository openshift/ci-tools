package fake

import (
	"context"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	fakectrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/openshift/ci-tools/pkg/util/watchingclient"
)

type fakewathingclient struct {
	ctrlruntimeclient.WithWatch
}

func (f *fakewathingclient) Watch(ctx context.Context, obj ctrlruntimeclient.ObjectList, opts ...ctrlruntimeclient.ListOption) (watch.Interface, error) {
	return f.WithWatch.Watch(ctx, obj, opts...)
}

func NewFakeClient(obj ...runtime.Object) watchingclient.Client {
	return &fakewathingclient{fakectrlruntimeclient.NewFakeClient(obj...)}
}
