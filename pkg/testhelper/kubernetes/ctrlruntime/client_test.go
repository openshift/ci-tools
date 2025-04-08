package client

import (
	"context"
	"testing"

	"github.com/google/go-cmp/cmp"

	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func convert(t *testing.T, scheme *runtime.Scheme, in ...ctrlclient.Object) []unstructured.Unstructured {
	out := make([]unstructured.Unstructured, 0, len(in))
	for _, obj := range in {
		u := unstructured.Unstructured{}
		if err := scheme.Convert(obj, &u, nil); err != nil {
			t.Fatalf("convert\n%v\nto unstructured: %s", obj, err)
		}
		out = append(out, u)
	}
	return out
}

func TestObjects(t *testing.T) {
	scheme := runtime.NewScheme()
	sb := runtime.NewSchemeBuilder(corev1.AddToScheme)
	if err := sb.AddToScheme(scheme); err != nil {
		t.Fatal("build scheme")
	}

	failOnErr := func(t *testing.T, msg string, err error) {
		if err != nil {
			t.Fatalf(msg+": %s", err)
		}
	}

	for _, tc := range []struct {
		name     string
		action   func(*testing.T, ctrlclient.Client)
		initObjs []ctrlclient.Object
		wantObjs []unstructured.Unstructured
	}{
		{
			name:     "No objects",
			wantObjs: []unstructured.Unstructured{},
		},
		{
			name:     "Return init objects",
			initObjs: []ctrlclient.Object{&corev1.Node{ObjectMeta: v1.ObjectMeta{Name: "foobar"}}},
			wantObjs: convert(t, scheme, &corev1.Node{
				ObjectMeta: v1.ObjectMeta{Name: "foobar", ResourceVersion: "999"},
			}),
		},
		{
			name:     "Create object",
			initObjs: []ctrlclient.Object{&corev1.Node{ObjectMeta: v1.ObjectMeta{Name: "foo"}}},
			action: func(t *testing.T, c ctrlclient.Client) {
				failOnErr(t, "create pod", c.Create(context.TODO(), &corev1.Pod{ObjectMeta: v1.ObjectMeta{Name: "bar"}}))
			},
			wantObjs: convert(t, scheme,
				&corev1.Node{ObjectMeta: v1.ObjectMeta{Name: "foo", ResourceVersion: "999"}},
				&corev1.Pod{ObjectMeta: v1.ObjectMeta{Name: "bar", ResourceVersion: "1"}},
			),
		},
		{
			name: "Update object",
			initObjs: []ctrlclient.Object{
				&corev1.Pod{
					ObjectMeta: v1.ObjectMeta{Name: "foo"},
					Spec:       corev1.PodSpec{RestartPolicy: corev1.RestartPolicyNever},
				},
			},
			action: func(t *testing.T, c ctrlclient.Client) {
				failOnErr(t, "update pod", c.Update(context.TODO(), &corev1.Pod{
					ObjectMeta: v1.ObjectMeta{Name: "foo"},
					Spec:       corev1.PodSpec{RestartPolicy: corev1.RestartPolicyAlways},
				}))
			},
			wantObjs: convert(t, scheme,
				&corev1.Pod{
					ObjectMeta: v1.ObjectMeta{Name: "foo", ResourceVersion: "1000"},
					Spec:       corev1.PodSpec{RestartPolicy: corev1.RestartPolicyAlways},
				},
			),
		},
		{
			name: "Patch object",
			initObjs: []ctrlclient.Object{
				&corev1.Pod{
					ObjectMeta: v1.ObjectMeta{Name: "foo"},
					Spec:       corev1.PodSpec{RestartPolicy: corev1.RestartPolicyNever},
				},
			},
			action: func(t *testing.T, c ctrlclient.Client) {
				failOnErr(t, "update pod", c.Patch(context.TODO(), &corev1.Pod{
					ObjectMeta: v1.ObjectMeta{Name: "foo"},
					Spec:       corev1.PodSpec{RestartPolicy: corev1.RestartPolicyAlways},
				}, ctrlclient.Merge))
			},
			wantObjs: convert(t, scheme,
				&corev1.Pod{
					ObjectMeta: v1.ObjectMeta{Name: "foo", ResourceVersion: "1000"},
					Spec:       corev1.PodSpec{RestartPolicy: corev1.RestartPolicyAlways},
				},
			),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			upstreamClient := fake.NewClientBuilder().
				WithObjects(tc.initObjs...).
				WithScheme(scheme).
				Build()

			client := NewFakeClient(upstreamClient, scheme, WithInitObjects(tc.initObjs...))

			if tc.action != nil {
				tc.action(t, client)
			}

			gotObjs, err := client.Objects()
			if err != nil {
				t.Fatalf("objects: %s", err)
			}

			if diff := cmp.Diff(tc.wantObjs, gotObjs); diff != "" {
				t.Errorf("unexpected objs:\n%s", diff)
			}
		})
	}
}
