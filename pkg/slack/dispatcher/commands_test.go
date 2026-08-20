package dispatcher

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	dispatcherv1 "github.com/openshift/ci-tools/pkg/api/dispatcher/v1"
	coredispatcher "github.com/openshift/ci-tools/pkg/dispatcher"
)

type fakeControlClient struct {
	calls          int
	planRequest    coredispatcher.PlanRequest
	applyRequests  []coredispatcher.ApplyRequest
	cancelRequests []coredispatcher.CancelRequest
}

func (f *fakeControlClient) Status(context.Context, string) (coredispatcher.ControlStatus, error) {
	f.calls++
	return coredispatcher.ControlStatus{Ready: true, Generation: 3, PolicyInputDigest: "1234567890abcdef"}, nil
}

func (f *fakeControlClient) Overrides(context.Context) ([]dispatcherv1.DispatchOverride, error) {
	f.calls++
	return nil, nil
}

func (f *fakeControlClient) Plan(_ context.Context, request coredispatcher.PlanRequest) (coredispatcher.DispatchPlan, error) {
	f.calls++
	f.planRequest = request
	return coredispatcher.DispatchPlan{
		ID: "plan-1", Request: request, Impact: coredispatcher.CompileSummary{AffectedJobs: 2, MovedJobs: 1},
		RequiredApprovals: 1, PropagationBound: 30 * time.Second,
	}, nil
}

func (f *fakeControlClient) Apply(_ context.Context, _ string, request coredispatcher.ApplyRequest) (*dispatcherv1.DispatchOverride, error) {
	f.calls++
	f.applyRequests = append(f.applyRequests, request)
	return &dispatcherv1.DispatchOverride{
		Spec: dispatcherv1.DispatchOverrideSpec{
			ID: "override-1", SourceChannelID: request.ChannelID, SlackThreadTS: request.SlackThreadTS,
			RequiredApprovals: 1, Approvals: []dispatcherv1.DispatchApproval{{UserID: request.UserID}},
		},
		Status: dispatcherv1.DispatchOverrideStatus{State: dispatcherv1.OverrideStateActive, PolicyGeneration: 4},
	}, nil
}

func (f *fakeControlClient) Cancel(_ context.Context, _ string, request coredispatcher.CancelRequest) (*dispatcherv1.DispatchOverride, error) {
	f.calls++
	f.cancelRequests = append(f.cancelRequests, request)
	return &dispatcherv1.DispatchOverride{Spec: dispatcherv1.DispatchOverrideSpec{ID: "override-1"}, Status: dispatcherv1.DispatchOverrideStatus{State: dispatcherv1.OverrideStateRevoked, PolicyGeneration: 5}}, nil
}

type postedMessage struct {
	channel, text, threadTS string
}

type fakeMessenger struct {
	posts []postedMessage
	err   error
}

func (f *fakeMessenger) PostMessage(channelID string, options ...slack.MsgOption) (string, string, error) {
	if f.err != nil {
		return "", "", f.err
	}
	_, values, err := slack.UnsafeApplyMsgOptions("token", channelID, "https://slack.com/api/chat.postMessage", options...)
	if err != nil {
		return "", "", err
	}
	f.posts = append(f.posts, postedMessage{channel: channelID, text: values.Get("text"), threadTS: values.Get("thread_ts")})
	return channelID, fmt.Sprintf("reply-%d", len(f.posts)), nil
}

func mentionCallback(eventID, channel, user, text, timestamp, threadTS string) *slackevents.EventsAPIEvent {
	return &slackevents.EventsAPIEvent{
		TeamID: "T-team", Type: slackevents.CallbackEvent,
		Data: &slackevents.EventsAPICallbackEvent{EventID: eventID},
		InnerEvent: slackevents.EventsAPIInnerEvent{Data: &slackevents.AppMentionEvent{
			User: user, Channel: channel, Text: text, TimeStamp: timestamp, ThreadTimeStamp: threadTS, EventTimeStamp: json.Number(timestamp),
		}},
	}
}

