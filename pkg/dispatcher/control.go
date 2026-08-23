package dispatcher

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/util/retry"

	dispatcherv1 "github.com/openshift/ci-tools/pkg/api/dispatcher/v1"
)

const maxAuditHistory = 50

var controlIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// ControlOptions defines safety policy and staged feature gates for runtime controls.
type ControlOptions struct {
	AllowedChannelID          string
	MaxTTL                    time.Duration
	MaxDrainTTL               time.Duration
	PlanTTL                   time.Duration
	AffectedDemandApproval    float64
	EnableCapacity            bool
	EnableDrain               bool
	EnableCapabilityScope     bool
	ReconcileInterval         time.Duration
	SchedulerPropagationBound time.Duration
	WriteSafetyCheck          func() error
}

func (o *ControlOptions) defaults() {
	if o.MaxTTL == 0 {
		o.MaxTTL = 24 * time.Hour
	}
	if o.MaxDrainTTL == 0 {
		o.MaxDrainTTL = 2 * time.Hour
	}
	if o.PlanTTL == 0 {
		o.PlanTTL = 15 * time.Minute
	}
	if o.ReconcileInterval == 0 {
		o.ReconcileInterval = 5 * time.Second
	}
}

// Validate checks control-plane safety configuration.
func (o *ControlOptions) Validate() error {
	o.defaults()
	if o.AllowedChannelID == "" {
		return errors.New("allowed Slack channel ID is required")
	}
	if o.MaxTTL <= 0 || o.MaxDrainTTL <= 0 || o.PlanTTL <= 0 || o.ReconcileInterval <= 0 {
		return errors.New("control durations must be positive")
	}
	if o.MaxDrainTTL > o.MaxTTL {
		return errors.New("maximum drain TTL cannot exceed maximum override TTL")
	}
	if o.AffectedDemandApproval < 0 {
		return errors.New("affected-demand approval threshold cannot be negative")
	}
	if o.EnableDrain && !o.EnableCapacity {
		return errors.New("drain operations require capacity operations to be enabled")
	}
	if o.EnableCapabilityScope && !o.EnableCapacity {
		return errors.New("capability-scoped operations require capacity operations to be enabled")
	}
	if o.EnableCapacity && (o.SchedulerPropagationBound <= 0 || o.SchedulerPropagationBound > 30*time.Second) {
		return errors.New("write operations require a measured scheduler propagation bound no greater than 30 seconds")
	}
	if o.EnableCapacity && o.AffectedDemandApproval <= 0 {
		return errors.New("write operations require a positive affected-demand threshold for second approval")
	}
	if o.EnableCapacity && o.WriteSafetyCheck == nil {
		return errors.New("write operations require verification of the Prow scheduler cache bound")
	}
	if o.EnableCapacity {
		if err := o.WriteSafetyCheck(); err != nil {
			return fmt.Errorf("write safety verification failed: %w", err)
		}
	}
	return nil
}

// PlanRequest is a read-only request to preview a temporary override.
type PlanRequest struct {
	Kind              dispatcherv1.OverrideKind `json:"kind"`
	Cluster           string                    `json:"cluster"`
	Capability        string                    `json:"capability,omitempty"`
	Capacity          *int32                    `json:"capacity,omitempty"`
	DurationSeconds   int64                     `json:"durationSeconds"`
	Reason            string                    `json:"reason"`
	IncidentURL       string                    `json:"incidentURL,omitempty"`
	UserID            string                    `json:"userID"`
	ChannelID         string                    `json:"channelID"`
	IdempotencyKey    string                    `json:"idempotencyKey"`
	FallbackConfirmed bool                      `json:"fallbackConfirmed,omitempty"`
}

// DispatchPlan is an immutable impact preview tied to one policy generation.
type DispatchPlan struct {
	ID                         string         `json:"id"`
	CreatedAt                  time.Time      `json:"createdAt"`
	ExpiresAt                  time.Time      `json:"expiresAt"`
	SourceGeneration           uint64         `json:"sourceGeneration"`
	PolicyInputDigest          string         `json:"policyInputDigest"`
	Request                    PlanRequest    `json:"request"`
	Impact                     CompileSummary `json:"impact"`
	RequiredApprovals          int32          `json:"requiredApprovals"`
	FallbackProtected          bool           `json:"fallbackProtected"`
	PropagationBound           time.Duration  `json:"propagationBound"`
	CurrentEffectiveCapacity   int            `json:"currentEffectiveCapacity"`
	RequestedEffectiveCapacity int            `json:"requestedEffectiveCapacity"`
}

// ApplyRequest applies or approves a plan.
type ApplyRequest struct {
	UserID            string `json:"userID"`
	ChannelID         string `json:"channelID"`
	IdempotencyKey    string `json:"idempotencyKey"`
	FallbackConfirmed bool   `json:"fallbackConfirmed,omitempty"`
	SlackThreadTS     string `json:"slackThreadTS,omitempty"`
}

// CancelRequest revokes an override idempotently.
type CancelRequest struct {
	UserID         string `json:"userID"`
	ChannelID      string `json:"channelID"`
	IdempotencyKey string `json:"idempotencyKey"`
}

// BindThreadRequest records the Slack thread used for lifecycle notifications.
type BindThreadRequest struct {
	UserID    string `json:"userID"`
	ChannelID string `json:"channelID"`
	ThreadTS  string `json:"threadTS"`
}

// ControlStatus describes the serving policy and optional cluster state.
type ControlStatus struct {
	Ready             bool                            `json:"ready"`
	Generation        uint64                          `json:"generation,omitempty"`
	PolicyInputDigest string                          `json:"policyInputDigest,omitempty"`
	SnapshotChecksum  string                          `json:"snapshotChecksum,omitempty"`
	GeneratedAt       time.Time                       `json:"generatedAt,omitempty"`
	Cluster           string                          `json:"cluster,omitempty"`
	ClusterInfo       *ClusterInfo                    `json:"clusterInfo,omitempty"`
	EffectiveCapacity *int                            `json:"effectiveCapacity,omitempty"`
	Overrides         []dispatcherv1.DispatchOverride `json:"overrides,omitempty"`
	FallbackProtected bool                            `json:"fallbackProtected"`
}

// FallbackProtectionObservation reports a short-lived external verification of
// the fallback assignments currently loaded by Prow.
type FallbackProtectionObservation struct {
	FallbackDigest    string    `json:"fallbackDigest"`
	ProtectedClusters []string  `json:"protectedClusters"`
	ValidUntil        time.Time `json:"validUntil"`
}

