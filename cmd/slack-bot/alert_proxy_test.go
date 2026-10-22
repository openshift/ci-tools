package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"

	alertproxyforward "github.com/openshift/ci-tools/pkg/slack/alertproxy"
	eventhandler "github.com/openshift/ci-tools/pkg/slack/events"
	interactionhandler "github.com/openshift/ci-tools/pkg/slack/interactions"
)

func TestValidateAlertProxyEndpointURL(t *testing.T) {
	const (
		internal           = "http://alert-proxy.ci.svc:8080"
		external           = "https://alert-proxy-ci.apps.master.ci.devcluster.openshift.com"
		invalidDestination = "must target the alert-proxy service in namespace ci or the approved Core CI HTTPS route"
		invalidExtra       = "must not contain user information, a query, or a fragment"
	)
	for _, tc := range []struct {
		name    string
		value   string
		path    string
		wantErr string
	}{
		{name: "internal HTTP interactions", value: internal + "/interactions", path: "/interactions"},
		{name: "internal HTTP mentions", value: internal + "/mentions", path: "/mentions"},
		{name: "internal HTTPS", value: "https://alert-proxy.ci.svc:8080/interactions", path: "/interactions"},
		{name: "internal fully qualified HTTP", value: "http://alert-proxy.ci.svc.cluster.local:8080/interactions", path: "/interactions"},
		{name: "internal fully qualified HTTPS", value: "https://alert-proxy.ci.svc.cluster.local:8080/interactions", path: "/interactions"},
		{name: "internal normalized host", value: "http://ALERT-PROXY.CI.SVC.:8080/interactions", path: "/interactions"},
		{name: "Core CI interactions default port", value: external + "/interactions", path: "/interactions"},
		{name: "Core CI mentions default port", value: external + "/mentions", path: "/mentions"},
		{name: "Core CI interactions port 443", value: external + ":443/interactions", path: "/interactions"},
		{name: "Core CI mentions port 443", value: external + ":443/mentions", path: "/mentions"},
		{name: "relative", value: "/interactions", path: "/interactions", wantErr: "must be an absolute URL"},
		{name: "malformed port", value: external + ":invalid/interactions", path: "/interactions", wantErr: "must be an absolute URL"},
		{name: "FTP", value: "ftp://alert-proxy.ci.svc:8080/interactions", path: "/interactions", wantErr: "scheme must be HTTP or HTTPS"},
		{name: "other host", value: "https://alert-proxy.example.com:8080/interactions", path: "/interactions", wantErr: invalidDestination},
		{name: "internal wrong port", value: "http://alert-proxy.ci.svc:8443/interactions", path: "/interactions", wantErr: "must target alert-proxy service port 8080"},
		{name: "internal no port", value: "http://alert-proxy.ci.svc/interactions", path: "/interactions", wantErr: "must target alert-proxy service port 8080"},
		{name: "internal wrong path", value: internal + "/mentions", path: "/interactions", wantErr: "path must be /interactions"},
		{name: "internal query", value: internal + "/interactions?target=x", path: "/interactions", wantErr: invalidExtra},
		{name: "internal bare query", value: internal + "/interactions?", path: "/interactions", wantErr: invalidExtra},
		{name: "internal fragment", value: internal + "/interactions#part", path: "/interactions", wantErr: invalidExtra},
		{name: "internal empty fragment", value: internal + "/interactions#", path: "/interactions", wantErr: invalidExtra},
		{name: "Core CI HTTP", value: "http://alert-proxy-ci.apps.master.ci.devcluster.openshift.com/interactions", path: "/interactions", wantErr: "Core CI route requires HTTPS"},
		{name: "Core CI HTTP port 443", value: "http://alert-proxy-ci.apps.master.ci.devcluster.openshift.com:443/interactions", path: "/interactions", wantErr: "Core CI route requires HTTPS"},
		{name: "Core CI wrong port", value: external + ":8080/interactions", path: "/interactions", wantErr: invalidDestination},
		{name: "Core CI empty port", value: external + ":/interactions", path: "/interactions", wantErr: invalidDestination},
		{name: "Core CI suffix host", value: external + ".evil.invalid/interactions", path: "/interactions", wantErr: invalidDestination},
		{name: "Core CI prefix host", value: "https://evil-alert-proxy-ci.apps.master.ci.devcluster.openshift.com/interactions", path: "/interactions", wantErr: invalidDestination},
		{name: "Core CI trailing dot", value: external + "./interactions", path: "/interactions", wantErr: invalidDestination},
		{name: "Core CI uppercase host", value: "https://ALERT-PROXY-CI.apps.master.ci.devcluster.openshift.com/interactions", path: "/interactions", wantErr: invalidDestination},
		{name: "Core CI wrong path", value: external + "/mentions", path: "/interactions", wantErr: "path must be /interactions"},
		{name: "Core CI escaped path", value: external + "/%69nteractions", path: "/interactions", wantErr: "path must be /interactions"},
		{name: "Core CI escaped slash", value: external + "/interactions%2f", path: "/interactions", wantErr: "path must be /interactions"},
		{name: "Core CI path suffix", value: external + "/interactions/", path: "/interactions", wantErr: "path must be /interactions"},
		{name: "Core CI user information", value: "https://user:do-not-log@alert-proxy-ci.apps.master.ci.devcluster.openshift.com/interactions", path: "/interactions", wantErr: invalidExtra},
		{name: "Core CI query", value: external + "/interactions?token=do-not-log", path: "/interactions", wantErr: invalidExtra},
		{name: "Core CI bare query", value: external + "/interactions?", path: "/interactions", wantErr: invalidExtra},
		{name: "Core CI fragment", value: external + "/interactions#part", path: "/interactions", wantErr: invalidExtra},
		{name: "Core CI empty fragment", value: external + "/interactions#", path: "/interactions", wantErr: invalidExtra},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateAlertProxyEndpointURL(tc.value, tc.path)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected validation error: %v", err)
				}
				return
			}
			if err == nil || err.Error() != tc.wantErr {
				t.Fatalf("validation error = %v, want %q", err, tc.wantErr)
			}
		})
	}
}

