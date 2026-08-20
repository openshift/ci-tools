package dispatcher

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"

	dispatcherv1 "github.com/openshift/ci-tools/pkg/api/dispatcher/v1"
)

func testControlPlane(t *testing.T, options ControlOptions) (*ControlPlane, *MemoryOverrideStore, *SnapshotManager, time.Time) {
	t.Helper()
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	options.AllowedChannelID = "C-team"
	if options.EnableCapacity && options.SchedulerPropagationBound == 0 {
		options.SchedulerPropagationBound = 30 * time.Second
	}
	if options.EnableCapacity && options.AffectedDemandApproval == 0 {
		options.AffectedDemandApproval = 1000
	}
	if options.EnableCapacity && options.WriteSafetyCheck == nil {
		options.WriteSafetyCheck = func() error { return nil }
	}
	store := NewMemoryOverrideStore()
	manager := NewSnapshotManager("")
	control, err := NewControlPlane(manager, store, options)
	if err != nil {
		t.Fatal(err)
	}
	control.now = func() time.Time { return now }
	control.UpdateBaseline(map[string]ProwJobData{
		"job-a": {Cluster: "build01", Demand: 10, Capabilities: []string{"intranet"}},
		"job-b": {Cluster: "build01", Demand: 10},
		"job-c": {Cluster: "build02", Demand: 10, Capabilities: []string{"intranet"}},
	}, ClusterMap{
		"build01": {Provider: "aws", Capacity: 100, Capabilities: []string{"intranet"}},
		"build02": {Provider: "aws", Capacity: 100, Capabilities: []string{"intranet"}},
	}, nil)
	if err := control.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	return control, store, manager, now
}

func capacityPlanRequest() PlanRequest {
	capacity := int32(25)
	return PlanRequest{
		Kind: dispatcherv1.OverrideKindCapacity, Cluster: "build01", Capacity: &capacity,
		DurationSeconds: int64((2 * time.Hour) / time.Second), Reason: "INC-123", UserID: "U1", ChannelID: "C-team", IdempotencyKey: "plan-key",
	}
}

type concurrentCreateStore struct {
	*MemoryOverrideStore
	mu           sync.Mutex
	notFoundGets int
	release      chan struct{}
}

type reconcileFailureStore struct {
	*MemoryOverrideStore
	failListAfterCreate bool
	failListAfterUpdate bool
	failList            bool
}

func (s *reconcileFailureStore) List(ctx context.Context) ([]dispatcherv1.DispatchOverride, error) {
	if s.failList {
		return nil, errors.New("injected reconciliation failure")
	}
	return s.MemoryOverrideStore.List(ctx)
}

func (s *reconcileFailureStore) Create(ctx context.Context, override *dispatcherv1.DispatchOverride) error {
	if err := s.MemoryOverrideStore.Create(ctx, override); err != nil {
		return err
	}
	s.failList = s.failListAfterCreate
	return nil
}

func (s *reconcileFailureStore) Update(ctx context.Context, override *dispatcherv1.DispatchOverride) error {
	if err := s.MemoryOverrideStore.Update(ctx, override); err != nil {
		return err
	}
	s.failList = s.failListAfterUpdate
	return nil
}

func (s *concurrentCreateStore) Get(ctx context.Context, name string) (*dispatcherv1.DispatchOverride, error) {
	override, err := s.MemoryOverrideStore.Get(ctx, name)
	if !apierrors.IsNotFound(err) {
		return override, err
	}
	s.mu.Lock()
	s.notFoundGets++
	if s.notFoundGets == 2 {
		close(s.release)
	}
	release := s.release
	s.mu.Unlock()
	<-release
	return override, err
}