const maxFallbackObservationTTL = 5 * time.Minute

// ControlPlane compiles durable overrides into serving snapshots and implements plan/apply/cancel.
type ControlPlane struct {
	manager *SnapshotManager
	store   OverrideStore
	options ControlOptions
	now     func() time.Time

	mu          sync.Mutex
	reconcileMu sync.Mutex
	baseline    map[string]ProwJobData
	inventory   ClusterMap
	blocked     sets.Set[string]
	// fallbackProtected is populated only from an external observation that Prow
	// loaded the matching Git fallback generation. Desired inventory is not proof.
	fallbackProtected        sets.Set[string]
	fallbackDigest           string
	fallbackProtectionExpiry time.Time
	plans                    map[string]DispatchPlan
	wake                     chan struct{}
}

// NewControlPlane creates a single-replica control plane.
func NewControlPlane(manager *SnapshotManager, store OverrideStore, options ControlOptions) (*ControlPlane, error) {
	if manager == nil || store == nil {
		return nil, errors.New("snapshot manager and override store are required")
	}
	if err := options.Validate(); err != nil {
		return nil, err
	}
	return &ControlPlane{manager: manager, store: store, options: options, now: time.Now, fallbackProtected: sets.New[string](), plans: make(map[string]DispatchPlan), wake: make(chan struct{}, 1)}, nil
}

// UpdateBaseline installs the latest durable Git baseline inputs and triggers compilation.
func (c *ControlPlane) UpdateBaseline(baseline map[string]ProwJobData, inventory ClusterMap, blocked sets.Set[string]) {
	c.mu.Lock()
	c.baseline = copyBaseline(baseline)
	c.inventory = copyInventory(inventory)
	c.blocked = blocked.Clone()
	if digest, err := fallbackDigestForBaseline(baseline); err != nil || digest != c.fallbackDigest {
		c.fallbackProtected = sets.New[string]()
		c.fallbackDigest = ""
		c.fallbackProtectionExpiry = time.Time{}
	}
	c.mu.Unlock()
	c.trigger()
}

// ObserveFallbackProtection installs a digest-bound, short-lived observation
// from a trusted external process that inspects Prow's loaded fallback state.
func (c *ControlPlane) ObserveFallbackProtection(observation FallbackProtectionObservation) error {
	now := c.now().UTC()
	if !observation.ValidUntil.After(now) || observation.ValidUntil.After(now.Add(maxFallbackObservationTTL)) {
		return fmt.Errorf("fallback observation validUntil must be after now and at most %s in the future", maxFallbackObservationTTL)
	}
	snapshot := c.manager.Current()
	if snapshot == nil || !c.manager.Ready() {
		return errors.New("dispatcher has no ready policy generation")
	}
	expectedDigest, err := fallbackDigestForBaseline(snapshot.Baseline)
	if err != nil {
		return fmt.Errorf("calculate current fallback digest: %w", err)
	}
	if observation.FallbackDigest == "" || observation.FallbackDigest != expectedDigest {
		return errors.New("fallback observation does not match the current baseline")
	}
	protected := sets.New[string]()
	for _, cluster := range observation.ProtectedClusters {
		if strings.TrimSpace(cluster) == "" {
			return errors.New("fallback protected cluster names must not be empty")
		}
		if protected.Has(cluster) {
			return fmt.Errorf("fallback protected cluster %q is duplicated", cluster)
		}
		for jobName, assignment := range snapshot.Baseline {
			if assignment.Cluster == cluster {
				return fmt.Errorf("fallback cluster %q is still targeted by job %q", cluster, jobName)
			}
		}
		protected.Insert(cluster)
	}
	c.mu.Lock()
	c.fallbackProtected = protected
	c.fallbackDigest = observation.FallbackDigest
	c.fallbackProtectionExpiry = observation.ValidUntil.UTC()
	c.mu.Unlock()
	c.trigger()
	return nil
}

func (c *ControlPlane) trigger() {
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

func (c *ControlPlane) inputs() (map[string]ProwJobData, ClusterMap, sets.Set[string]) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return copyBaseline(c.baseline), copyInventory(c.inventory), c.blocked.Clone()
}

func (c *ControlPlane) verifiedFallbackProtection() sets.Set[string] {
	c.mu.Lock()
	protected := c.fallbackProtected.Clone()
	digest := c.fallbackDigest
	expires := c.fallbackProtectionExpiry
	c.mu.Unlock()
	if digest == "" || !expires.After(c.now().UTC()) {
		return sets.New[string]()
	}
	snapshot := c.manager.Current()
	if snapshot == nil || !c.manager.Ready() {
		return sets.New[string]()
	}
	currentDigest, err := fallbackDigestForBaseline(snapshot.Baseline)
	if err != nil || currentDigest != digest {
		return sets.New[string]()
	}
	return protected
}

// FallbackAssignmentDigest returns the stable digest of the job-to-cluster
// assignments observed in Prow's loaded fallback configuration.
func FallbackAssignmentDigest(assignments map[string]string) (string, error) {
	return digestJSON(assignments)
}

func fallbackDigestForBaseline(baseline map[string]ProwJobData) (string, error) {
	assignments := make(map[string]string, len(baseline))
	for jobName, assignment := range baseline {
		assignments[jobName] = assignment.Cluster
	}
	return FallbackAssignmentDigest(assignments)
}

// Run reconciles baseline and override changes until ctx is cancelled.
func (c *ControlPlane) Run(ctx context.Context) {
	delay := c.options.ReconcileInterval
	for {
		if err := c.Reconcile(ctx); err != nil && !errors.Is(err, context.Canceled) {
			logrus.WithError(err).Error("failed to reconcile dispatcher runtime policy; retaining the last valid generation")
			delay *= 2
			if delay > time.Minute {
				delay = time.Minute
			}
		} else {
			delay = c.options.ReconcileInterval
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return
		case <-timer.C:
		case <-c.wake:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			delay = c.options.ReconcileInterval
		}
	}
}

func desiredOverrideState(override *dispatcherv1.DispatchOverride, now time.Time) dispatcherv1.OverrideState {
	if override.Spec.RevokedAt != nil {
		return dispatcherv1.OverrideStateRevoked
	}
	if !now.Before(override.Spec.ExpiresAt.Time) {
		return dispatcherv1.OverrideStateExpired
	}
	if now.Before(override.Spec.StartsAt.Time) || uniqueApprovals(override) < int(override.Spec.RequiredApprovals) {
		return dispatcherv1.OverrideStatePending
	}
	return dispatcherv1.OverrideStateActive
}

