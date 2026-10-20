package dispatcher

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"

	dispatcherv1 "github.com/openshift/ci-tools/pkg/api/dispatcher/v1"
)

// reconciliationClock provides a deterministic completion time for health tests.
type reconciliationClock struct {
	now time.Time
}

func (c reconciliationClock) Now() time.Time { return c.now }

// reconciliationHealthStore injects failures before or after snapshot publication.
type reconciliationHealthStore struct {
	*MemoryOverrideStore
	listCalls  int
	failListAt int
	listErr    error
	statusErr  error
}

func (s *reconciliationHealthStore) List(ctx context.Context) ([]dispatcherv1.DispatchOverride, error) {
	s.listCalls++
	if s.listCalls == s.failListAt {
		return nil, s.listErr
	}
	return s.MemoryOverrideStore.List(ctx)
}

func (s *reconciliationHealthStore) UpdateStatus(ctx context.Context, override *dispatcherv1.DispatchOverride) error {
	if s.statusErr != nil {
		return s.statusErr
	}
	return s.MemoryOverrideStore.UpdateStatus(ctx, override)
}

// reconciliationMetricValue reads health gauges and counters without a separate registry.
func reconciliationMetricValue(t *testing.T, metric prometheus.Metric) float64 {
	t.Helper()
	var value dto.Metric
	if err := metric.Write(&value); err != nil {
		t.Fatal(err)
	}
	if value.Counter != nil {
		return value.GetCounter().GetValue()
	}
	return value.GetGauge().GetValue()
}

// TestReconciliationHealth distinguishes policy publication from successful reconciliation.
func TestReconciliationHealth(t *testing.T) {
	for _, tc := range []struct {
		name         string
		missingInput bool
		failListAt   int
		listErr      error
		statusErr    error
		wantError    string
		wantFailures float64
	}{
		{name: "successful unchanged policy"},
		{name: "missing baseline", missingInput: true, wantError: "baseline and inventory are not loaded", wantFailures: 1},
		{name: "override read failed", failListAt: 1, listErr: errors.New("read failed"), wantError: "read failed", wantFailures: 1},
		{name: "override status persistence failed", statusErr: errors.New("status failed"), wantError: "status failed", wantFailures: 1},
		{name: "final override read failed", failListAt: 2, listErr: errors.New("final read failed"), wantError: "final read failed", wantFailures: 1},
		{name: "canceled reconciliation", failListAt: 1, listErr: context.Canceled, wantError: "context canceled"},
		{name: "canceled status write", statusErr: context.Canceled, wantError: "context canceled"},
		{name: "canceled status write and failed final read", statusErr: context.Canceled, failListAt: 2, listErr: errors.New("final read failed"), wantError: "final read failed", wantFailures: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			control, store, manager, now := testControlPlane(t, ControlOptions{})
			previous := manager.Current()
			previousSuccess := reconciliationMetricValue(t, lastSuccessfulReconcileMetric)
			previousErrors := reconciliationMetricValue(t, reconcileErrorsMetric)
			if previousSuccess != float64(now.Unix()) {
				t.Fatalf("initial successful reconciliation timestamp = %v, want %v", previousSuccess, now.Unix())
			}
			clock := reconciliationClock{now: now.Add(time.Minute)}
			control.now = clock.Now
			baseline, inventory, blocked := control.inputs()
			if tc.missingInput {
				baseline = nil
			}
			control.UpdateBaseline(baseline, inventory, blocked)
			if got, want := reconciliationMetricValue(t, lastBaselineUpdateMetric), float64(clock.now.Unix()); got != want {
				t.Fatalf("baseline update timestamp = %v, want %v", got, want)
			}
			if tc.statusErr != nil {
				// An invalid override needs a status update but leaves the serving policy unchanged.
				capacity := int32(25)
				override := activeOverride("invalid", dispatcherv1.OverrideKindCapacity, "missing", &capacity, "", now)
				override.Name = override.Spec.ID
				if err := store.Create(context.Background(), &override); err != nil {
					t.Fatal(err)
				}
			}
			control.store = &reconciliationHealthStore{
				MemoryOverrideStore: store, failListAt: tc.failListAt, listErr: tc.listErr, statusErr: tc.statusErr,
			}
			err := control.Reconcile(context.Background())
			if tc.wantError == "" && err != nil || tc.wantError != "" && (err == nil || !strings.Contains(err.Error(), tc.wantError)) {
				t.Fatalf("Reconcile() error = %v, want %q", err, tc.wantError)
			}
			wantSuccess := previousSuccess
			if tc.wantError == "" {
				wantSuccess = float64(clock.now.Unix())
			}
			if got := reconciliationMetricValue(t, lastSuccessfulReconcileMetric); got != wantSuccess {
				t.Errorf("successful reconciliation timestamp = %v, want %v", got, wantSuccess)
			}
			if got := reconciliationMetricValue(t, reconcileErrorsMetric) - previousErrors; got != tc.wantFailures {
				t.Errorf("reconciliation error increase = %v, want %v", got, tc.wantFailures)
			}
			if diff := cmp.Diff(previous, manager.Current()); diff != "" {
				t.Errorf("serving snapshot changed (-want +got):\n%s", diff)
			}
			if got := reconciliationMetricValue(t, policyGenerationMetric); got != float64(previous.Generation) {
				t.Errorf("policy generation metric = %v, want %v", got, previous.Generation)
			}
			if got := reconciliationMetricValue(t, policyReadyMetric); got != 1 {
				t.Errorf("policy readiness metric = %v, want 1", got)
			}
		})
	}
}