func TestControlPlanePlanIsReadOnlyIdempotentAndChannelGated(t *testing.T) {
	control, store, _, _ := testControlPlane(t, ControlOptions{})
	request := capacityPlanRequest()
	request.ChannelID = "C-other"
	if _, err := control.Plan(context.Background(), request); err == nil {
		t.Fatal("wrong-channel plan was accepted")
	}
	if overrides, _ := store.List(context.Background()); len(overrides) != 0 {
		t.Fatalf("wrong-channel plan changed state: %#v", overrides)
	}
	request.ChannelID = "C-team"
	first, err := control.Plan(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	second, err := control.Plan(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID || first.SourceGeneration != second.SourceGeneration {
		t.Fatalf("idempotent plan changed: %#v != %#v", first, second)
	}
	if overrides, _ := store.List(context.Background()); len(overrides) != 0 {
		t.Fatalf("plan was not read-only: %#v", overrides)
	}
}

func TestControlPlaneRejectsWritesWithoutMeasuredPropagationBound(t *testing.T) {
	_, err := NewControlPlane(NewSnapshotManager(""), NewMemoryOverrideStore(), ControlOptions{AllowedChannelID: "C-team", EnableCapacity: true})
	if err == nil || !strings.Contains(err.Error(), "propagation") {
		t.Fatalf("expected missing propagation measurement to block writes, got %v", err)
	}
}

func TestControlPlaneRechecksWriteSafety(t *testing.T) {
	var safetyErr error
	control, _, _, _ := testControlPlane(t, ControlOptions{
		EnableCapacity: true,
		WriteSafetyCheck: func() error {
			return safetyErr
		},
	})
	plan, err := control.Plan(context.Background(), capacityPlanRequest())
	if err != nil {
		t.Fatal(err)
	}
	safetyErr = errors.New("scheduler cache is too long")
	if _, err := control.Plan(context.Background(), capacityPlanRequest()); err == nil || !strings.Contains(err.Error(), "scheduler cache is too long") {
		t.Fatalf("expected the live safety check to block planning, got %v", err)
	}
	if _, err := control.Apply(context.Background(), plan.ID, ApplyRequest{UserID: "U1", ChannelID: "C-team", IdempotencyKey: "apply"}); err == nil || !strings.Contains(err.Error(), "scheduler cache is too long") {
		t.Fatalf("expected the live safety check to block apply, got %v", err)
	}
	safetyErr = nil
	override, err := control.Apply(context.Background(), plan.ID, ApplyRequest{UserID: "U1", ChannelID: "C-team", IdempotencyKey: "apply"})
	if err != nil {
		t.Fatal(err)
	}
	safetyErr = errors.New("scheduler cache changed")
	if _, err := control.Cancel(context.Background(), override.Spec.ID, CancelRequest{UserID: "U1", ChannelID: "C-team", IdempotencyKey: "cancel"}); err == nil || !strings.Contains(err.Error(), "scheduler cache changed") {
		t.Fatalf("expected the live safety check to block cancellation, got %v", err)
	}
}

func TestControlPlaneRejectsStalePlan(t *testing.T) {
	control, _, _, _ := testControlPlane(t, ControlOptions{EnableCapacity: true})
	plan, err := control.Plan(context.Background(), capacityPlanRequest())
	if err != nil {
		t.Fatal(err)
	}
	control.UpdateBaseline(map[string]ProwJobData{
		"new-job": {Cluster: "build01", Demand: 2},
	}, ClusterMap{
		"build01": {Provider: "aws", Capacity: 100},
		"build02": {Provider: "aws", Capacity: 100},
	}, nil)
	if err := control.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := control.Apply(context.Background(), plan.ID, ApplyRequest{UserID: "U1", ChannelID: "C-team", IdempotencyKey: "stale"}); err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("expected stale plan rejection, got %v", err)
	}
}

func TestControlPlaneRejectsStaleSecondApproval(t *testing.T) {
	control, store, _, _ := testControlPlane(t, ControlOptions{EnableCapacity: true, AffectedDemandApproval: 15})
	plan, err := control.Plan(context.Background(), capacityPlanRequest())
	if err != nil {
		t.Fatal(err)
	}
	pending, err := control.Apply(context.Background(), plan.ID, ApplyRequest{UserID: "U1", ChannelID: "C-team", IdempotencyKey: "apply-1"})
	if err != nil {
		t.Fatal(err)
	}
	if pending.Status.State != dispatcherv1.OverrideStatePending || len(pending.Spec.Approvals) != 1 {
		t.Fatalf("first approval was not pending: %#v", pending)
	}

	control.UpdateBaseline(map[string]ProwJobData{
		"job-a": {Cluster: "build01", Demand: 20, Capabilities: []string{"intranet"}},
		"job-b": {Cluster: "build01", Demand: 10},
		"job-c": {Cluster: "build02", Demand: 10, Capabilities: []string{"intranet"}},
	}, ClusterMap{
		"build01": {Provider: "aws", Capacity: 100, Capabilities: []string{"intranet"}},
		"build02": {Provider: "aws", Capacity: 100, Capabilities: []string{"intranet"}},
	}, nil)
	if err := control.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := control.Apply(context.Background(), plan.ID, ApplyRequest{UserID: "U2", ChannelID: "C-team", IdempotencyKey: "apply-2"}); err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("stale second approval was accepted: %v", err)
	}
	stored, err := store.Get(context.Background(), pending.Name)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored.Spec.Approvals) != 1 || stored.Status.State != dispatcherv1.OverrideStatePending {
		t.Fatalf("stale approval changed the pending override: %#v", stored)
	}
}