func appendAudit(status *dispatcherv1.DispatchOverrideStatus, event dispatcherv1.DispatchAuditEvent) {
	status.History = append(status.History, event)
	if len(status.History) > maxAuditHistory {
		status.History = append([]dispatcherv1.DispatchAuditEvent(nil), status.History[len(status.History)-maxAuditHistory:]...)
	}
}

func (c *ControlPlane) updateStatus(ctx context.Context, override *dispatcherv1.DispatchOverride, state dispatcherv1.OverrideState, snapshot *PolicySnapshot, fallbackProtected bool, message string) error {
	if override.Status.State == state && override.Status.ObservedGeneration == override.Generation &&
		override.Status.Message == message &&
		override.Status.FallbackProtected == fallbackProtected &&
		(snapshot == nil || override.Status.PolicyGeneration == snapshot.Generation && override.Status.SnapshotChecksum == snapshot.Checksum) {
		return nil
	}
	previous := override.Status.State
	override.Status.State = state
	override.Status.ObservedGeneration = override.Generation
	override.Status.Message = message
	override.Status.FallbackProtected = fallbackProtected
	if snapshot != nil {
		override.Status.PolicyGeneration = snapshot.Generation
		override.Status.SnapshotChecksum = snapshot.Checksum
	}
	if previous != state {
		actor := override.Spec.CreatedBy
		if state == dispatcherv1.OverrideStateRevoked && override.Spec.RevokedBy != "" {
			actor = override.Spec.RevokedBy
		} else if len(override.Spec.Approvals) > 0 {
			actor = override.Spec.Approvals[len(override.Spec.Approvals)-1].UserID
		}
		appendAudit(&override.Status, dispatcherv1.DispatchAuditEvent{At: metav1.NewTime(c.now().UTC()), State: state, Actor: actor, Message: message, Generation: override.Status.PolicyGeneration})
		var transitionAt time.Time
		switch state {
		case dispatcherv1.OverrideStateActive:
			transitionAt = override.Spec.StartsAt.Time
		case dispatcherv1.OverrideStateExpired:
			transitionAt = override.Spec.ExpiresAt.Time
		case dispatcherv1.OverrideStateRevoked:
			if override.Spec.RevokedAt != nil {
				transitionAt = override.Spec.RevokedAt.Time
			}
		}
		if !transitionAt.IsZero() {
			delay := c.now().Sub(transitionAt).Seconds()
			if delay >= 0 {
				overrideActivationMetric.WithLabelValues(strings.ToLower(string(state))).Observe(delay)
			}
		}
	}
	return c.store.UpdateStatus(ctx, override)
}

// Reconcile compiles and publishes one complete generation, preserving the last good snapshot on failure.
func (c *ControlPlane) Reconcile(ctx context.Context) error {
	c.reconcileMu.Lock()
	defer c.reconcileMu.Unlock()
	started := time.Now()
	defer func() { compileDurationMetric.Observe(time.Since(started).Seconds()) }()
	baseline, inventory, blocked := c.inputs()
	if len(baseline) == 0 || len(inventory) == 0 {
		return errors.New("baseline and inventory are not loaded")
	}
	overrides, err := c.store.List(ctx)
	if err != nil {
		return err
	}
	now := c.now().UTC()
	verifiedFallback := c.verifiedFallbackProtection()
	current := c.manager.Current()
	generation := uint64(1)
	if current != nil {
		generation = current.Generation + 1
	}
	compileOverrides, invalidOverrides := c.compileableOverrides(overrides, baseline, inventory, blocked, generation, now)
	snapshot, _, err := CompileSnapshot(CompileInput{Baseline: baseline, Inventory: inventory, Blocked: blocked, Overrides: compileOverrides, Generation: generation, Now: now})
	if err != nil {
		return err
	}
	if current != nil && current.InputDigest == snapshot.InputDigest {
		snapshot = current
		if !c.manager.Ready() {
			if err := c.manager.Publish(snapshot); err != nil {
				return fmt.Errorf("verify cached snapshot: %w", err)
			}
		}
	} else if err := c.manager.Publish(snapshot); err != nil {
		return fmt.Errorf("publish snapshot: %w", err)
	}
	var statusErrors []error
	for i := range overrides {
		state := desiredOverrideState(&overrides[i], now)
		message := strings.ToLower(string(state))
		if invalidErr, invalid := invalidOverrides[overrides[i].Name]; invalid {
			state = dispatcherv1.OverrideStateFailed
			message = invalidErr.Error()
		}
		fallbackProtected := verifiedFallback.Has(overrides[i].Spec.Cluster)
		if err := c.updateStatus(ctx, &overrides[i], state, snapshot, fallbackProtected, message); err != nil {
			statusErrors = append(statusErrors, fmt.Errorf("update override %q status: %w", overrides[i].Name, err))
		}
	}
	updatedOverrides, listErr := c.store.List(ctx)
	if listErr == nil {
		observeOverrides(updatedOverrides)
	} else {
		statusErrors = append(statusErrors, listErr)
	}
	return errors.Join(statusErrors...)
}

func stableID(prefix string, value interface{}) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return prefix + "-" + hex.EncodeToString(sum[:8]), nil
}

func (c *ControlPlane) validateChannel(channel string) error {
	if channel != c.options.AllowedChannelID {
		return fmt.Errorf("dispatcher controls are restricted to configured Slack channel")
	}
	return nil
}