func TestMentionHandlerRoutesExactPrefixAndDeniesWrongChannel(t *testing.T) {
	client := &fakeControlClient{}
	messenger := &fakeMessenger{}
	denials := 0
	handler, err := NewHandler(client, Options{ChannelID: "C-team", Messenger: messenger, OnDenial: func(Command) { denials++ }})
	if err != nil {
		t.Fatal(err)
	}
	logger := logrus.NewEntry(logrus.New())
	handled, err := handler.MentionHandler().Handle(mentionCallback("Ev-generic", "C-team", "U1", "<@B1> help", "100.1", ""), logger)
	if err != nil || handled || client.calls != 0 || len(messenger.posts) != 0 {
		t.Fatalf("generic mention did not fall through: handled=%t calls=%d posts=%d err=%v", handled, client.calls, len(messenger.posts), err)
	}
	handled, err = handler.MentionHandler().Handle(mentionCallback("Ev-near", "C-team", "U1", "<@B1> tp-dispatcher status", "100.15", ""), logger)
	if err != nil || handled || client.calls != 0 || len(messenger.posts) != 0 {
		t.Fatalf("near-prefix mention did not fall through: handled=%t calls=%d posts=%d err=%v", handled, client.calls, len(messenger.posts), err)
	}
	handled, err = handler.MentionHandler().Handle(mentionCallback("Ev-wrong", "C-other", "U1", "<@B1> tp-dispatch status", "100.2", ""), logger)
	if err != nil || !handled || client.calls != 0 || denials != 1 || len(messenger.posts) != 1 || !strings.Contains(messenger.posts[0].text, "<#C-team>") {
		t.Fatalf("wrong-channel command crossed boundary: handled=%t calls=%d denials=%d posts=%#v err=%v", handled, client.calls, denials, messenger.posts, err)
	}
}

func TestHandlerDefaultTimeoutCoversControlClientTimeout(t *testing.T) {
	handler, err := NewHandler(&fakeControlClient{}, Options{ChannelID: "C-team"})
	if err != nil {
		t.Fatal(err)
	}
	if handler.options.Timeout < 10*time.Second {
		t.Fatalf("default command timeout %s is shorter than the control client timeout", handler.options.Timeout)
	}
}

func TestReadOnlyShadowModeAllowsPlanButRejectsMutations(t *testing.T) {
	client := &fakeControlClient{}
	handler, err := NewHandler(client, Options{ChannelID: "C-team"})
	if err != nil {
		t.Fatal(err)
	}
	command := Command{
		TeamID: "T-team", ChannelID: "C-team", UserID: "U1", RequestID: "Ev-plan", ThreadTS: "100.1",
		Text: "plan capacity build01 25 --capability intranet --for 2h --reason INC-123 API outage",
	}
	result := handler.Handle(context.Background(), command)
	if !strings.Contains(result.Text, "plan-1") || !strings.Contains(result.Text, "@dptp-bot tp-dispatch apply") || client.calls != 1 {
		t.Fatalf("plan failed in shadow mode: %#v, calls=%d", result, client.calls)
	}
	if client.planRequest.Capability != "intranet" || client.planRequest.DurationSeconds != 7200 || client.planRequest.Reason != "INC-123 API outage" || client.planRequest.IdempotencyKey != "plan:Ev-plan" {
		t.Fatalf("plan parsed incorrectly: %#v", client.planRequest)
	}
	result = handler.Handle(context.Background(), Command{ChannelID: "C-team", UserID: "U1", Text: "apply plan-1"})
	if !strings.Contains(result.Text, "disabled") || client.calls != 1 {
		t.Fatalf("apply was not held in shadow mode: %#v, calls=%d", result, client.calls)
	}
	result = handler.Handle(context.Background(), Command{ChannelID: "C-team", UserID: "U1", Text: "cancel override-1"})
	if !strings.Contains(result.Text, "disabled") || client.calls != 1 {
		t.Fatalf("cancel was not held in shadow mode: %#v, calls=%d", result, client.calls)
	}
}

