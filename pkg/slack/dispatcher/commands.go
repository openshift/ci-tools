package dispatcher

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/slack-go/slack"

	dispatcherv1 "github.com/openshift/ci-tools/pkg/api/dispatcher/v1"
	coredispatcher "github.com/openshift/ci-tools/pkg/dispatcher"
)

// ControlClient is the dispatcher API surface needed by the DPTP command adapter.
type ControlClient interface {
	Status(context.Context, string) (coredispatcher.ControlStatus, error)
	Overrides(context.Context) ([]dispatcherv1.DispatchOverride, error)
	GetPlan(context.Context, string) (coredispatcher.DispatchPlan, error)
	Explain(context.Context, string) (coredispatcher.Decision, error)
	Plan(context.Context, coredispatcher.PlanRequest) (coredispatcher.DispatchPlan, error)
	Apply(context.Context, string, coredispatcher.ApplyRequest) (*dispatcherv1.DispatchOverride, error)
	Cancel(context.Context, string, coredispatcher.CancelRequest) (*dispatcherv1.DispatchOverride, error)
}

// Messenger posts lifecycle messages to the configured Slack channel and thread.
type Messenger interface {
	PostMessage(channelID string, options ...slack.MsgOption) (string, string, error)
}

// Options configures the immutable Slack command boundary.
type Options struct {
	ChannelID string
	Timeout   time.Duration
	// EnableApply gates all mutating commands while status, overrides, and plan remain available.
	EnableApply           bool
	EnableCapacity        bool
	EnableDrain           bool
	EnableCapabilityScope bool
	Messenger             Messenger
	PollInterval          time.Duration
	OnDenial              func(Command)
}

// Command is a dispatcher operation derived from a signed Slack event.
type Command struct {
	TeamID    string
	ChannelID string
	UserID    string
	Text      string
	RequestID string
	ThreadTS  string
}

// Handler parses and executes tp-dispatch mention commands.
type Handler struct {
	client   ControlClient
	options  Options
	mu       sync.Mutex
	observed map[string]string
	requests map[string]time.Time
}

// NewHandler creates a channel-gated dispatcher command handler.
func NewHandler(client ControlClient, options Options) (*Handler, error) {
	if client == nil {
		return nil, errors.New("dispatcher control client is required")
	}
	if options.ChannelID == "" {
		return nil, errors.New("dispatcher command channel ID is required")
	}
	if options.Timeout == 0 {
		options.Timeout = 10 * time.Second
	}
	if options.PollInterval == 0 {
		options.PollInterval = 5 * time.Second
	}
	return &Handler{client: client, options: options, observed: make(map[string]string), requests: make(map[string]time.Time)}, nil
}