func (c *ControlPlane) validatePlanRequest(request PlanRequest) error {
	if err := c.validateChannel(request.ChannelID); err != nil {
		return err
	}
	if request.UserID == "" || strings.TrimSpace(request.Reason) == "" || request.IdempotencyKey == "" {
		return errors.New("user, reason, and idempotency key are required")
	}
	if request.Cluster == "" {
		return errors.New("cluster is required")
	}
	if request.DurationSeconds <= 0 || request.DurationSeconds > int64(c.options.MaxTTL/time.Second) {
		return fmt.Errorf("override TTL must be positive and no more than %s", c.options.MaxTTL)
	}
	duration := time.Duration(request.DurationSeconds) * time.Second
	if request.Kind == dispatcherv1.OverrideKindDrain && duration > c.options.MaxDrainTTL {
		return fmt.Errorf("drain TTL must be no more than %s", c.options.MaxDrainTTL)
	}
	if request.Kind != dispatcherv1.OverrideKindDrain && request.Kind != dispatcherv1.OverrideKindCapacity {
		return fmt.Errorf("unsupported override kind %q", request.Kind)
	}
	if request.Kind == dispatcherv1.OverrideKindCapacity && (request.Capacity == nil || *request.Capacity < 1 || *request.Capacity > 100) {
		return errors.New("capacity must be between 1 and 100")
	}
	if request.Kind == dispatcherv1.OverrideKindDrain && request.Capacity != nil {
		return errors.New("drain must not set capacity")
	}
	if len(request.Reason) > 500 || len(request.IncidentURL) > 2048 || len(request.Cluster) > 253 || len(request.Capability) > 253 {
		return errors.New("reason, incident URL, cluster, or capability exceeds its maximum length")
	}
	if request.IncidentURL != "" {
		parsed, err := url.ParseRequestURI(request.IncidentURL)
		if err != nil || parsed.Scheme != "https" && parsed.Scheme != "http" || parsed.Host == "" {
			return errors.New("incident URL must be an absolute HTTP or HTTPS URL")
		}
	}
	return nil
}

func (c *ControlPlane) validateStoredOverride(override *dispatcherv1.DispatchOverride, fallbackProtected bool) error {
	if override.Name == "" || override.Name != override.Spec.ID || !controlIDPattern.MatchString(override.Spec.ID) {
		return errors.New("override resource name and spec ID must be the same valid identifier")
	}
	if override.Spec.PlanID == "" || override.Spec.CreatedBy == "" || strings.TrimSpace(override.Spec.Reason) == "" || override.Spec.IdempotencyKey == "" {
		return errors.New("override plan, creator, reason, and idempotency key are required")
	}
	if override.Spec.SourceChannelID != c.options.AllowedChannelID {
		return errors.New("override source channel is not allowed")
	}
	if err := featureEnabled(c.options, PlanRequest{Kind: override.Spec.Kind, Capability: override.Spec.Scope.Capability}); err != nil {
		return err
	}
	if !override.Spec.ExpiresAt.Time.After(override.Spec.StartsAt.Time) || override.Spec.ExpiresAt.Sub(override.Spec.StartsAt.Time) > c.options.MaxTTL {
		return errors.New("override has an invalid or excessive TTL")
	}
	if override.Spec.Kind == dispatcherv1.OverrideKindDrain {
		if override.Spec.ExpiresAt.Sub(override.Spec.StartsAt.Time) > c.options.MaxDrainTTL || override.Spec.RequiredApprovals != 2 {
			return errors.New("drain exceeds its TTL or does not require two approvals")
		}
		if !fallbackProtected && !override.Spec.FallbackProtected && !override.Spec.FallbackConfirmed {
			return errors.New("unprotected drain lacks explicit fallback confirmation")
		}
	} else if override.Spec.RequiredApprovals < 1 || override.Spec.RequiredApprovals > 2 {
		return errors.New("override required approvals must be one or two")
	}
	return nil
}

func (c *ControlPlane) compileableOverrides(overrides []dispatcherv1.DispatchOverride, baseline map[string]ProwJobData, inventory ClusterMap, blocked sets.Set[string], generation uint64, now time.Time) ([]dispatcherv1.DispatchOverride, map[string]error) {
	result := make([]dispatcherv1.DispatchOverride, 0, len(overrides))
	invalid := make(map[string]error)
	verifiedFallback := c.verifiedFallbackProtection()
	for i := range overrides {
		live := overrides[i].Spec.RevokedAt == nil && now.Before(overrides[i].Spec.ExpiresAt.Time)
		if live {
			if err := c.validateStoredOverride(&overrides[i], verifiedFallback.Has(overrides[i].Spec.Cluster)); err != nil {
				invalid[overrides[i].Name] = err
				continue
			}
			if err := validateOverride(&overrides[i], baseline, inventory, blocked); err != nil {
				invalid[overrides[i].Name] = err
				continue
			}
		}
		trial := append(append([]dispatcherv1.DispatchOverride(nil), result...), overrides[i])
		if _, _, err := CompileSnapshot(CompileInput{Baseline: baseline, Inventory: inventory, Blocked: blocked, Overrides: trial, Generation: generation, Now: now}); err != nil {
			invalid[overrides[i].Name] = err
			continue
		}
		result = append(result, overrides[i])
	}
	return result, invalid
}