func TestUnknownOptionIsRejectedBeforeDispatcherCall(t *testing.T) {
	client := &fakeControlClient{}
	handler, err := NewHandler(client, Options{ChannelID: "C-team"})
	if err != nil {
		t.Fatal(err)
	}
	result := handler.Handle(context.Background(), Command{
		ChannelID: "C-team", UserID: "U1",
		Text: "plan drain build01 --for 1h --reason incident --capabilty intranet",
	})
	if client.calls != 0 || !strings.Contains(result.Text, "unknown option") {
		t.Fatalf("unknown option crossed the dispatcher boundary: calls=%d response=%#v", client.calls, result)
	}
}

func TestMentionRetryIsIdempotentAndApplyUsesOriginatingThread(t *testing.T) {
	client := &fakeControlClient{}
	messenger := &fakeMessenger{}
	handler, err := NewHandler(client, Options{ChannelID: "C-team", EnableApply: true, Messenger: messenger})
	if err != nil {
		t.Fatal(err)
	}
	callback := mentionCallback("Ev-apply", "C-team", "U2", "<@B1> tp-dispatch apply plan-1 --confirm-fallback", "101.2", "100.1")
	logger := logrus.NewEntry(logrus.New())
	for range 2 {
		handled, err := handler.MentionHandler().Handle(callback, logger)
		if err != nil || !handled {
			t.Fatalf("apply mention was not handled: handled=%t err=%v", handled, err)
		}
	}
	if client.calls != 1 || len(client.applyRequests) != 1 || len(messenger.posts) != 1 {
		t.Fatalf("Slack retry was not deduplicated: calls=%d apply=%d posts=%d", client.calls, len(client.applyRequests), len(messenger.posts))
	}
	request := client.applyRequests[0]
	if request.IdempotencyKey != "apply:Ev-apply" || request.SlackThreadTS != "100.1" || !request.FallbackConfirmed {
		t.Fatalf("apply identity or thread was not preserved: %#v", request)
	}
	if messenger.posts[0].channel != "C-team" || messenger.posts[0].threadTS != "100.1" || !strings.Contains(messenger.posts[0].text, "override-1") {
		t.Fatalf("response was not posted to the originating thread: %#v", messenger.posts[0])
	}
}

func TestMutatingCommandsUseEventIdentity(t *testing.T) {
	client := &fakeControlClient{}
	handler, err := NewHandler(client, Options{ChannelID: "C-team", EnableApply: true})
	if err != nil {
		t.Fatal(err)
	}
	result := handler.Handle(context.Background(), Command{ChannelID: "C-team", UserID: "U2", RequestID: "Ev-apply", ThreadTS: "100.1", Text: "apply plan-1 --confirm-fallback"})
	if !strings.Contains(result.Text, "Active") {
		t.Fatalf("apply failed: %#v", result)
	}
	result = handler.Handle(context.Background(), Command{ChannelID: "C-team", UserID: "U2", RequestID: "Ev-cancel", Text: "cancel override-1"})
	if !strings.Contains(result.Text, "Revoked") || client.calls != 2 || client.cancelRequests[0].IdempotencyKey != "cancel:Ev-cancel" {
		t.Fatalf("cancel failed: %#v, calls=%d", result, client.calls)
	}
}

func TestFormatOverridesIncludesExpiry(t *testing.T) {
	override := dispatcherv1.DispatchOverride{Spec: dispatcherv1.DispatchOverrideSpec{ID: "o1", Kind: dispatcherv1.OverrideKindDrain, Cluster: "build01", ExpiresAt: metav1.NewTime(time.Date(2026, 8, 20, 14, 0, 0, 0, time.UTC))}, Status: dispatcherv1.DispatchOverrideStatus{State: dispatcherv1.OverrideStatePending}}
	formatted := formatOverrides([]dispatcherv1.DispatchOverride{override})
	if !strings.Contains(formatted, "o1") || !strings.Contains(formatted, "Pending") || !strings.Contains(formatted, "2026-08-20T14:00:00Z") {
		t.Fatalf("unexpected override presentation: %s", formatted)
	}
}
