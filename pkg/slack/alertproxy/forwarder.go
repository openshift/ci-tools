package alertproxy

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"

	"github.com/openshift/ci-tools/pkg/slack/events"
)

const (
	// InteractionPrefix is reserved for callbacks owned by alert-proxy.
	InteractionPrefix = "alert-proxy:"

	TimestampHeader = "X-Alert-Proxy-Timestamp"
	SignatureHeader = "X-Alert-Proxy-Signature"

	mentionCommandPrefix = "ci-alerts"
	signatureVersion     = "v1"
	defaultRelayTimeout  = 2 * time.Second
	maxResponseBytes     = 1 << 20
)

// MentionEnvelope is the authenticated representation of a ci-alerts app mention.
// IssuedAt is also sent, in decimal form, in TimestampHeader.
type MentionEnvelope struct {
	EventID  string `json:"eventID"`
	IssuedAt int64  `json:"issuedAt"`
	Channel  string `json:"channel"`
	User     string `json:"user"`
	ThreadTS string `json:"threadTS"`
	Text     string `json:"text"`
}

// Response is the response from a successfully reached alert-proxy endpoint.
type Response struct {
	StatusCode int
	Header     http.Header
	Body       []byte
}

// Relay signs and forwards alert-proxy requests. The secret is read for every
// request so rotation through the Prow secret agent does not require a restart.
type Relay struct {
	secret func() []byte
	client *http.Client
	now    func() time.Time
}

func NewRelay(secret func() []byte) *Relay {
	return &Relay{
		secret: secret,
		client: &http.Client{
			Timeout: defaultRelayTimeout,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		now: time.Now,
	}
}

// Claims reports whether any alert-proxy-owned routing field carries the
// reserved prefix. Parsing is intentionally separate from forwarding so the
// caller can relay Slack's original request body byte-for-byte.
func Claims(callback *slack.InteractionCallback) bool {
	if callback == nil {
		return false
	}
	if hasPrefix(callback.CallbackID) || hasPrefix(callback.View.CallbackID) || hasPrefix(callback.View.PrivateMetadata) {
		return true
	}
	for _, action := range callback.ActionCallback.BlockActions {
		if action != nil && hasPrefix(action.ActionID) {
			return true
		}
	}
	return false
}

func hasPrefix(value string) bool {
	return strings.HasPrefix(value, InteractionPrefix)
}

// RelayInteraction forwards Slack's exact form body and original headers,
// adding the alert-proxy forwarder authentication headers.
func (r *Relay) RelayInteraction(ctx context.Context, target string, originalHeaders http.Header, body []byte) (*Response, error) {
	if r == nil {
		return nil, fmt.Errorf("alert-proxy relay is not configured")
	}
	return r.relay(ctx, target, originalHeaders, body, r.now().Unix())
}

// MentionHandler claims exact `ci-alerts` app mentions and forwards a newly
// authenticated envelope. Unrelated mentions fall through to the existing
// generic mention handler.
func (r *Relay) MentionHandler(target, channelID string, observe func(result string)) events.PartialHandler {
	return events.PartialHandlerFunc(mentionCommandPrefix, func(callback *slackevents.EventsAPIEvent, logger *logrus.Entry) (bool, error) {
		if callback.Type != slackevents.CallbackEvent {
			return false, nil
		}
		event, ok := callback.InnerEvent.Data.(*slackevents.AppMentionEvent)
		if !ok {
			return false, nil
		}
		mentionedUserID, text, routed := commandTextFromMention(event.Text)
		if !routed {
			return false, nil
		}
		if !authenticatedUser(callback, mentionedUserID) {
			observeResult(observe, "invalid")
			logger.WithField("mentioned_user_id", mentionedUserID).Warn("ignored alert-proxy command whose leading mention is not an authenticated bot user")
			return true, nil
		}
		if event.Channel != channelID {
			observeResult(observe, "denied")
			logger.WithFields(logrus.Fields{"channel_id": event.Channel, "route": mentionCommandPrefix}).Warn("denied alert-proxy Slack mention outside the configured channel")
			return true, nil
		}
		if event.BotID != "" || event.User == "" {
			observeResult(observe, "invalid")
			logger.WithField("bot_id", event.BotID).Warn("ignored alert-proxy mention without a human user")
			return true, nil
		}

		eventID := eventID(callback)
		if eventID == "" {
			observeResult(observe, "invalid")
			return true, fmt.Errorf("alert-proxy mention is missing its Slack event ID")
		}
		threadTS := event.ThreadTimeStamp
		if threadTS == "" {
			threadTS = event.TimeStamp
		}
		issuedAt := r.now().Unix()
		body, err := json.Marshal(MentionEnvelope{
			EventID: eventID, IssuedAt: issuedAt, Channel: event.Channel,
			User: event.User, ThreadTS: threadTS, Text: text,
		})
		if err != nil {
			observeResult(observe, "error")
			return true, fmt.Errorf("marshal alert-proxy mention envelope: %w", err)
		}
		headers := make(http.Header)
		headers.Set("Content-Type", "application/json")
		response, err := r.relay(context.Background(), target, headers, body, issuedAt)
		if err != nil {
			observeResult(observe, "error")
			return true, err
		}
		if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
			observeResult(observe, "error")
			return true, fmt.Errorf("alert-proxy mention endpoint returned HTTP %d", response.StatusCode)
		}
		observeResult(observe, "success")
		return true, nil
	})
}