// Plan validates and previews an override without mutating durable state.
func (c *ControlPlane) Plan(ctx context.Context, request PlanRequest) (DispatchPlan, error) {
	if err := c.validatePlanRequest(request); err != nil {
		return DispatchPlan{}, err
	}
	if c.options.EnableCapacity {
		if err := c.verifyWriteSafety(); err != nil {
			return DispatchPlan{}, err
		}
	}
	snapshot := c.manager.Current()
	if snapshot == nil || !c.manager.Ready() {
		return DispatchPlan{}, errors.New("dispatcher has no ready policy generation")
	}
	now := c.now().UTC()
	overrides, err := c.store.List(ctx)
	if err != nil {
		return DispatchPlan{}, err
	}
	overrides, _ = c.compileableOverrides(overrides, snapshot.Baseline, snapshot.Inventory, sets.New[string](snapshot.Blocked...), snapshot.Generation+1, now)
	planID, err := stableID("plan", struct {
		Request    PlanRequest `json:"request"`
		Generation uint64      `json:"generation"`
	}{request, snapshot.Generation})
	if err != nil {
		return DispatchPlan{}, err
	}
	capacity := request.Capacity
	preview := NewOverride(dispatcherv1.DispatchOverrideSpec{
		ID: planID, PlanID: planID, SourceGeneration: snapshot.Generation, PolicyInputDigest: snapshot.InputDigest,
		Kind: request.Kind, Cluster: request.Cluster, Scope: dispatcherv1.DispatchOverrideScope{Capability: request.Capability}, Capacity: capacity,
		StartsAt: metav1.NewTime(now), ExpiresAt: metav1.NewTime(now.Add(time.Duration(request.DurationSeconds) * time.Second)),
		CreatedBy: request.UserID, SourceChannelID: request.ChannelID, Reason: request.Reason, IncidentURL: request.IncidentURL,
		Approvals: []dispatcherv1.DispatchApproval{{UserID: request.UserID, At: metav1.NewTime(now)}}, RequiredApprovals: 1,
		FallbackConfirmed: request.FallbackConfirmed, IdempotencyKey: request.IdempotencyKey,
	})
	preview.Name = planID
	overrides = append(overrides, preview)
	_, combinedImpact, err := CompileSnapshot(CompileInput{Baseline: snapshot.Baseline, Inventory: snapshot.Inventory, Blocked: sets.New[string](snapshot.Blocked...), Overrides: overrides, Generation: snapshot.Generation + 1, Now: now})
	if err != nil {
		return DispatchPlan{}, err
	}
	impact, exists := combinedImpact.ByOverride[planID]
	if !exists {
		return DispatchPlan{}, errors.New("planned override was not included in the compiled policy")
	}
	if impact.AffectedJobs == 0 {
		return DispatchPlan{}, errors.New("override has no affected workloads")
	}
	if impact.MovableJobs == 0 {
		return DispatchPlan{}, errors.New("override has no movable workloads or eligible destination")
	}
	if impact.MovedJobs == 0 {
		return DispatchPlan{}, errors.New("override would not change any assignment")
	}
	requiredApprovals := int32(1)
	if request.Kind == dispatcherv1.OverrideKindDrain || c.options.AffectedDemandApproval > 0 && impact.AffectedDemand >= c.options.AffectedDemandApproval {
		requiredApprovals = 2
	}
	fallbackProtected := c.verifiedFallbackProtection().Has(request.Cluster)
	plan := DispatchPlan{
		ID: planID, CreatedAt: now, ExpiresAt: now.Add(c.options.PlanTTL), SourceGeneration: snapshot.Generation,
		PolicyInputDigest: snapshot.InputDigest, Request: request, Impact: impact, RequiredApprovals: requiredApprovals,
		FallbackProtected: fallbackProtected, PropagationBound: c.options.SchedulerPropagationBound,
		CurrentEffectiveCapacity: snapshot.Inventory[request.Cluster].Capacity,
	}
	if request.Kind == dispatcherv1.OverrideKindDrain {
		plan.RequestedEffectiveCapacity = 0
	} else {
		plan.RequestedEffectiveCapacity = int(*request.Capacity)
	}
	c.mu.Lock()
	for id, existing := range c.plans {
		if !now.Before(existing.ExpiresAt) {
			delete(c.plans, id)
		}
	}
	if len(c.plans) >= 10000 {
		c.mu.Unlock()
		return DispatchPlan{}, errors.New("too many live plans; wait for existing plans to expire")
	}
	c.plans[planID] = plan
	c.mu.Unlock()
	return plan, nil
}

func (c *ControlPlane) getPlan(id string) (DispatchPlan, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	plan, exists := c.plans[id]
	return plan, exists
}

// GetPlan returns a live plan or an error matching apply for missing or expired IDs.
func (c *ControlPlane) GetPlan(id string) (DispatchPlan, error) {
	plan, exists := c.getPlan(id)
	if !exists || !c.now().Before(plan.ExpiresAt) {
		return DispatchPlan{}, errors.New("plan is missing or expired; create a new plan")
	}
	return plan, nil
}

// Explain returns the current scheduling decision for a job.
func (c *ControlPlane) Explain(job string) (Decision, error) {
	snapshot := c.manager.Current()
	if snapshot == nil || !c.manager.Ready() {
		return Decision{}, errors.New("dispatcher has no ready policy generation")
	}
	decision, found := c.manager.Lookup(job, c.now().UTC())
	if !found {
		return Decision{}, fmt.Errorf("unknown job %q", job)
	}
	return decision, nil
}

func featureEnabled(options ControlOptions, request PlanRequest) error {
	if request.Capability != "" && !options.EnableCapabilityScope {
		return errors.New("capability-scoped operations are disabled")
	}
	switch request.Kind {
	case dispatcherv1.OverrideKindCapacity:
		if !options.EnableCapacity {
			return errors.New("capacity operations are disabled")
		}
	case dispatcherv1.OverrideKindDrain:
		if !options.EnableDrain {
			return errors.New("drain operations are disabled")
		}
	}
	return nil
}

func (c *ControlPlane) verifyWriteSafety() error {
	if c.options.WriteSafetyCheck == nil {
		return errors.New("write safety verification is not configured")
	}
	if err := c.options.WriteSafetyCheck(); err != nil {
		return fmt.Errorf("write safety verification failed: %w", err)
	}
	return nil
}

