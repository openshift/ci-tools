package fake

import (
	"context"
	"errors"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	fakectrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/openshift/ci-tools/pkg/util/watchingclient"
)

type fakewathingclient struct {
	ctrlruntimeclient.Client
}

func (f *fakewathingclient) Watch(ctx context.Context, obj ctrlruntimeclient.ObjectList, opts ...ctrlruntimeclient.ListOption) (watch.Interface, error) {
	return nil, errors.New("not implemented")
}

func NewFakeClient(obj ...runtime.Object) watchingclient.Client {
	return &fakewathingclient{fakectrlruntimeclient.NewFakeClient(obj...)}
}