func TestAlertProxyOptionValidation(t *testing.T) {
	base := gatherOptions(flag.NewFlagSet("validation", flag.ContinueOnError),
		"--slack-token-path=/token",
		"--slack-signing-secret-path=/signing",
		"--pager-duty-token-file=/pager-duty",
		"--prow-config-path=/prow-config",
	)
	testCases := []struct {
		name    string
		mutate  func(*options)
		wantErr bool
	}{
		{name: "disabled"},
		{name: "interaction", mutate: func(o *options) {
			o.alertProxyInteractionURL = "http://alert-proxy.ci.svc:8080/interactions"
			o.alertProxyForwarderSecretPath = "/forwarder"
		}},
		{name: "mention", mutate: func(o *options) {
			o.alertProxyMentionURL = "http://alert-proxy.ci.svc:8080/mentions"
			o.alertProxyForwarderSecretPath = "/forwarder"
		}},
		{name: "missing secret", mutate: func(o *options) {
			o.alertProxyInteractionURL = "http://alert-proxy.ci.svc:8080/interactions"
		}, wantErr: true},
		{name: "orphan secret", mutate: func(o *options) { o.alertProxyForwarderSecretPath = "/forwarder" }, wantErr: true},
		{name: "missing mention channel", mutate: func(o *options) {
			o.alertProxyMentionURL = "http://alert-proxy.ci.svc:8080/mentions"
			o.alertProxyForwarderSecretPath = "/forwarder"
			o.alertProxyChannelID = ""
		}, wantErr: true},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			o := base
			if testCase.mutate != nil {
				testCase.mutate(&o)
			}
			if err := o.Validate(); (err != nil) != testCase.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %t", err, testCase.wantErr)
			}
		})
	}
}

func TestComposeEventRoutesOrder(t *testing.T) {
	var calls []string
	partial := func(name string, claim bool) eventhandler.PartialHandler {
		return eventhandler.PartialHandlerFunc(name, func(*slackevents.EventsAPIEvent, *logrus.Entry) (bool, error) {
			calls = append(calls, name)
			return claim, nil
		})
	}
	base := eventhandler.HandlerFunc("base", func(*slackevents.EventsAPIEvent, *logrus.Entry) error {
		calls = append(calls, "base")
		return nil
	})
	for _, testCase := range []struct {
		name       string
		alertClaim bool
		want       []string
	}{
		{name: "alert proxy claims first", alertClaim: true, want: []string{"alert-proxy"}},
		{name: "fall through in order", want: []string{"alert-proxy", "dispatcher", "base"}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			calls = nil
			routes := composeEventRoutes(base, partial("alert-proxy", testCase.alertClaim), partial("dispatcher", false))
			if err := routes.Handle(&slackevents.EventsAPIEvent{}, logrus.NewEntry(logrus.New())); err != nil {
				t.Fatalf("Handle() error: %v", err)
			}
			if !slices.Equal(calls, testCase.want) {
				t.Fatalf("calls = %#v, want %#v", calls, testCase.want)
			}
		})
	}
}