// Apply creates or adds a distinct approval to the override represented by planID.
func (c *ControlPlane) Apply(ctx context.Context, planID string, request ApplyRequest) (*dispatcherv1.DispatchOverride, error) {
	if !controlIDPattern.MatchString(planID) {
		return nil, errors.New("invalid plan ID")
	}
	if err := c.validateChannel(request.ChannelID); err != nil {
		return nil, err
	}
	if request.UserID == "" || request.IdempotencyKey == "" {
		return nil, errors.New("user and idempotency key are required")
	}
	plan, exists := c.getPlan(planID)
	if !exists || !c.now().Before(plan.ExpiresAt) {
		return nil, errors.New("plan is missing or expired; create a new plan")
	}
	if err := featureEnabled(c.options, plan.Request); err != nil {
		return nil, err
	}
	if err := c.verifyWriteSafety(); err != nil {
		return nil, err
	}
	fallbackProtected := false
	if plan.Request.Kind == dispatcherv1.OverrideKindDrain {
		fallbackProtected = c.verifiedFallbackProtection().Has(plan.Request.Cluster)
		if !fallbackProtected && !request.FallbackConfirmed {
			return nil, errors.New("drain requires explicit confirmation that the Git fallback is not protected")
		}
	}
	overrideID, err := stableID("override", struct {
		Plan string `json:"plan"`
	}{plan.ID})
	if err != nil {
		return nil, err
	}
	now := c.now().UTC()
	planIsCurrent := func() bool {
		current := c.manager.Current()
		return current != nil && c.manager.Ready() && current.Generation == plan.SourceGeneration && current.InputDigest == plan.PolicyInputDigest
	}
	var override *dispatcherv1.DispatchOverride
	err = retry.OnError(retry.DefaultRetry, func(err error) bool {
		return apierrors.IsAlreadyExists(err) || apierrors.IsConflict(err)
	}, func() error {
		stored, err := c.store.Get(ctx, overrideID)
		if apierrors.IsNotFound(err) {
			if !planIsCurrent() {
				return errors.New("plan is stale; create a new plan")
			}
			overrideValue := NewOverride(dispatcherv1.DispatchOverrideSpec{
				ID: overrideID, PlanID: plan.ID, SourceGeneration: plan.SourceGeneration, PolicyInputDigest: plan.PolicyInputDigest,
				Kind: plan.Request.Kind, Cluster: plan.Request.Cluster, Scope: dispatcherv1.DispatchOverrideScope{Capability: plan.Request.Capability}, Capacity: plan.Request.Capacity,
				StartsAt: metav1.NewTime(now), ExpiresAt: metav1.NewTime(now.Add(time.Duration(plan.Request.DurationSeconds) * time.Second)),
				CreatedBy: plan.Request.UserID, SourceChannelID: plan.Request.ChannelID, Reason: plan.Request.Reason, IncidentURL: plan.Request.IncidentURL,
				Approvals: []dispatcherv1.DispatchApproval{{UserID: request.UserID, At: metav1.NewTime(now)}}, RequiredApprovals: plan.RequiredApprovals,
				FallbackProtected: fallbackProtected, FallbackConfirmed: request.FallbackConfirmed, SlackThreadTS: request.SlackThreadTS,
				IdempotencyKey: request.IdempotencyKey,
			})
			override = &overrideValue
			return c.store.Create(ctx, override)
		}
		if err != nil {
			return err
		}
		override = stored
		if override.Spec.PlanID != plan.ID {
			return errors.New("override ID collision")
		}
		for _, approval := range override.Spec.Approvals {
			if approval.UserID == request.UserID {
				return nil
			}
		}
		if !planIsCurrent() {
			return errors.New("plan is stale; create a new plan")
		}
		override.Spec.Approvals = append(override.Spec.Approvals, dispatcherv1.DispatchApproval{UserID: request.UserID, At: metav1.NewTime(now)})
		if request.FallbackConfirmed {
			override.Spec.FallbackConfirmed = true
		}
		if fallbackProtected {
			override.Spec.FallbackProtected = true
		}
		return c.store.Update(ctx, override)
	})
	if err != nil {
		return nil, err
	}
	c.trigger()
	if err := c.Reconcile(ctx); err != nil {
		logrus.WithError(err).WithField("override_id", overrideID).Error("failed to reconcile dispatcher policy after storing override; reconciliation will retry")
		return c.store.Get(ctx, overrideID)
	}
	return c.store.Get(ctx, overrideID)
}

// Cancel durably revokes an override. Repeated cancellation is successful.
func (c *ControlPlane) Cancel(ctx context.Context, overrideID string, request CancelRequest) (*dispatcherv1.DispatchOverride, error) {
	if !controlIDPattern.MatchString(overrideID) {
		return nil, errors.New("invalid override ID")
	}
	if err := c.validateChannel(request.ChannelID); err != nil {
		return nil, err
	}
	if request.UserID == "" || request.IdempotencyKey == "" {
		return nil, errors.New("user and idempotency key are required")
	}
	override, err := c.store.Get(ctx, overrideID)
	if err != nil {
		return nil, err
	}
	if override.Spec.SourceChannelID != c.options.AllowedChannelID {
		return nil, errors.New("override originated outside the configured channel")
	}
	if override.Spec.RevokedAt == nil {
		if err := featureEnabled(c.options, PlanRequest{Kind: override.Spec.Kind, Capability: override.Spec.Scope.Capability}); err != nil {
			return nil, err
		}
		if err := c.verifyWriteSafety(); err != nil {
			return nil, err
		}
		now := metav1.NewTime(c.now().UTC())
		override.Spec.RevokedAt = &now
		override.Spec.RevokedBy = request.UserID
		if err := c.store.Update(ctx, override); err != nil {
			return nil, err
		}
	}
	c.trigger()
	if err := c.Reconcile(ctx); err != nil {
		logrus.WithError(err).WithField("override_id", overrideID).Error("failed to reconcile dispatcher policy after revoking override; reconciliation will retry")
		return c.store.Get(ctx, overrideID)
	}
	return c.store.Get(ctx, overrideID)
}

// BindThread idempotently associates an override with a notification thread in the allowed channel.
func (c *ControlPlane) BindThread(ctx context.Context, overrideID string, request BindThreadRequest) (*dispatcherv1.DispatchOverride, error) {
	if !controlIDPattern.MatchString(overrideID) {
		return nil, errors.New("invalid override ID")
	}
	if err := c.validateChannel(request.ChannelID); err != nil {
		return nil, err
	}
	if request.UserID == "" || request.ThreadTS == "" {
		return nil, errors.New("user and thread timestamp are required")
	}
	override, err := c.store.Get(ctx, overrideID)
	if err != nil {
		return nil, err
	}
	if override.Spec.SourceChannelID != request.ChannelID {
		return nil, errors.New("callback channel does not match the initiating channel")
	}
	if override.Spec.SlackThreadTS != "" && override.Spec.SlackThreadTS != request.ThreadTS {
		return nil, errors.New("override is already bound to another Slack thread")
	}
	if override.Spec.SlackThreadTS == "" {
		override.Spec.SlackThreadTS = request.ThreadTS
		if err := c.store.Update(ctx, override); err != nil {
			return nil, err
		}
	}
	return override, nil
}

// Overrides returns every durable override.
func (c *ControlPlane) Overrides(ctx context.Context) ([]dispatcherv1.DispatchOverride, error) {
	return c.store.List(ctx)
}

// Status returns current generation and optional cluster details.
func (c *ControlPlane) Status(ctx context.Context, cluster string) (ControlStatus, error) {
	snapshot := c.manager.Current()
	if snapshot == nil || !c.manager.Ready() {
		return ControlStatus{}, errors.New("dispatcher has no ready policy generation")
	}
	status := ControlStatus{Ready: true, Generation: snapshot.Generation, PolicyInputDigest: snapshot.InputDigest, SnapshotChecksum: snapshot.Checksum, GeneratedAt: snapshot.GeneratedAt, Cluster: cluster}
	if cluster != "" {
		info, exists := snapshot.Inventory[cluster]
		if !exists {
			return ControlStatus{}, fmt.Errorf("unknown or inactive cluster %q", cluster)
		}
		status.ClusterInfo = &info
		effectiveCapacity := info.Capacity
		status.EffectiveCapacity = &effectiveCapacity
		status.FallbackProtected = c.verifiedFallbackProtection().Has(cluster)
	}
	overrides, err := c.store.List(ctx)
	if err != nil {
		return ControlStatus{}, err
	}
	activeOverrideIDs := sets.New[string](snapshot.OverrideIDs...)
	for i := range overrides {
		if cluster == "" || overrides[i].Spec.Cluster == cluster {
			status.Overrides = append(status.Overrides, overrides[i])
		}
		if cluster != "" && overrides[i].Spec.Cluster == cluster && activeOverrideIDs.Has(overrides[i].Spec.ID) && OverrideIsActive(&overrides[i], c.now()) && overrides[i].Spec.Scope.Capability == "" {
			if overrides[i].Spec.Kind == dispatcherv1.OverrideKindDrain {
				value := 0
				status.EffectiveCapacity = &value
			} else if overrides[i].Spec.Capacity != nil {
				value := int(*overrides[i].Spec.Capacity)
				status.EffectiveCapacity = &value
			}
		}
	}
	return status, nil
}

