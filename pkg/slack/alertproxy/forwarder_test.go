package alertproxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
)

func TestClaims(t *testing.T) {
	testCases := []struct {
		name     string
		callback *slack.InteractionCallback
		want     bool
	}{
		{name: "unrelated", callback: &slack.InteractionCallback{CallbackID: "incident"}},
		{name: "callback", callback: &slack.InteractionCallback{CallbackID: "alert-proxy:ack:a1"}, want: true},
		{name: "view", callback: &slack.InteractionCallback{View: slack.View{CallbackID: "alert-proxy:silence:a1"}}, want: true},
		{name: "view metadata", callback: &slack.InteractionCallback{View: slack.View{PrivateMetadata: "alert-proxy:submit:a1"}}, want: true},
		{name: "block action", callback: &slack.InteractionCallback{ActionCallback: slack.ActionCallbacks{BlockActions: []*slack.BlockAction{{ActionID: "alert-proxy:unsilence:a1"}}}}, want: true},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := Claims(testCase.callback); got != testCase.want {
				t.Fatalf("Claims() = %t, want %t", got, testCase.want)
			}
		})
	}
}

type capturedRequest struct {
	body   []byte
	header http.Header
	method string
	path   string
}

func TestRelayInteraction(t *testing.T) {
	const timestamp = int64(1788850923)
	body := []byte(`payload=%7B%22type%22%3A%22block_actions%22%2C%22value%22%3A%22a%2Bb%22%7D&keep=%2f%2F+%20`)
	headers := http.Header{
		"Content-Type":              {"application/x-www-form-urlencoded"},
		"X-Slack-Request-Timestamp": {"1788850900"},
		"X-Slack-Signature":         {"v0=slack-signature"},
		TimestampHeader:             {"attacker-timestamp"},
		SignatureHeader:             {"attacker-signature"},
	}
	requests := make(chan capturedRequest, 2)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		gotBody, err := io.ReadAll(request.Body)
		if err != nil {
			http.Error(writer, err.Error(), http.StatusInternalServerError)
			return
		}
		requests <- capturedRequest{body: gotBody, header: request.Header.Clone(), method: request.Method, path: request.URL.Path}
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusAccepted)
		_, _ = writer.Write([]byte(`{"response_action":"clear"}`))
	}))
	defer server.Close()

	secret := []byte("forwarder-secret")
	secretReads := 0
	relay := NewRelay(func() []byte {
		secretReads++
		return secret
	})
	relay.now = func() time.Time { return time.Unix(timestamp, 0) }
	response, err := relay.RelayInteraction(context.Background(), server.URL+"/interactions", headers, body)
	if err != nil {
		t.Fatalf("RelayInteraction() error: %v", err)
	}
	request := <-requests
	if request.method != http.MethodPost || request.path != "/interactions" || !bytes.Equal(request.body, body) {
		t.Fatalf("forwarded request = %s %s %q", request.method, request.path, request.body)
	}
	if request.header.Get("Content-Type") != "application/x-www-form-urlencoded" ||
		request.header.Get("X-Slack-Request-Timestamp") != "1788850900" ||
		request.header.Get("X-Slack-Signature") != "v0=slack-signature" ||
		request.header.Get(TimestampHeader) != "1788850923" ||
		request.header.Get(SignatureHeader) != "v1=f5ae6fed5e77487ec3b0c9631ec538e2489ae063df1d92d2752ef38462d8571c" {
		t.Fatalf("forwarded headers are incorrect: %#v", request.header)
	}
	if response.StatusCode != http.StatusAccepted || response.Header.Get("Content-Type") != "application/json" || string(response.Body) != `{"response_action":"clear"}` {
		t.Fatalf("unexpected response: %#v", response)
	}
	if headers.Get(TimestampHeader) != "attacker-timestamp" || headers.Get(SignatureHeader) != "attacker-signature" {
		t.Fatalf("input headers were mutated: %#v", headers)
	}
	if relay.client.Timeout != 2*time.Second {
		t.Fatalf("relay timeout = %s, want 2s", relay.client.Timeout)
	}

	secret = []byte("rotated-secret")
	if _, err := relay.RelayInteraction(context.Background(), server.URL, nil, body); err != nil {
		t.Fatalf("RelayInteraction() after rotation: %v", err)
	}
	rotated := <-requests
	if rotated.header.Get(SignatureHeader) == request.header.Get(SignatureHeader) || secretReads != 2 {
		t.Fatalf("secret rotation was not observed: reads=%d signatures=(%q, %q)", secretReads, request.header.Get(SignatureHeader), rotated.header.Get(SignatureHeader))
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestRelayErrorsDoNotExposeTarget(t *testing.T) {
	const marker = "do-not-log"
	relay := NewRelay(func() []byte { return []byte("secret") })
	relay.client.Transport = roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("transport error containing " + marker)
	})
	_, err := relay.RelayInteraction(context.Background(), "https://user:"+marker+"@example.invalid/interactions?token="+marker, nil, nil)
	if err == nil || strings.Contains(err.Error(), marker) {
		t.Fatalf("relay error exposed request data: %v", err)
	}
}