func commandTextFromMention(text string) (string, string, bool) {
	tokens := strings.Fields(text)
	if len(tokens) < 2 || !strings.HasPrefix(tokens[0], "<@") || !strings.HasSuffix(tokens[0], ">") || tokens[1] != mentionCommandPrefix {
		return "", "", false
	}
	mentionedUserID := strings.TrimSuffix(strings.TrimPrefix(tokens[0], "<@"), ">")
	if mentionedUserID == "" || strings.ContainsAny(mentionedUserID, "<>|") {
		return "", "", false
	}
	return mentionedUserID, strings.Join(tokens[2:], " "), true
}

func authenticatedUser(callback *slackevents.EventsAPIEvent, userID string) bool {
	outer, ok := callback.Data.(*slackevents.EventsAPICallbackEvent)
	if !ok {
		return false
	}
	for _, authenticatedUserID := range outer.AuthedUsers {
		if authenticatedUserID == userID {
			return true
		}
	}
	return false
}

func eventID(callback *slackevents.EventsAPIEvent) string {
	outer, ok := callback.Data.(*slackevents.EventsAPICallbackEvent)
	if !ok {
		return ""
	}
	return outer.EventID
}

func observeResult(observe func(string), result string) {
	if observe != nil {
		observe(result)
	}
}

func redactedRelayError(operation string, err error) error {
	switch {
	case errors.Is(err, context.Canceled):
		return fmt.Errorf("%s: %w", operation, context.Canceled)
	case errors.Is(err, context.DeadlineExceeded):
		return fmt.Errorf("%s: %w", operation, context.DeadlineExceeded)
	default:
		return fmt.Errorf("%s failed", operation)
	}
}

func (r *Relay) relay(ctx context.Context, target string, headers http.Header, body []byte, issuedAt int64) (*Response, error) {
	if r == nil || r.secret == nil {
		return nil, fmt.Errorf("alert-proxy relay secret is not configured")
	}
	secret := r.secret()
	if len(secret) == 0 {
		return nil, fmt.Errorf("alert-proxy relay secret is empty")
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return nil, redactedRelayError("create alert-proxy relay request", err)
	}
	request.Header = headers.Clone()
	if request.Header == nil {
		request.Header = make(http.Header)
	}
	timestamp := strconv.FormatInt(issuedAt, 10)
	request.Header.Set(TimestampHeader, timestamp)
	request.Header.Set(SignatureHeader, sign(timestamp, body, secret))

	response, err := r.client.Do(request)
	if err != nil {
		return nil, redactedRelayError("forward request to alert-proxy", err)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return nil, redactedRelayError("read alert-proxy response", err)
	}
	if len(responseBody) > maxResponseBytes {
		return nil, fmt.Errorf("alert-proxy response exceeds %d bytes", maxResponseBytes)
	}
	return &Response{StatusCode: response.StatusCode, Header: response.Header.Clone(), Body: responseBody}, nil
}

func sign(timestamp string, body, secret []byte) string {
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(signatureVersion + ":" + timestamp + ":"))
	_, _ = mac.Write(body)
	return signatureVersion + "=" + hex.EncodeToString(mac.Sum(nil))
}