func signedSlackInteraction(body, secret string) *http.Request {
	request := httptest.NewRequest(http.MethodPost, "/slack/interactive-endpoint", strings.NewReader(body))
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte("v0:" + timestamp + ":" + body))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("X-Slack-Request-Timestamp", timestamp)
	request.Header.Set("X-Slack-Signature", "v0="+hex.EncodeToString(mac.Sum(nil)))
	return request
}

func interactionBody(actionID string) string {
	payload := `{"type":"block_actions","trigger_id":"Tr-1","channel":{"id":"CHY2E1BL4"},"user":{"id":"U1"},"actions":[{"action_id":"` + actionID + `","block_id":"controls"}]}`
	return "payload=" + url.QueryEscape(payload) + "&preserve=%2f%2F+%20"
}

type ephemeralCall struct{ channel, user string }

type fakeEphemeralMessenger struct{ calls chan ephemeralCall }

func (f *fakeEphemeralMessenger) PostEphemeralContext(_ context.Context, channelID, userID string, _ ...slack.MsgOption) (string, error) {
	f.calls <- ephemeralCall{channel: channelID, user: userID}
	return "", nil
}

func TestInteractionForwarding(t *testing.T) {
	type forwardedRequest struct {
		body   []byte
		header http.Header
	}
	forwarded := make(chan forwardedRequest, 1)
	proxy := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		forwarded <- forwardedRequest{body: body, header: request.Header.Clone()}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"response_action":"clear"}`))
	}))
	defer proxy.Close()

	fallbackCalled := false
	fallback := interactionhandler.HandlerFunc("fallback", func(*slack.InteractionCallback, *logrus.Entry) ([]byte, error) {
		fallbackCalled = true
		return nil, errors.New("claimed callback reached fallback")
	})
	body := interactionBody("alert-proxy:ack:a1")
	endpoint := handleInteraction(
		func() []byte { return []byte("slack-secret") }, proxy.URL,
		alertproxyforward.NewRelay(func() []byte { return []byte("forwarder-secret") }), nil, fallback,
	)
	response := httptest.NewRecorder()
	endpoint.ServeHTTP(response, signedSlackInteraction(body, "slack-secret"))
	if response.Code != http.StatusOK || response.Body.String() != `{"response_action":"clear"}` || fallbackCalled {
		t.Fatalf("status=%d body=%q fallbackCalled=%t", response.Code, response.Body.String(), fallbackCalled)
	}
	request := <-forwarded
	if string(request.body) != body || request.header.Get("X-Slack-Signature") == "" || request.header.Get(alertproxyforward.SignatureHeader) == "" {
		t.Fatalf("forwarded body=%q headers=%#v", request.body, request.header)
	}
}

func TestInteractionFallback(t *testing.T) {
	proxyCalled := make(chan struct{}, 1)
	proxy := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { proxyCalled <- struct{}{} }))
	defer proxy.Close()
	fallback := interactionhandler.HandlerFunc("fallback", func(*slack.InteractionCallback, *logrus.Entry) ([]byte, error) {
		return []byte(`{"fallback":true}`), nil
	})
	endpoint := handleInteraction(
		func() []byte { return []byte("slack-secret") }, proxy.URL,
		alertproxyforward.NewRelay(func() []byte { return []byte("forwarder-secret") }), nil, fallback,
	)
	response := httptest.NewRecorder()
	endpoint.ServeHTTP(response, signedSlackInteraction(interactionBody("incident:open"), "slack-secret"))
	if response.Body.String() != `{"fallback":true}` {
		t.Fatalf("response = %q", response.Body.String())
	}
	select {
	case <-proxyCalled:
		t.Fatal("unclaimed interaction was forwarded")
	default:
	}
}

func TestInteractionForwardingFailure(t *testing.T) {
	proxy := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		http.Error(writer, "unavailable", http.StatusServiceUnavailable)
	}))
	defer proxy.Close()
	messenger := &fakeEphemeralMessenger{calls: make(chan ephemeralCall, 1)}
	endpoint := handleInteraction(
		func() []byte { return []byte("slack-secret") }, proxy.URL,
		alertproxyforward.NewRelay(func() []byte { return []byte("forwarder-secret") }), messenger, nil,
	)
	response := httptest.NewRecorder()
	endpoint.ServeHTTP(response, signedSlackInteraction(interactionBody("alert-proxy:ack:a1"), "slack-secret"))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
	select {
	case call := <-messenger.calls:
		if call != (ephemeralCall{channel: "CHY2E1BL4", user: "U1"}) {
			t.Fatalf("ephemeral call = %#v", call)
		}
	case <-time.After(time.Second):
		t.Fatal("ephemeral failure message was not posted")
	}
}