func TestRelayDoesNotFollowRedirects(t *testing.T) {
	var destinationRequests atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		destinationRequests.Add(1)
	}))
	defer destination.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.Redirect(writer, request, destination.URL, http.StatusFound)
	}))
	defer redirect.Close()

	response, err := NewRelay(func() []byte { return []byte("secret") }).RelayInteraction(context.Background(), redirect.URL, nil, nil)
	if err != nil {
		t.Fatalf("RelayInteraction() error: %v", err)
	}
	if response.StatusCode != http.StatusFound || destinationRequests.Load() != 0 {
		t.Fatalf("status=%d destination requests=%d", response.StatusCode, destinationRequests.Load())
	}
}

func mentionCallback(text, channel, user, botID string) *slackevents.EventsAPIEvent {
	return &slackevents.EventsAPIEvent{
		Type: slackevents.CallbackEvent,
		Data: &slackevents.EventsAPICallbackEvent{EventID: "Ev-123", AuthedUsers: []string{"BOT"}},
		InnerEvent: slackevents.EventsAPIInnerEvent{Data: &slackevents.AppMentionEvent{
			Type: slackevents.AppMention, Text: text, Channel: channel, User: user, BotID: botID,
			TimeStamp: "100.1", ThreadTimeStamp: "99.1",
		}},
	}
}

func TestMentionHandler(t *testing.T) {
	const timestamp = int64(1788850923)
	requests := make(chan capturedRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		requests <- capturedRequest{body: body, header: request.Header.Clone()}
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	relay := NewRelay(func() []byte { return []byte("forwarder-secret") })
	relay.now = func() time.Time { return time.Unix(timestamp, 0) }
	var result string
	handler := relay.MentionHandler(server.URL, "CHY2E1BL4", func(got string) { result = got })
	handled, err := handler.Handle(
		mentionCallback("<@BOT> ci-alerts   silence AlertA 2h planned work", "CHY2E1BL4", "U-admin", ""),
		logrus.NewEntry(logrus.New()),
	)
	if err != nil || !handled || result != "success" {
		t.Fatalf("Handle() = (%t, %v), result=%q", handled, err, result)
	}
	request := <-requests
	var envelope MentionEnvelope
	if err := json.Unmarshal(request.body, &envelope); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}
	want := (MentionEnvelope{EventID: "Ev-123", IssuedAt: timestamp, Channel: "CHY2E1BL4", User: "U-admin", ThreadTS: "99.1", Text: "silence AlertA 2h planned work"})
	if envelope != want || request.header.Get("Content-Type") != "application/json" {
		t.Fatalf("envelope=%#v headers=%#v, want %#v", envelope, request.header, want)
	}
}

func TestMentionHandlerBoundaries(t *testing.T) {
	var forwarded atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		forwarded.Add(1)
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	unauthenticated := mentionCallback("<@BOT> ci-alerts list", "CHY2E1BL4", "U1", "")
	unauthenticated.Data.(*slackevents.EventsAPICallbackEvent).AuthedUsers = []string{"OTHER"}
	testCases := []struct {
		name        string
		callback    *slackevents.EventsAPIEvent
		wantHandled bool
		wantResult  string
	}{
		{name: "unrelated command", callback: mentionCallback("<@BOT> help", "CHY2E1BL4", "U1", "")},
		{name: "unauthenticated bot identity", callback: unauthenticated, wantHandled: true, wantResult: "invalid"},
		{name: "wrong channel", callback: mentionCallback("<@BOT> ci-alerts list", "C-other", "U1", ""), wantHandled: true, wantResult: "denied"},
		{name: "bot actor", callback: mentionCallback("<@BOT> ci-alerts list", "CHY2E1BL4", "U1", "B1"), wantHandled: true, wantResult: "invalid"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			var result string
			handler := NewRelay(func() []byte { return []byte("secret") }).MentionHandler(server.URL, "CHY2E1BL4", func(got string) { result = got })
			handled, err := handler.Handle(testCase.callback, logrus.NewEntry(logrus.New()))
			if err != nil || handled != testCase.wantHandled || result != testCase.wantResult {
				t.Fatalf("Handle() = (%t, %v), result=%q", handled, err, result)
			}
		})
	}
	if forwarded.Load() != 0 {
		t.Fatalf("forwarded %d rejected mentions", forwarded.Load())
	}
}
