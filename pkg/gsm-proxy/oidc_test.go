package gsmproxy

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	jose "github.com/go-jose/go-jose/v4"
	"github.com/google/go-cmp/cmp"
	"golang.org/x/time/rate"
)

func TestDexTokenVerifier(t *testing.T) {
	key := newSigningKey(t)
	otherKey := newSigningKey(t)
	now := time.Date(2026, time.October, 12, 12, 0, 0, 0, time.UTC)
	verifier := newDexTokenVerifier(&oidc.StaticKeySet{PublicKeys: []crypto.PublicKey{&key.PublicKey}}, func() time.Time { return now })

	testCases := []struct {
		name   string
		change func(map[string]any)
		token  func(map[string]any) string
		want   *Identity
	}{
		{name: "valid", want: &Identity{Username: "test-user", Groups: []string{"example-owner"}}},
		{name: "single audience in a list", change: func(c map[string]any) { c["aud"] = []string{"gsm-cli"} }, want: &Identity{Username: "test-user", Groups: []string{"example-owner"}}},
		{name: "empty group list is valid but grants no memberships", change: func(c map[string]any) { c["groups"] = []string{} }, want: &Identity{Username: "test-user", Groups: []string{}}},
		{name: "just under maximum age", change: func(c map[string]any) { c["iat"] = now.Add(-time.Hour + time.Second).Unix() }, want: &Identity{Username: "test-user", Groups: []string{"example-owner"}}},
		{name: "maximum age with unexpired Dex token", change: func(c map[string]any) {
			c["iat"] = now.Add(-time.Hour).Unix()
			c["exp"] = now.Add(23 * time.Hour).Unix()
		}},
		{name: "over maximum age with unexpired Dex token", change: func(c map[string]any) {
			c["iat"] = now.Add(-2 * time.Hour).Unix()
			c["exp"] = now.Add(22 * time.Hour).Unix()
		}},
		{name: "future issuance within clock skew", change: func(c map[string]any) { c["iat"] = now.Add(time.Minute).Unix() }, want: &Identity{Username: "test-user", Groups: []string{"example-owner"}}},
		{name: "future issuance beyond clock skew", change: func(c map[string]any) { c["iat"] = now.Add(time.Minute + time.Second).Unix() }},
		{name: "missing issuance", change: func(c map[string]any) { delete(c, "iat") }},
		{name: "null issuance", change: func(c map[string]any) { c["iat"] = nil }},
		{name: "malformed issuance", change: func(c map[string]any) { c["iat"] = "invalid" }},
		{name: "expired", change: func(c map[string]any) { c["exp"] = now.Add(-time.Second).Unix() }},
		{name: "expires now", change: func(c map[string]any) { c["exp"] = now.Unix() }},
		{name: "missing expiration", change: func(c map[string]any) { delete(c, "exp") }},
		{name: "wrong issuer", change: func(c map[string]any) { c["iss"] = "https://other.example" }},
		{name: "missing issuer", change: func(c map[string]any) { delete(c, "iss") }},
		{name: "Vault audience", change: func(c map[string]any) { c["aud"] = "vault" }},
		{name: "audience prefix is not sufficient", change: func(c map[string]any) { c["aud"] = "gsm-cli-other" }},
		{name: "missing audience", change: func(c map[string]any) { delete(c, "aud") }},
		{name: "multiple audiences", change: func(c map[string]any) { c["aud"] = []string{"gsm-cli", "vault"} }},
		{name: "missing groups", change: func(c map[string]any) { delete(c, "groups") }},
		{name: "null groups", change: func(c map[string]any) { c["groups"] = nil }},
		{name: "groups is a string", change: func(c map[string]any) { c["groups"] = "example-owner" }},
		{name: "group is numeric", change: func(c map[string]any) { c["groups"] = []any{123} }},
		{name: "group is null", change: func(c map[string]any) { c["groups"] = []any{nil} }},
		{name: "group is empty", change: func(c map[string]any) { c["groups"] = []string{""} }},
		{name: "group has control characters", change: func(c map[string]any) { c["groups"] = []string{"example\nowner"} }},
		{name: "missing username", change: func(c map[string]any) { delete(c, "preferred_username") }},
		{name: "null username", change: func(c map[string]any) { c["preferred_username"] = nil }},
		{name: "username is numeric", change: func(c map[string]any) { c["preferred_username"] = 123 }},
		{name: "empty username", change: func(c map[string]any) { c["preferred_username"] = "" }},
		{name: "username has whitespace", change: func(c map[string]any) { c["preferred_username"] = "test user" }},
		{name: "username has control characters", change: func(c map[string]any) { c["preferred_username"] = "test\x00user" }},
		{name: "not yet valid", change: func(c map[string]any) { c["nbf"] = now.Add(time.Hour).Unix() }},
		{name: "signature from an untrusted key", token: func(c map[string]any) string { return signClaims(t, otherKey, "other-key", jose.RS256, c) }},
		{name: "RS512 is not allowed", token: func(c map[string]any) string { return signClaims(t, key, "test-key", jose.RS512, c) }},
		{name: "unsigned token", token: func(c map[string]any) string {
			return base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`)) + "." + encodedClaims(t, c) + "."
		}},
		{name: "HMAC with public key as secret", token: func(c map[string]any) string {
			der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
			if err != nil {
				t.Fatal(err)
			}
			secret := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})
			return signClaims(t, secret, "test-key", jose.HS256, c)
		}},
		{name: "attacker adds an owning group without resigning", token: func(c map[string]any) string {
			token := signClaims(t, key, "test-key", jose.RS256, c)
			parts := strings.Split(token, ".")
			c["groups"] = []string{"different-owner"}
			parts[1] = encodedClaims(t, c)
			return strings.Join(parts, ".")
		}},
		{name: "malformed token", token: func(_ map[string]any) string { return "TOKEN_SENTINEL" }},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			claims := testClaims(now)
			if tc.change != nil {
				tc.change(claims)
			}
			var token string
			if tc.token != nil {
				token = tc.token(claims)
			} else {
				token = signClaims(t, key, "test-key", jose.RS256, claims)
			}
			got, err := verifier.Verify(context.Background(), token)
			if tc.want == nil {
				if !errors.Is(err, ErrUnauthenticated) {
					t.Fatalf("error = %v, want ErrUnauthenticated", err)
				}
				if diff := cmp.Diff(Identity{}, got); diff != "" {
					t.Fatalf("rejection returned identity (-want +got): %s", diff)
				}
				if strings.Contains(err.Error(), token) {
					t.Fatal("error exposed the raw token")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(*tc.want, got); diff != "" {
				t.Fatalf("identity mismatch (-want +got): %s", diff)
			}
		})
	}
}

func TestKeyFetchTransportRateLimit(t *testing.T) {
	base := &countingRoundTripper{}
	clock := &fakeClock{}
	clock.nanos.Store(time.Date(2026, time.October, 12, 12, 0, 0, 0, time.UTC).UnixNano())
	transport := &keyFetchTransport{
		base:    base,
		limiter: rate.NewLimiter(rate.Every(time.Minute), 1),
		now:     clock.now,
	}
	request, err := http.NewRequest(http.MethodGet, "https://idp.ci.openshift.org/keys", nil)
	if err != nil {
		t.Fatal(err)
	}
	testCases := []struct {
		name    string
		advance time.Duration
		wantErr string
	}{
		{name: "first fetch is allowed"},
		{name: "second fetch inside the window is rejected", wantErr: "dex signing-key fetch is rate limited"},
		{name: "fetch after the window is allowed", advance: time.Minute + time.Second},
	}
	var calls int32
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			checkKeyFetch(t, transport, base, clock, request, &calls, tc.advance, tc.wantErr)
		})
	}
}

func checkKeyFetch(t *testing.T, transport *keyFetchTransport, base *countingRoundTripper, clock *fakeClock, request *http.Request, calls *int32, advance time.Duration, wantErr string) {
	t.Helper()
	if advance > 0 {
		clock.nanos.Add(int64(advance))
	}
	resp, err := transport.RoundTrip(request)
	if wantErr != "" {
		if err == nil || err.Error() != wantErr {
			t.Fatalf("error = %v, want %q", err, wantErr)
		}
		if resp != nil {
			t.Fatal("rate limited fetch returned a response")
		}
		if got := base.calls.Load(); got != *calls {
			t.Fatalf("upstream calls = %d, want %d", got, *calls)
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	if err := resp.Body.Close(); err != nil {
		t.Fatal(err)
	}
	*calls++
	if got := base.calls.Load(); got != *calls {
		t.Fatalf("upstream calls = %d, want %d", got, *calls)
	}
}

// countingRoundTripper records signing-key fetches that passed the rate limit.
type countingRoundTripper struct {
	calls atomic.Int32
}

func (c *countingRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	c.calls.Add(1)
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("{}")), Header: make(http.Header)}, nil
}

// fakeClock is the time source for the signing-key fetch limiter.
type fakeClock struct {
	nanos atomic.Int64
}

func (c *fakeClock) now() time.Time {
	return time.Unix(0, c.nanos.Load())
}

func newSigningKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func testClaims(now time.Time) map[string]any {
	return map[string]any{
		"iss": dexIssuerURL, "aud": dexClientID, "sub": "test-subject",
		"iat": now.Unix(), "exp": now.Add(time.Hour).Unix(),
		"preferred_username": "test-user", "groups": []string{"example-owner"},
	}
}

func signClaims(t *testing.T, key any, kid string, algorithm jose.SignatureAlgorithm, claims map[string]any) string {
	t.Helper()
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: algorithm, Key: key}, (&jose.SignerOptions{}).WithHeader("kid", kid))
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	signed, err := signer.Sign(payload)
	if err != nil {
		t.Fatal(err)
	}
	token, err := signed.CompactSerialize()
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func encodedClaims(t *testing.T, claims map[string]any) string {
	t.Helper()
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	return base64.RawURLEncoding.EncodeToString(payload)
}
