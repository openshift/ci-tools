package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	fakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"

	prpqv1 "github.com/openshift/ci-tools/pkg/api/pullrequestpayloadqualification/v1"
)

func TestExcludePrivateRuns(t *testing.T) {
	items := []prpqv1.PullRequestPayloadQualificationRun{
		{Spec: prpqv1.PullRequestPayloadTestSpec{}},
		{Spec: prpqv1.PullRequestPayloadTestSpec{Private: true}},
		{Spec: prpqv1.PullRequestPayloadTestSpec{}},
	}
	got := excludePrivateRuns(items)
	if len(got) != 2 {
		t.Fatalf("excludePrivateRuns() len = %d, want 2", len(got))
	}
	for _, run := range got {
		if run.Spec.Private {
			t.Fatalf("excludePrivateRuns() returned a private run")
		}
	}
}

func TestRunsListAndDetailsHidePrivate(t *testing.T) {
	if err := prpqv1.AddToScheme(scheme.Scheme); err != nil {
		t.Fatalf("add scheme: %v", err)
	}
	client := fakeclient.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(
		&prpqv1.PullRequestPayloadQualificationRun{
			ObjectMeta: metav1.ObjectMeta{Name: "public-run", Namespace: "ci"},
		},
		&prpqv1.PullRequestPayloadQualificationRun{
			ObjectMeta: metav1.ObjectMeta{Name: "private-run", Namespace: "ci"},
			Spec:       prpqv1.PullRequestPayloadTestSpec{Private: true},
		},
	).Build()
	s, err := newServer(client, context.TODO(), "ci")
	if err != nil {
		t.Fatalf("newServer: %v", err)
	}
	handler := s.RunsList()

	t.Run("list omits private", func(t *testing.T) {
		rr := httptest.NewRecorder()
		handler(rr, httptest.NewRequest(http.MethodGet, "/runs/", nil))
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
		}
		body := rr.Body.String()
		if !strings.Contains(body, "public-run") {
			t.Fatalf("list missing public-run: %s", body)
		}
		if strings.Contains(body, "private-run") {
			t.Fatalf("list exposed private-run: %s", body)
		}
	})

	t.Run("details 404 for private", func(t *testing.T) {
		rr := httptest.NewRecorder()
		handler(rr, httptest.NewRequest(http.MethodGet, "/runs/ci/private-run", nil))
		if rr.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want %d", rr.Code, http.StatusNotFound)
		}
	})
}