func (h *Handler) reserveRequest(requestID string, now time.Time) bool {
	if requestID == "" {
		return true
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for id, processedAt := range h.requests {
		if now.Sub(processedAt) > 24*time.Hour {
			delete(h.requests, id)
		}
	}
	if _, exists := h.requests[requestID]; exists {
		return false
	}
	h.requests[requestID] = now
	return true
}

func (h *Handler) releaseRequest(requestID string) {
	if requestID == "" {
		return
	}
	h.mu.Lock()
	delete(h.requests, requestID)
	h.mu.Unlock()
}

// Response is the text posted back to the originating Slack thread.
type Response struct {
	Text     string `json:"text"`
	override *dispatcherv1.DispatchOverride
}

func response(text string) Response { return Response{Text: text} }

func idempotencyKey(command Command, action string) string {
	if command.RequestID != "" {
		return action + ":" + command.RequestID
	}
	sum := sha256.Sum256([]byte(strings.Join([]string{command.TeamID, command.ChannelID, command.UserID, command.ThreadTS, command.Text, action}, "\x00")))
	return action + ":" + hex.EncodeToString(sum[:8])
}

func parseOptions(tokens []string, positionalCount int, allowedOptions ...string) ([]string, map[string]string, error) {
	positionals := make([]string, 0, positionalCount)
	options := make(map[string]string)
	allowed := make(map[string]struct{}, len(allowedOptions))
	for _, option := range allowedOptions {
		allowed[option] = struct{}{}
	}
	for i := 0; i < len(tokens); i++ {
		if !strings.HasPrefix(tokens[i], "--") {
			positionals = append(positionals, tokens[i])
			continue
		}
		name := strings.TrimPrefix(tokens[i], "--")
		if _, exists := allowed[name]; !exists {
			return nil, nil, fmt.Errorf("unknown option --%s", name)
		}
		if _, exists := options[name]; exists {
			return nil, nil, fmt.Errorf("option --%s was specified more than once", name)
		}
		if i+1 >= len(tokens) || strings.HasPrefix(tokens[i+1], "--") {
			return nil, nil, fmt.Errorf("--%s requires a value", name)
		}
		i++
		if name == "reason" {
			values := []string{tokens[i]}
			for i+1 < len(tokens) && !strings.HasPrefix(tokens[i+1], "--") {
				i++
				values = append(values, tokens[i])
			}
			options[name] = strings.Join(values, " ")
			continue
		}
		options[name] = tokens[i]
	}
	if len(positionals) != positionalCount {
		return nil, nil, fmt.Errorf("expected %d positional arguments, got %d", positionalCount, len(positionals))
	}
	return positionals, options, nil
}

func formatOverrides(overrides []dispatcherv1.DispatchOverride) string {
	if len(overrides) == 0 {
		return "No dispatcher overrides exist."
	}
	sort.Slice(overrides, func(i, j int) bool { return overrides[i].Spec.ID < overrides[j].Spec.ID })
	lines := []string{"Dispatcher overrides:"}
	for i := range overrides {
		scope := overrides[i].Spec.Cluster
		if overrides[i].Spec.Scope.Capability != "" {
			scope += "/capability:" + overrides[i].Spec.Scope.Capability
		}
		detail := fmt.Sprintf("reason %s; created by %s; approvals %d/%d; generation %d",
			overrides[i].Spec.Reason, overrides[i].Spec.CreatedBy, len(overrides[i].Spec.Approvals), overrides[i].Spec.RequiredApprovals,
			overrides[i].Status.PolicyGeneration)
		if overrides[i].Spec.IncidentURL != "" {
			detail += "; incident " + overrides[i].Spec.IncidentURL
		}
		lines = append(lines, fmt.Sprintf("• `%s` %s %s — %s, expires %s\n  %s", overrides[i].Spec.ID, overrides[i].Spec.Kind, scope, overrides[i].Status.State, overrides[i].Spec.ExpiresAt.Time.UTC().Format(time.RFC3339), detail))
	}
	return strings.Join(lines, "\n")
}

func formatStatus(status coredispatcher.ControlStatus) string {
	text := fmt.Sprintf("Dispatcher generation `%d` is ready (policy `%s`).", status.Generation, short(status.PolicyInputDigest))
	if status.Cluster != "" && status.ClusterInfo != nil {
		effective := status.ClusterInfo.Capacity
		if status.EffectiveCapacity != nil {
			effective = *status.EffectiveCapacity
		}
		text += fmt.Sprintf("\n`%s`: provider %s, baseline/effective capacity %d/%d, capabilities %s.", status.Cluster, status.ClusterInfo.Provider, status.ClusterInfo.Capacity, effective, strings.Join(status.ClusterInfo.Capabilities, ", "))
	}
	if len(status.Overrides) > 0 {
		text += "\n" + formatOverrides(status.Overrides)
	}
	return text
}

func short(value string) string {
	if len(value) <= 12 {
		return value
	}
	return value[:12]
}

func formatPlan(plan coredispatcher.DispatchPlan) string {
	scope := plan.Request.Cluster
	if plan.Request.Capability != "" {
		scope += "/capability:" + plan.Request.Capability
	}
	destinations := make([]string, 0, len(plan.Impact.Destinations))
	for cluster, demand := range plan.Impact.Destinations {
		destinations = append(destinations, fmt.Sprintf("%s=%.1f", cluster, demand))
	}
	sort.Strings(destinations)
	propagation := "not yet measured (shadow mode)"
	if plan.PropagationBound > 0 {
		propagation = plan.PropagationBound.String()
	}
	return fmt.Sprintf("Plan `%s`: %s %s for %s; effective capacity %d → %d.\nAffected: %d jobs in %d placement groups / %.1f demand; movable: %d / %.1f; immovable: %d.\nDestinations: %s.\nApprovals required: %d. Maximum propagation: %s.\nApply with `@dptp-bot tp-dispatch apply %s`.",
		plan.ID, plan.Request.Kind, scope, coredispatcher.FormatDurationSeconds(plan.Request.DurationSeconds),
		plan.CurrentEffectiveCapacity, plan.RequestedEffectiveCapacity,
		plan.Impact.AffectedJobs, plan.Impact.AffectedGroups, plan.Impact.AffectedDemand, plan.Impact.MovableJobs, plan.Impact.MovableDemand, len(plan.Impact.ImmovableJobs),
		strings.Join(destinations, ", "), plan.RequiredApprovals, propagation,
		plan.ID)
}

func formatHistory(override dispatcherv1.DispatchOverride) string {
	if len(override.Status.History) == 0 {
		return fmt.Sprintf("Override `%s` has no recorded history.", override.Spec.ID)
	}
	lines := []string{fmt.Sprintf("History for override `%s`:", override.Spec.ID)}
	for _, event := range override.Status.History {
		lines = append(lines, fmt.Sprintf("• %s %s by %s — %s (generation %d)", event.At.Time.UTC().Format(time.RFC3339), event.State, event.Actor, event.Message, event.Generation))
	}
	return strings.Join(lines, "\n")
}

func formatDecision(job string, decision coredispatcher.Decision) string {
	text := fmt.Sprintf("Job `%s` is on `%s` (%s), generation `%d`.", job, decision.Cluster, decision.Source, decision.PolicyGeneration)
	if decision.OverrideID != "" {
		text += fmt.Sprintf(" Override `%s`.", decision.OverrideID)
	}
	if !decision.ValidUntil.IsZero() {
		text += fmt.Sprintf(" Valid until %s.", decision.ValidUntil.UTC().Format(time.RFC3339))
	}
	if decision.Explanation != "" {
		text += " " + decision.Explanation
	}
	return text
}

func (h *Handler) disabledPlanKind(plan coredispatcher.DispatchPlan) string {
	if plan.Request.Capability != "" && !h.options.EnableCapabilityScope {
		return "capability-scoped operations are disabled"
	}
	if plan.Request.Kind == dispatcherv1.OverrideKindCapacity && !h.options.EnableCapacity {
		return "capacity operations are disabled"
	}
	if plan.Request.Kind == dispatcherv1.OverrideKindDrain && !h.options.EnableDrain {
		return "drain operations are disabled"
	}
	return ""
}

func usage() string {
	return "Usage: `@dptp-bot tp-dispatch status [cluster]`, `overrides`, `history OVERRIDE_ID`, `explain JOB`, `plan show PLAN_ID`, `plan capacity CLUSTER VALUE --for DURATION --reason REASON`, `plan drain CLUSTER --for DURATION --reason REASON`, `apply PLAN_ID`, `approve PLAN_ID`, or `cancel OVERRIDE_ID`. Add `--capability NAME` to scope a plan."
}

// DeniedResponse records and formats a channel-boundary denial without calling the dispatcher.
func (h *Handler) DeniedResponse(command Command) Response {
	if h.options.OnDenial != nil {
		h.options.OnDenial(command)
	}
	return response(fmt.Sprintf("`@dptp-bot tp-dispatch` is available only in <#%s>.", h.options.ChannelID))
}

func transitionKey(override *dispatcherv1.DispatchOverride) string {
	// Policy generation is stamped on every reconcile, including terminal overrides.
	// Notify only when lifecycle state changes.
	return string(override.Status.State)
}

func transitionText(override *dispatcherv1.DispatchOverride) string {
	return fmt.Sprintf("Dispatcher override `%s` transitioned to *%s* at policy generation `%d`.", override.Spec.ID, override.Status.State, override.Status.PolicyGeneration)
}

func (h *Handler) markObserved(override *dispatcherv1.DispatchOverride) {
	h.mu.Lock()
	h.observed[override.Spec.ID] = transitionKey(override)
	h.mu.Unlock()
}

func (h *Handler) postTransition(override *dispatcherv1.DispatchOverride) {
	if h.options.Messenger == nil || override.Spec.SourceChannelID != h.options.ChannelID || override.Spec.SlackThreadTS == "" {
		return
	}
	if _, _, err := h.options.Messenger.PostMessage(h.options.ChannelID, slack.MsgOptionText(transitionText(override), false), slack.MsgOptionTS(override.Spec.SlackThreadTS)); err != nil {
		logrus.WithError(err).WithFields(logrus.Fields{"override_id": override.Spec.ID, "channel_id": h.options.ChannelID}).Error("failed to post dispatcher override transition to Slack")
		return
	}
	h.markObserved(override)
}

// Run posts asynchronous Pending/Active/Expired/Revoked/Failed transitions into
// each override's originating thread in the configured Slack channel.
func (h *Handler) Run(ctx context.Context) {
	if h.options.Messenger == nil {
		return
	}
	ticker := time.NewTicker(h.options.PollInterval)
	defer ticker.Stop()
	initialized := false
	for {
		if err := h.pollTransitions(ctx, initialized); err != nil {
			logrus.WithError(err).Warn("failed to list dispatcher overrides for Slack transition polling")
		} else {
			initialized = true
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (h *Handler) pollTransitions(ctx context.Context, initialized bool) error {
	overrides, err := h.client.Overrides(ctx)
	if err != nil {
		return err
	}
	for i := range overrides {
		if overrides[i].Spec.SourceChannelID != h.options.ChannelID || overrides[i].Spec.SlackThreadTS == "" {
			continue
		}
		key := transitionKey(&overrides[i])
		h.mu.Lock()
		previous, seen := h.observed[overrides[i].Spec.ID]
		h.mu.Unlock()
		if !initialized {
			h.markObserved(&overrides[i])
			continue
		}
		if !seen || previous != key {
			h.postTransition(&overrides[i])
		}
	}
	return nil
}

// Handle enforces the channel boundary before any dispatcher API call.
func (h *Handler) Handle(parent context.Context, command Command) Response {
	if command.ChannelID != h.options.ChannelID {
		return h.DeniedResponse(command)
	}
	ctx, cancel := context.WithTimeout(parent, h.options.Timeout)
	defer cancel()
	tokens := strings.Fields(command.Text)
	if len(tokens) == 0 {
		return response(usage())
	}

	var text string
	var err error
	var resultingOverride *dispatcherv1.DispatchOverride
	switch tokens[0] {
	case "status":
		if len(tokens) > 2 {
			return response(usage())
		}
		cluster := ""
		if len(tokens) == 2 {
			cluster = tokens[1]
		}
		var status coredispatcher.ControlStatus
		status, err = h.client.Status(ctx, cluster)
		if err == nil {
			text = formatStatus(status)
		}
	case "overrides":
		if len(tokens) != 1 {
			return response(usage())
		}
		var overrides []dispatcherv1.DispatchOverride
		overrides, err = h.client.Overrides(ctx)
		if err == nil {
			text = formatOverrides(overrides)
		}
	case "history":
		positionals, _, parseErr := parseOptions(tokens[1:], 1)
		if parseErr != nil {
			return response(parseErr.Error() + ". " + usage())
		}
		var overrides []dispatcherv1.DispatchOverride
		overrides, err = h.client.Overrides(ctx)
		if err == nil {
			found := false
			for i := range overrides {
				if overrides[i].Spec.ID == positionals[0] {
					text = formatHistory(overrides[i])
					found = true
					break
				}
			}
			if !found {
				return response(fmt.Sprintf("Override `%s` was not found.", positionals[0]))
			}
		}
	case "explain":
		positionals, _, parseErr := parseOptions(tokens[1:], 1)
		if parseErr != nil {
			return response(parseErr.Error() + ". " + usage())
		}
		var decision coredispatcher.Decision
		decision, err = h.client.Explain(ctx, positionals[0])
		if err == nil {
			text = formatDecision(positionals[0], decision)
		}
	case "plan":
		if len(tokens) < 2 {
			return response(usage())
		}
		if tokens[1] == "show" {
			positionals, _, parseErr := parseOptions(tokens[2:], 1)
			if parseErr != nil {
				return response(parseErr.Error() + ". " + usage())
			}
			var shown coredispatcher.DispatchPlan
			shown, err = h.client.GetPlan(ctx, positionals[0])
			if err == nil {
				text = formatPlan(shown)
			}
			break
		}
		kind := dispatcherv1.OverrideKind(strings.ToUpper(tokens[1][:1]) + strings.ToLower(tokens[1][1:]))
		positionalCount := 1
		if kind == dispatcherv1.OverrideKindCapacity {
			positionalCount = 2
		}
		positionals, options, parseErr := parseOptions(tokens[2:], positionalCount, "for", "reason", "incident", "capability")
		if parseErr != nil {
			return response(parseErr.Error() + ". " + usage())
		}
		duration, parseErr := coredispatcher.ParsePositiveDuration(options["for"])
		if parseErr != nil {
			return response(parseErr.Error())
		}
		var capacity *int32
		if kind == dispatcherv1.OverrideKindCapacity {
			capacity, parseErr = coredispatcher.ParseCapacity(positionals[1])
			if parseErr != nil {
				return response(parseErr.Error())
			}
		}
		request := coredispatcher.PlanRequest{
			Kind: kind, Cluster: positionals[0], Capability: options["capability"], Capacity: capacity,
			DurationSeconds: duration, Reason: options["reason"], IncidentURL: options["incident"],
			UserID: command.UserID, ChannelID: command.ChannelID, IdempotencyKey: idempotencyKey(command, "plan"),
		}
		var plan coredispatcher.DispatchPlan
		plan, err = h.client.Plan(ctx, request)
		if err == nil {
			text = formatPlan(plan)
		}
	case "apply", "approve":
		if !h.options.EnableApply {
			return response(fmt.Sprintf("Dispatcher %s is disabled while the command is in read-only shadow mode.", tokens[0]))
		}
		positionals, _, parseErr := parseOptions(tokens[1:], 1)
		if parseErr != nil {
			return response(parseErr.Error() + ". " + usage())
		}
		var preview coredispatcher.DispatchPlan
		preview, err = h.client.GetPlan(ctx, positionals[0])
		if err != nil {
			break
		}
		if refusal := h.disabledPlanKind(preview); refusal != "" {
			return response(refusal)
		}
		var override *dispatcherv1.DispatchOverride
		override, err = h.client.Apply(ctx, positionals[0], coredispatcher.ApplyRequest{
			UserID: command.UserID, ChannelID: command.ChannelID, IdempotencyKey: idempotencyKey(command, tokens[0]),
			SlackThreadTS: command.ThreadTS,
		})
		if err == nil {
			resultingOverride = override
			if tokens[0] == "approve" {
				text = fmt.Sprintf("Recorded approval for override `%s`; it is %s with %d/%d approvals. Policy generation: %d.", override.Spec.ID, override.Status.State, len(override.Spec.Approvals), override.Spec.RequiredApprovals, override.Status.PolicyGeneration)
			} else {
				text = fmt.Sprintf("Override `%s` is %s with %d/%d approvals. Policy generation: %d.", override.Spec.ID, override.Status.State, len(override.Spec.Approvals), override.Spec.RequiredApprovals, override.Status.PolicyGeneration)
			}
		}
	case "cancel":
		if !h.options.EnableApply {
			return response("Dispatcher cancel is disabled while the command is in read-only shadow mode.")
		}
		positionals, _, parseErr := parseOptions(tokens[1:], 1)
		if parseErr != nil {
			return response(parseErr.Error() + ". " + usage())
		}
		var override *dispatcherv1.DispatchOverride
		override, err = h.client.Cancel(ctx, positionals[0], coredispatcher.CancelRequest{UserID: command.UserID, ChannelID: command.ChannelID, IdempotencyKey: idempotencyKey(command, "cancel")})
		if err == nil {
			resultingOverride = override
			text = fmt.Sprintf("Override `%s` is %s. Policy generation: %d.", override.Spec.ID, override.Status.State, override.Status.PolicyGeneration)
		}
	default:
		return response(usage())
	}
	if err != nil {
		return response("Dispatcher request failed: " + err.Error())
	}
	result := response(text)
	result.override = resultingOverride
	return result
}
