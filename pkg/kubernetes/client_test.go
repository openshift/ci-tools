package kubernetes

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	coreapi "k8s.io/api/core/v1"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	fakectrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/openshift/ci-tools/pkg/testhelper"
)

func aPod() *coreapi.Pod {
	return &coreapi.Pod{
		ObjectMeta: meta.ObjectMeta{
			Name:      "p",
			Namespace: "ns",
		},
	}
}

func TestWaitForConditionOnObject(t *testing.T) {
	containerName := "c"
	podName := "p"
	ns := "ns"

	evaluateFunc := func(obj runtime.Object) (bool, error) {
		switch pod := obj.(type) {
		case *coreapi.Pod:
			for _, container := range pod.Status.ContainerStatuses {
				if container.Name == containerName {
					if container.State.Running != nil || container.State.Terminated != nil {
						return true, nil
					}
					break
				}
			}
		default:
			return false, fmt.Errorf("pod/%v ns/%v got an event that did not contain a pod: %v", podName, ns, obj)
		}
		return false, nil
	}

	testCases := []struct {
		name          string
		expected      error
		client        ctrlruntimeclient.WithWatch
		containerName string
		objectFunc    func(client ctrlruntimeclient.Client) error
	}{
		{
			name:   "happy path: pod",
			client: fakectrlruntimeclient.NewClientBuilder().WithRuntimeObjects(aPod()).Build(),
			objectFunc: func(client ctrlruntimeclient.Client) error {
				// wait for watch being ready
				time.Sleep(100 * time.Millisecond)
				ctx := context.TODO()
				pod := &coreapi.Pod{}
				if err := client.Get(ctx, ctrlruntimeclient.ObjectKey{Name: podName, Namespace: ns}, pod); err != nil {
					return err
				}
				pod.Status.ContainerStatuses = append(pod.Status.ContainerStatuses, coreapi.ContainerStatus{
					Name: "c",
					State: coreapi.ContainerState{
						Running: &coreapi.ContainerStateRunning{},
					},
				})
				if err := client.Status().Update(ctx, pod); err != nil {
					return err
				}
				return nil
			},
		},
		{
			name:       "timeout",
			client:     fakectrlruntimeclient.NewClientBuilder().WithRuntimeObjects(aPod()).Build(),
			objectFunc: func(client ctrlruntimeclient.Client) error { return nil },
			expected:   fmt.Errorf("timed out waiting for the condition"),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			readingDone := make(chan struct{})
			errChan := make(chan error)
			var errs []error
			go func() {
				for err := range errChan {
					errs = append(errs, err)
				}
				close(readingDone)
			}()
			go func() {
				if err := tc.objectFunc(tc.client); err != nil {
					errChan <- err
				}
			}()
			actual := WaitForConditionOnObject(context.TODO(), tc.client, ctrlruntimeclient.ObjectKey{Name: podName, Namespace: ns}, &coreapi.PodList{}, &coreapi.Pod{}, evaluateFunc, 300*time.Millisecond)
			close(errChan)
			<-readingDone
			if diff := cmp.Diff(tc.expected, actual, testhelper.EquateErrorMessage); diff != "" {
				t.Errorf("actualError does not match expectedError, diff: %s", diff)
			}
			if len(errs) > 0 {
				t.Errorf("unexpected error occurred: %v", utilerrors.NewAggregate(errs))
			}
		})
	}
}