// ControlServer exposes the authenticated dispatcher operator API.
type ControlServer struct {
	control *ControlPlane
	token   func() []byte
}

// NewControlServer creates an authenticated control API handler.
func NewControlServer(control *ControlPlane, token func() []byte) *ControlServer {
	return &ControlServer{control: control, token: token}
}

func (s *ControlServer) authorized(request *http.Request) bool {
	return bearerTokenAuthorized(request, s.token)
}

func bearerTokenAuthorized(request *http.Request, token func() []byte) bool {
	if token == nil || !strings.HasPrefix(request.Header.Get("Authorization"), "Bearer ") {
		return false
	}
	provided := strings.TrimPrefix(request.Header.Get("Authorization"), "Bearer ")
	expected := string(token())
	return expected != "" && len(provided) == len(expected) && subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) == 1
}

func writeJSON(writer http.ResponseWriter, status int, value interface{}) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

func writeControlError(writer http.ResponseWriter, status int, err error) {
	writeJSON(writer, status, map[string]string{"error": err.Error()})
}

// ServeHTTP routes control API requests after bearer-token authentication.
func (s *ControlServer) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if !s.authorized(request) {
		writeControlError(writer, http.StatusUnauthorized, errors.New("unauthorized"))
		return
	}
	request.Body = http.MaxBytesReader(writer, request.Body, 64<<10)
	path := strings.TrimPrefix(request.URL.Path, "/control/v1")
	switch {
	case request.Method == http.MethodGet && path == "/status":
		status, err := s.control.Status(request.Context(), request.URL.Query().Get("cluster"))
		if err != nil {
			writeControlError(writer, http.StatusBadRequest, err)
			return
		}
		writeJSON(writer, http.StatusOK, status)
	case request.Method == http.MethodGet && path == "/overrides":
		overrides, err := s.control.Overrides(request.Context())
		if err != nil {
			writeControlError(writer, http.StatusInternalServerError, err)
			return
		}
		writeJSON(writer, http.StatusOK, overrides)
	case request.Method == http.MethodPost && path == "/plans":
		var planRequest PlanRequest
		if err := json.NewDecoder(request.Body).Decode(&planRequest); err != nil {
			writeControlError(writer, http.StatusBadRequest, err)
			return
		}
		plan, err := s.control.Plan(request.Context(), planRequest)
		if err != nil {
			writeControlError(writer, http.StatusConflict, err)
			return
		}
		writeJSON(writer, http.StatusOK, plan)
	case request.Method == http.MethodGet && strings.HasPrefix(path, "/plans/"):
		planID := strings.TrimPrefix(path, "/plans/")
		if planID == "" || strings.Contains(planID, "/") {
			writeControlError(writer, http.StatusNotFound, errors.New("not found"))
			return
		}
		plan, err := s.control.GetPlan(planID)
		if err != nil {
			writeControlError(writer, http.StatusNotFound, err)
			return
		}
		writeJSON(writer, http.StatusOK, plan)
	case request.Method == http.MethodGet && path == "/jobs":
		job := request.URL.Query().Get("name")
		if job == "" {
			writeControlError(writer, http.StatusBadRequest, errors.New("job name is required"))
			return
		}
		decision, err := s.control.Explain(job)
		if err != nil {
			status := http.StatusBadRequest
			if strings.Contains(err.Error(), "unknown job") {
				status = http.StatusNotFound
			}
			writeControlError(writer, status, err)
			return
		}
		writeJSON(writer, http.StatusOK, decision)
	case request.Method == http.MethodPost && strings.HasPrefix(path, "/plans/") && strings.HasSuffix(path, "/apply"):
		planID := strings.TrimSuffix(strings.TrimPrefix(path, "/plans/"), "/apply")
		var applyRequest ApplyRequest
		if err := json.NewDecoder(request.Body).Decode(&applyRequest); err != nil {
			writeControlError(writer, http.StatusBadRequest, err)
			return
		}
		override, err := s.control.Apply(request.Context(), planID, applyRequest)
		if err != nil {
			writeControlError(writer, http.StatusConflict, err)
			return
		}
		writeJSON(writer, http.StatusOK, override)
	case request.Method == http.MethodPost && strings.HasPrefix(path, "/overrides/") && strings.HasSuffix(path, "/cancel"):
		overrideID := strings.TrimSuffix(strings.TrimPrefix(path, "/overrides/"), "/cancel")
		var cancelRequest CancelRequest
		if err := json.NewDecoder(request.Body).Decode(&cancelRequest); err != nil {
			writeControlError(writer, http.StatusBadRequest, err)
			return
		}
		override, err := s.control.Cancel(request.Context(), overrideID, cancelRequest)
		if err != nil {
			status := http.StatusConflict
			if apierrors.IsNotFound(err) {
				status = http.StatusNotFound
			}
			writeControlError(writer, status, err)
			return
		}
		writeJSON(writer, http.StatusOK, override)
	case request.Method == http.MethodPost && strings.HasPrefix(path, "/overrides/") && strings.HasSuffix(path, "/thread"):
		overrideID := strings.TrimSuffix(strings.TrimPrefix(path, "/overrides/"), "/thread")
		var bindRequest BindThreadRequest
		if err := json.NewDecoder(request.Body).Decode(&bindRequest); err != nil {
			writeControlError(writer, http.StatusBadRequest, err)
			return
		}
		override, err := s.control.BindThread(request.Context(), overrideID, bindRequest)
		if err != nil {
			writeControlError(writer, http.StatusConflict, err)
			return
		}
		writeJSON(writer, http.StatusOK, override)
	default:
		writeControlError(writer, http.StatusNotFound, errors.New("not found"))
	}
}

