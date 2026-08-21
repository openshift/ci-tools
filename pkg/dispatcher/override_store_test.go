package dispatcher

import (
	"context"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"

	dispatcherv1 "github.com/openshift/ci-tools/pkg/api/dispatcher/v1"
)

func TestMemoryOverrideStoreUpdatePreservesStatusAndRejectsStaleVersion(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryOverrideStore()
	override := &dispatcherv1.DispatchOverride{
		Spec:   dispatcherv1.DispatchOverrideSpec{ID: "override", Reason: "original"},
		Status: dispatcherv1.DispatchOverrideStatus{State: dispatcherv1.OverrideStateActive, Message: "published"},
	}
	override.Name = override.Spec.ID
	if err := store.Create(ctx, override); err != nil {
		t.Fatal(err)
	}
	stale := override.DeepCopy()
	incoming, err := store.Get(ctx, override.Name)
	if err != nil {
		t.Fatal(err)
	}
	incoming.Spec.Reason = "updated"
	incoming.Status = dispatcherv1.DispatchOverrideStatus{State: dispatcherv1.OverrideStateFailed, Message: "must not replace status"}
	if err := store.Update(ctx, incoming); err != nil {
		t.Fatal(err)
	}
	stored, err := store.Get(ctx, override.Name)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Spec.Reason != "updated" || stored.Status.State != dispatcherv1.OverrideStateActive || stored.Status.Message != "published" || stored.ResourceVersion != "2" {
		t.Fatalf("unexpected stored override after update: %#v", stored)
	}
	stale.Spec.Reason = "stale"
	if err := store.Update(ctx, stale); !apierrors.IsConflict(err) {
		t.Fatalf("stale resource version did not return a Kubernetes conflict: %v", err)
	}
}
