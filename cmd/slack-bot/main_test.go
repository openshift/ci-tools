package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/slack-go/slack/slackevents"

	eventhandler "github.com/openshift/ci-tools/pkg/slack/events"
)

func signedSlackEvent(body, secret string) *http.Request {
	request := httptest.NewRequest(http.MethodPost, "/slack/events-endpoint", strings.NewReader(body))
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte("v0:" + timestamp + ":" + body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Slack-Request-Timestamp", timestamp)
	request.Header.Set("X-Slack-Signature", "v0="+hex.EncodeToString(mac.Sum(nil)))
	return request
}

func TestEventEndpointVerifiesSignatureAndPreservesEventID(t *testing.T) {
	const body = `{"type":"event_callback","team_id":"T-team","api_app_id":"A-app","event_id":"Ev-dispatch","event_time":1787263200,"event":{"type":"app_mention","user":"U1","text":"<@B1> tp-dispatch status","ts":"100.1","channel":"C-team","event_ts":"100.1"}}`
	handled := make(chan *slackevents.EventsAPIEvent, 1)
	endpoint := handleEvent(func() []byte { return []byte("signing-secret") }, eventhandler.HandlerFunc("test", func(event *slackevents.EventsAPIEvent, _ *logrus.Entry) error {
		handled <- event
		return nil
	}))

	unsigned := httptest.NewRecorder()
	endpoint.ServeHTTP(unsigned, httptest.NewRequest(http.MethodPost, "/slack/events-endpoint", strings.NewReader(body)))
	if unsigned.Code == http.StatusOK {
		t.Fatal("unsigned Slack event was accepted")
	}

	signed := httptest.NewRecorder()
	endpoint.ServeHTTP(signed, signedSlackEvent(body, "signing-secret"))
	if signed.Code != http.StatusOK {
		t.Fatalf("signed Slack event returned %d", signed.Code)
	}
	select {
	case event := <-handled:
		outer, ok := event.Data.(*slackevents.EventsAPICallbackEvent)
		if !ok || outer.EventID != "Ev-dispatch" {
			t.Fatalf("event ID was not preserved: %#v", event.Data)
		}
	case <-time.After(time.Second):
		t.Fatal("signed Slack event was not dispatched")
	}
}

func TestValidateDispatcherControlURL(t *testing.T) {
	testCases := []struct {
		name    string
		value   string
		wantErr bool
	}{
		{name: "external HTTPS", value: "https://dispatcher.example.com/control"},
		{name: "in-cluster short service URL", value: "http://prow-job-dispatcher.ci.svc:8080"},
		{name: "in-cluster fully-qualified service URL", value: "http://prow-job-dispatcher.ci.svc.cluster.local:8080"},
		{name: "external HTTP", value: "http://dispatcher.example.com", wantErr: true},
		{name: "ambiguous cluster search name", value: "http://prow-job-dispatcher.ci:8080", wantErr: true},
		{name: "relative URL", value: "/control", wantErr: true},
		{name: "missing host", value: "https:///control", wantErr: true},
		{name: "unsupported scheme", value: "ftp://prow-job-dispatcher.ci.svc", wantErr: true},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			err := validateDispatcherControlURL(testCase.value)
			if (err != nil) != testCase.wantErr {
				t.Fatalf("validateDispatcherControlURL(%q) error = %v, wantErr %t", testCase.value, err, testCase.wantErr)
			}
		})
	}
}