func TestControlPlaneQuarantinesInvalidStoredOverride(t *testing.T) {
	control, store, manager, now := testControlPlane(t, ControlOptions{EnableCapacity: true})
	capacity := int32(25)
	invalid := activeOverride("invalid", dispatcherv1.OverrideKindCapacity, "missing", &capacity, "", now)
	invalid.Name = invalid.Spec.ID
	invalid.Spec.SourceChannelID = "C-team"
	if err := store.Create(context.Background(), &invalid); err != nil {
		t.Fatal(err)
	}
	if err := control.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	stored, err := store.Get(context.Background(), invalid.Name)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status.State != dispatcherv1.OverrideStateFailed || !strings.Contains(stored.Status.Message, "unknown") {
		t.Fatalf("invalid override was not quarantined: %#v", stored.Status)
	}
	decision, found := manager.Lookup("job-a", now)
	if !found || decision.Cluster != "build01" || decision.OverrideID != "" {
		t.Fatalf("invalid override affected serving policy: %#v", decision)
	}
}

func TestControlPlaneRequiresExplicitVerifiedFallbackProtection(t *testing.T) {
	control, store, manager, now := testControlPlane(t, ControlOptions{EnableCapacity: true, EnableDrain: true})
	override := activeOverride("drain", dispatcherv1.OverrideKindDrain, "build01", nil, "", now)
	override.Name = override.Spec.ID
	override.Spec.SourceChannelID = "C-team"
	override.Spec.RequiredApprovals = 2
	override.Spec.Approvals = append(override.Spec.Approvals, dispatcherv1.DispatchApproval{UserID: "U2", At: metav1.NewTime(now)})
	override.Spec.FallbackConfirmed = true
	if err := store.Create(context.Background(), &override); err != nil {
		t.Fatal(err)
	}
	if err := control.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	control.UpdateBaseline(map[string]ProwJobData{"job": {Cluster: "build02", Demand: 1}}, ClusterMap{"build02": {Provider: "aws", Capacity: 100}}, sets.New[string]("build01"))
	if err := control.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	stored, err := store.Get(context.Background(), override.Name)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status.FallbackProtected || stored.Status.State != dispatcherv1.OverrideStateActive {
		t.Fatalf("desired blocked inventory was incorrectly treated as verified fallback protection: %#v", stored.Status)
	}
	digest, err := fallbackDigestForBaseline(manager.Current().Baseline)
	if err != nil {
		t.Fatal(err)
	}
	if err := control.ObserveFallbackProtection(FallbackProtectionObservation{
		FallbackDigest: digest, ProtectedClusters: []string{"build01"}, ValidUntil: now.Add(time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	if err := control.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	stored, err = store.Get(context.Background(), override.Name)
	if err != nil {
		t.Fatal(err)
	}
	if !stored.Status.FallbackProtected {
		t.Fatalf("explicit verified fallback protection was not observed: %#v", stored.Status)
	}
	control.now = func() time.Time { return now.Add(2 * time.Minute) }
	if control.verifiedFallbackProtection().Has("build01") {
		t.Fatal("expired fallback observation remained protected")
	}
}

func TestControlPlaneExpiryPublishesBaselineGeneration(t *testing.T) {
	control, store, manager, _ := testControlPlane(t, ControlOptions{EnableCapacity: true})
	plan, err := control.Plan(context.Background(), capacityPlanRequest())
	if err != nil {
		t.Fatal(err)
	}
	active, err := control.Apply(context.Background(), plan.ID, ApplyRequest{UserID: "U1", ChannelID: "C-team", IdempotencyKey: "apply-expiring"})
	if err != nil {
		t.Fatal(err)
	}
	activeGeneration := active.Status.PolicyGeneration
	control.now = func() time.Time { return active.Spec.ExpiresAt.Time }
	if err := control.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	expired, err := store.Get(context.Background(), active.Name)
	if err != nil {
		t.Fatal(err)
	}
	if expired.Status.State != dispatcherv1.OverrideStateExpired || expired.Status.PolicyGeneration <= activeGeneration {
		t.Fatalf("expiry did not publish a new baseline generation: %#v", expired.Status)
	}
	if current := manager.Current(); current == nil || len(current.OverrideIDs) != 0 {
		t.Fatalf("expired override remained in current policy: %#v", current)
	}
}

func TestControlPlaneFeatureGateQuarantinesStoredWrite(t *testing.T) {
	control, store, manager, now := testControlPlane(t, ControlOptions{})
	capacity := int32(25)
	override := activeOverride("disabled", dispatcherv1.OverrideKindCapacity, "build01", &capacity, "", now)
	override.Name = override.Spec.ID
	override.Spec.SourceChannelID = "C-team"
	if err := store.Create(context.Background(), &override); err != nil {
		t.Fatal(err)
	}
	if err := control.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	stored, err := store.Get(context.Background(), override.Name)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status.State != dispatcherv1.OverrideStateFailed || !strings.Contains(stored.Status.Message, "disabled") {
		t.Fatalf("disabled stored write was not quarantined: %#v", stored.Status)
	}
	decision, found := manager.Lookup("job-a", now)
	if !found || decision.OverrideID != "" || decision.Cluster != "build01" {
		t.Fatalf("disabled stored write changed policy: %#v", decision)
	}
}

func TestControlPlaneCapacityApplyCancelLifecycle(t *testing.T) {
	control, _, manager, now := testControlPlane(t, ControlOptions{EnableCapacity: true})
	plan, err := control.Plan(context.Background(), capacityPlanRequest())
	if err != nil {
		t.Fatal(err)
	}
	override, err := control.Apply(context.Background(), plan.ID, ApplyRequest{UserID: "U1", ChannelID: "C-team", IdempotencyKey: "apply-1"})
	if err != nil {
		t.Fatal(err)
	}
	if override.Status.State != dispatcherv1.OverrideStateActive || override.Status.PolicyGeneration <= plan.SourceGeneration {
		t.Fatalf("override did not become active: %#v", override)
	}
	decision, found := manager.Lookup("job-a", now)
	if !found || decision.PolicyGeneration != override.Status.PolicyGeneration {
		t.Fatalf("active generation was not serving: %#v", decision)
	}
	cancelled, err := control.Cancel(context.Background(), override.Spec.ID, CancelRequest{UserID: "U2", ChannelID: "C-team", IdempotencyKey: "cancel-1"})
	if err != nil {
		t.Fatal(err)
	}
	if cancelled.Status.State != dispatcherv1.OverrideStateRevoked {
		t.Fatalf("override was not revoked: %#v", cancelled.Status)
	}
	again, err := control.Cancel(context.Background(), override.Spec.ID, CancelRequest{UserID: "U2", ChannelID: "C-team", IdempotencyKey: "cancel-1"})
	if err != nil || again.Spec.RevokedAt == nil {
		t.Fatalf("idempotent cancel failed: %#v, %v", again, err)
	}
}

func TestControlPlaneReturnsDurableStateWhenPostWriteReconcileFails(t *testing.T) {
	t.Run("apply", func(t *testing.T) {
		control, baseStore, _, _ := testControlPlane(t, ControlOptions{EnableCapacity: true})
		store := &reconcileFailureStore{MemoryOverrideStore: baseStore, failListAfterCreate: true}
		control.store = store
		plan, err := control.Plan(context.Background(), capacityPlanRequest())
		if err != nil {
			t.Fatal(err)
		}
		override, err := control.Apply(context.Background(), plan.ID, ApplyRequest{UserID: "U1", ChannelID: "C-team", IdempotencyKey: "apply"})
		if err != nil || override == nil || override.Spec.PlanID != plan.ID {
			t.Fatalf("durable apply was reported as failed: override=%#v err=%v", override, err)
		}
	})

	t.Run("cancel and repeat", func(t *testing.T) {
		control, baseStore, _, _ := testControlPlane(t, ControlOptions{EnableCapacity: true})
		store := &reconcileFailureStore{MemoryOverrideStore: baseStore, failListAfterUpdate: true}
		control.store = store
		plan, err := control.Plan(context.Background(), capacityPlanRequest())
		if err != nil {
			t.Fatal(err)
		}
		override, err := control.Apply(context.Background(), plan.ID, ApplyRequest{UserID: "U1", ChannelID: "C-team", IdempotencyKey: "apply"})
		if err != nil {
			t.Fatal(err)
		}
		cancelRequest := CancelRequest{UserID: "U2", ChannelID: "C-team", IdempotencyKey: "cancel"}
		for range 2 {
			cancelled, err := control.Cancel(context.Background(), override.Spec.ID, cancelRequest)
			if err != nil || cancelled == nil || cancelled.Spec.RevokedAt == nil {
				t.Fatalf("durable cancellation was reported as failed: override=%#v err=%v", cancelled, err)
			}
		}
	})
}

func TestParsePositiveDurationRejectsSubSecondValues(t *testing.T) {
	for _, value := range []string{"1ns", "999ms"} {
		if _, err := ParsePositiveDuration(value); err == nil {
			t.Fatalf("sub-second duration %q was accepted", value)
		}
	}
	if seconds, err := ParsePositiveDuration("1.5s"); err != nil || seconds != 1 {
		t.Fatalf("duration of at least one second was rejected or converted incorrectly: seconds=%d err=%v", seconds, err)
	}
}

func TestControlPlaneLargeCapacityChangeRequiresSecondApproval(t *testing.T) {
	control, _, _, _ := testControlPlane(t, ControlOptions{EnableCapacity: true, AffectedDemandApproval: 15})
	plan, err := control.Plan(context.Background(), capacityPlanRequest())
	if err != nil {
		t.Fatal(err)
	}
	if plan.RequiredApprovals != 2 || plan.Impact.AffectedDemand != 20 {
		t.Fatalf("large capacity change did not require two approvals: %#v", plan)
	}
}

func TestControlPlaneMergesConcurrentInitialApprovals(t *testing.T) {
	control, baseStore, _, _ := testControlPlane(t, ControlOptions{EnableCapacity: true, AffectedDemandApproval: 15})
	store := &concurrentCreateStore{MemoryOverrideStore: baseStore, release: make(chan struct{})}
	control.store = store
	plan, err := control.Plan(context.Background(), capacityPlanRequest())
	if err != nil {
		t.Fatal(err)
	}

	start := make(chan struct{})
	errs := make(chan error, 2)
	for _, user := range []string{"U1", "U2"} {
		user := user
		go func() {
			<-start
			_, err := control.Apply(context.Background(), plan.ID, ApplyRequest{UserID: user, ChannelID: "C-team", IdempotencyKey: "apply-" + user})
			errs <- err
		}()
	}
	close(start)
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	overrides, err := store.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(overrides) != 1 || uniqueApprovals(&overrides[0]) != 2 || overrides[0].Status.State != dispatcherv1.OverrideStateActive {
		t.Fatalf("concurrent approvals were not merged: %#v", overrides)
	}
}

func TestControlPlaneDrainRequiresDistinctSecondApprovalAndFallbackConfirmation(t *testing.T) {
	control, _, _, _ := testControlPlane(t, ControlOptions{EnableCapacity: true, EnableDrain: true})
	request := PlanRequest{
		Kind: dispatcherv1.OverrideKindDrain, Cluster: "build01", DurationSeconds: 3600,
		Reason: "INC-456", UserID: "U1", ChannelID: "C-team", IdempotencyKey: "drain-plan",
	}
	plan, err := control.Plan(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if plan.RequiredApprovals != 2 || plan.FallbackProtected {
		t.Fatalf("unexpected drain safety policy: %#v", plan)
	}
	if _, err := control.Apply(context.Background(), plan.ID, ApplyRequest{UserID: "U1", ChannelID: "C-team", IdempotencyKey: "apply-1"}); err == nil || !strings.Contains(err.Error(), "confirmation") {
		t.Fatalf("unconfirmed drain was accepted: %v", err)
	}
	pending, err := control.Apply(context.Background(), plan.ID, ApplyRequest{UserID: "U1", ChannelID: "C-team", IdempotencyKey: "apply-1", FallbackConfirmed: true})
	if err != nil {
		t.Fatal(err)
	}
	if pending.Status.State != dispatcherv1.OverrideStatePending || len(pending.Spec.Approvals) != 1 {
		t.Fatalf("first drain approval was not pending: %#v", pending)
	}
	repeated, err := control.Apply(context.Background(), plan.ID, ApplyRequest{UserID: "U1", ChannelID: "C-team", IdempotencyKey: "apply-retry", FallbackConfirmed: true})
	if err != nil || len(repeated.Spec.Approvals) != 1 {
		t.Fatalf("same user counted twice: %#v, %v", repeated, err)
	}
	active, err := control.Apply(context.Background(), plan.ID, ApplyRequest{UserID: "U2", ChannelID: "C-team", IdempotencyKey: "apply-2", FallbackConfirmed: true})
	if err != nil {
		t.Fatal(err)
	}
	if active.Status.State != dispatcherv1.OverrideStateActive || len(active.Spec.Approvals) != 2 {
		t.Fatalf("second approval did not activate drain: %#v", active)
	}
}

func TestControlServerAuthenticationAndPlan(t *testing.T) {
	control, _, _, _ := testControlPlane(t, ControlOptions{})
	server := NewControlServer(control, func() []byte { return []byte("secret") })
	requestBody, err := json.Marshal(capacityPlanRequest())
	if err != nil {
		t.Fatal(err)
	}
	unauthorized := httptest.NewRecorder()
	server.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodPost, "/control/v1/plans", bytes.NewReader(requestBody)))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("expected unauthorized, got %d", unauthorized.Code)
	}
	authorizedRequest := httptest.NewRequest(http.MethodPost, "/control/v1/plans", bytes.NewReader(requestBody))
	authorizedRequest.Header.Set("Authorization", "Bearer secret")
	authorized := httptest.NewRecorder()
	server.ServeHTTP(authorized, authorizedRequest)
	if authorized.Code != http.StatusOK {
		t.Fatalf("expected success, got %d: %s", authorized.Code, authorized.Body.String())
	}
}

func TestFallbackObservationServerAuthenticationAndDigestBinding(t *testing.T) {
	control, _, manager, now := testControlPlane(t, ControlOptions{})
	server := NewFallbackObservationServer(control, func() []byte { return []byte("observer-secret") })
	digest, err := fallbackDigestForBaseline(manager.Current().Baseline)
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(FallbackProtectionObservation{FallbackDigest: digest, ValidUntil: now.Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	unauthorized := httptest.NewRecorder()
	server.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodPost, "/fallback-observer/v1/protection", bytes.NewReader(body)))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("expected unauthorized, got %d", unauthorized.Code)
	}
	staleBody, err := json.Marshal(FallbackProtectionObservation{FallbackDigest: "stale", ValidUntil: now.Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	staleRequest := httptest.NewRequest(http.MethodPost, "/fallback-observer/v1/protection", bytes.NewReader(staleBody))
	staleRequest.Header.Set("Authorization", "Bearer observer-secret")
	stale := httptest.NewRecorder()
	server.ServeHTTP(stale, staleRequest)
	if stale.Code != http.StatusConflict {
		t.Fatalf("expected stale digest conflict, got %d: %s", stale.Code, stale.Body.String())
	}
	request := httptest.NewRequest(http.MethodPost, "/fallback-observer/v1/protection", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer observer-secret")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("expected observation to be accepted, got %d: %s", response.Code, response.Body.String())
	}
}
