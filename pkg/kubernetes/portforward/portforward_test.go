package portforward

import (
	"context"
	"errors"
	"io"
	"net/url"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/rest"
)

type runTestCase struct {
	name             string
	opts             PortForwardOptions
	forwarderFactory func(t *testing.T, testCase *runTestCase) PortForwarder
	wantOpts         PortForwardOptions
	wantErr          error
}

func TestFoo(t *testing.T) {
	cmpOpts := func(a, b PortForwardOptions) string {
		return cmp.Diff(a, b, cmpopts.IgnoreUnexported(PortForwardOptions{}),
			cmpopts.IgnoreFields(PortForwardOptions{}, "PodGetter", "StopChannel", "Out", "ErrOut", "Config"))
	}

	for _, tc := range []runTestCase{
		{
			name: "Forward successfully",
			opts: PortForwardOptions{
				Namespace: "ns",
				PodName:   "foo",
				PodGetter: func(ctx context.Context, namespace, name string) (*corev1.Pod, error) {
					return &corev1.Pod{Status: corev1.PodStatus{Phase: corev1.PodRunning}}, nil
				},
				Address:     []string{"localhost"},
				Ports:       []string{"9999"},
				StopChannel: make(chan struct{}),
				Out:         io.Discard,
				ErrOut:      io.Discard,
				Config:      &rest.Config{},
			},
			forwarderFactory: func(t *testing.T, tc *runTestCase) PortForwarder {
				return func(method string, url *url.URL, readyChannel chan struct{}, opts PortForwardOptions) error {
					defer close(readyChannel)

					if method != "POST" {
						t.Errorf("want method POST but got %s", method)
						return nil
					}

					if url == nil {
						t.Error("url is nil")
						return nil
					}

					wantURL := "http://localhost/api/v1/namespaces/ns/pods/foo/portforward"
					if url.String() != wantURL {
						t.Errorf("want url %s but got %s", wantURL, url.String())
						return nil
					}

					if d := cmpOpts(tc.wantOpts, opts); d != "" {
						t.Errorf("unexpected diff:\n%s", d)
						return nil
					}

					return nil
				}
			},
			wantOpts: PortForwardOptions{
				Namespace: "ns",
				PodName:   "foo",
				Address:   []string{"localhost"},
				Ports:     []string{"9999"},
			},
		},
		{
			name: "Forwarder returns an error",
			opts: PortForwardOptions{
				Namespace: "ns",
				PodName:   "foo",
				PodGetter: func(ctx context.Context, namespace, name string) (*corev1.Pod, error) {
					return &corev1.Pod{Status: corev1.PodStatus{Phase: corev1.PodRunning}}, nil
				},
				Address:     []string{"localhost"},
				Ports:       []string{"9999"},
				StopChannel: make(chan struct{}),
				Out:         io.Discard,
				ErrOut:      io.Discard,
				Config:      &rest.Config{},
			},
			forwarderFactory: func(t *testing.T, tc *runTestCase) PortForwarder {
				return func(method string, url *url.URL, readyChannel chan struct{}, opts PortForwardOptions) error {
					defer close(readyChannel)
					return errors.New("fw err")
				}
			},
			wantErr: errors.New("fw err"),
		},
		{
			name: "Fails if the pod is not running",
			opts: PortForwardOptions{
				Namespace: "ns",
				PodName:   "foo",
				PodGetter: func(ctx context.Context, namespace, name string) (*corev1.Pod, error) {
					return &corev1.Pod{Status: corev1.PodStatus{Phase: corev1.PodPending}}, nil
				},
				Address:     []string{"localhost"},
				Ports:       []string{"9999"},
				StopChannel: make(chan struct{}),
				Out:         io.Discard,
				ErrOut:      io.Discard,
				Config:      &rest.Config{},
			},
			forwarderFactory: func(t *testing.T, tc *runTestCase) PortForwarder {
				return func(method string, url *url.URL, readyChannel chan struct{}, opts PortForwardOptions) error {
					return nil
				}
			},
			wantErr: errors.New("pod is not running - current status=Pending"),
		},
	} {
		t.Run(tc.name, func(tt *testing.T) {
			tt.Parallel()

			fw := tc.forwarderFactory(tt, &tc)
			err := <-Run(context.TODO(), fw, tc.opts)

			if err != nil && tc.wantErr == nil {
				t.Fatalf("want err nil but got: %v", err)
			}
			if err == nil && tc.wantErr != nil {
				t.Fatalf("want err %v but nil", tc.wantErr)
			}
			if err != nil && tc.wantErr != nil {
				if tc.wantErr.Error() != err.Error() {
					t.Fatalf("expect error %q but got %q", tc.wantErr.Error(), err.Error())
				}
			}
		})
	}
}