// FallbackObservationServer accepts narrowly authenticated observations from
// the process that inspects the fallback assignments loaded by Prow.
type FallbackObservationServer struct {
	control *ControlPlane
	token   func() []byte
}

// NewFallbackObservationServer creates the fallback observation API handler.
func NewFallbackObservationServer(control *ControlPlane, token func() []byte) *FallbackObservationServer {
	return &FallbackObservationServer{control: control, token: token}
}

// ServeHTTP validates and installs one leased fallback-protection observation.
func (s *FallbackObservationServer) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if !bearerTokenAuthorized(request, s.token) {
		writeControlError(writer, http.StatusUnauthorized, errors.New("unauthorized"))
		return
	}
	if request.Method != http.MethodPost || request.URL.Path != "/fallback-observer/v1/protection" {
		writeControlError(writer, http.StatusNotFound, errors.New("not found"))
		return
	}
	request.Body = http.MaxBytesReader(writer, request.Body, 64<<10)
	var observation FallbackProtectionObservation
	if err := json.NewDecoder(request.Body).Decode(&observation); err != nil {
		writeControlError(writer, http.StatusBadRequest, err)
		return
	}
	if err := s.control.ObserveFallbackProtection(observation); err != nil {
		writeControlError(writer, http.StatusConflict, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]string{"status": "observed"})
}

// ControlClient is used by the DPTP bot to call the dispatcher control API.
type ControlClient struct {
	baseURL string
	token   func() []byte
	client  *http.Client
}

// NewControlClient creates a dispatcher control API client with bounded requests.
func NewControlClient(baseURL string, token func() []byte) *ControlClient {
	return &ControlClient{baseURL: strings.TrimSuffix(baseURL, "/"), token: token, client: &http.Client{Timeout: 10 * time.Second}}
}

func (c *ControlClient) request(ctx context.Context, method, path string, input, output interface{}) error {
	if c.token == nil {
		return errors.New("dispatcher control token source is nil")
	}
	var body *strings.Reader
	if input != nil {
		data, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = strings.NewReader(string(data))
	} else {
		body = strings.NewReader("")
	}
	request, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+string(c.token()))
	request.Header.Set("Content-Type", "application/json")
	response, err := c.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		var apiError map[string]string
		_ = json.NewDecoder(response.Body).Decode(&apiError)
		return fmt.Errorf("dispatcher control API returned %s: %s", response.Status, apiError["error"])
	}
	if output == nil {
		return nil
	}
	return json.NewDecoder(response.Body).Decode(output)
}

// Status returns dispatcher control status.
func (c *ControlClient) Status(ctx context.Context, cluster string) (ControlStatus, error) {
	var status ControlStatus
	path := "/control/v1/status"
	if cluster != "" {
		path += "?cluster=" + url.QueryEscape(cluster)
	}
	err := c.request(ctx, http.MethodGet, path, nil, &status)
	return status, err
}

// Overrides returns durable runtime overrides.
func (c *ControlClient) Overrides(ctx context.Context) ([]dispatcherv1.DispatchOverride, error) {
	var overrides []dispatcherv1.DispatchOverride
	err := c.request(ctx, http.MethodGet, "/control/v1/overrides", nil, &overrides)
	return overrides, err
}

// Plan previews a runtime override.
func (c *ControlClient) Plan(ctx context.Context, request PlanRequest) (DispatchPlan, error) {
	var plan DispatchPlan
	err := c.request(ctx, http.MethodPost, "/control/v1/plans", request, &plan)
	return plan, err
}

// GetPlan returns a previously created plan.
func (c *ControlClient) GetPlan(ctx context.Context, id string) (DispatchPlan, error) {
	var plan DispatchPlan
	err := c.request(ctx, http.MethodGet, "/control/v1/plans/"+id, nil, &plan)
	return plan, err
}

// Explain returns the current scheduling decision for a job.
func (c *ControlClient) Explain(ctx context.Context, job string) (Decision, error) {
	var decision Decision
	err := c.request(ctx, http.MethodGet, "/control/v1/jobs?name="+url.QueryEscape(job), nil, &decision)
	return decision, err
}

// Apply applies or approves a plan.
func (c *ControlClient) Apply(ctx context.Context, planID string, request ApplyRequest) (*dispatcherv1.DispatchOverride, error) {
	var override dispatcherv1.DispatchOverride
	err := c.request(ctx, http.MethodPost, "/control/v1/plans/"+planID+"/apply", request, &override)
	return &override, err
}

// Cancel revokes an override.
func (c *ControlClient) Cancel(ctx context.Context, overrideID string, request CancelRequest) (*dispatcherv1.DispatchOverride, error) {
	var override dispatcherv1.DispatchOverride
	err := c.request(ctx, http.MethodPost, "/control/v1/overrides/"+overrideID+"/cancel", request, &override)
	return &override, err
}

// BindThread records the Slack notification thread for an override.
func (c *ControlClient) BindThread(ctx context.Context, overrideID string, request BindThreadRequest) (*dispatcherv1.DispatchOverride, error) {
	var override dispatcherv1.DispatchOverride
	err := c.request(ctx, http.MethodPost, "/control/v1/overrides/"+overrideID+"/thread", request, &override)
	return &override, err
}

// FormatDurationSeconds converts an API duration field to a human-readable value.
func FormatDurationSeconds(seconds int64) string {
	return (time.Duration(seconds) * time.Second).String()
}

// ParsePositiveDuration parses a bounded positive duration for command adapters.
func ParsePositiveDuration(value string) (int64, error) {
	duration, err := time.ParseDuration(value)
	if err != nil || duration < time.Second {
		return 0, fmt.Errorf("invalid positive duration %q", value)
	}
	return int64(duration / time.Second), nil
}

// ParseCapacity parses a 1-100 capacity value.
func ParseCapacity(value string) (*int32, error) {
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 1 || parsed > 100 {
		return nil, fmt.Errorf("capacity %q must be an integer from 1 to 100", value)
	}
	result := int32(parsed)
	return &result, nil
}

// SortOverrides sorts overrides by stable identifier for presentation.
func SortOverrides(overrides []dispatcherv1.DispatchOverride) {
	sort.Slice(overrides, func(i, j int) bool { return overrides[i].Spec.ID < overrides[j].Spec.ID })
}
