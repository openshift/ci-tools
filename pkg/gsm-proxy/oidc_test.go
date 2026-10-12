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
	"fmt"
	"net/http"
	"net/http/httptest"
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

func TestDexSigningKeyCacheAndRotation(t *testing.T) {
	firstKey := newSigningKey(t)
	secondKey := newSigningKey(t)
	var activeKey atomic.Pointer[rsa.PrivateKey]
	activeKey.Store(firstKey)
	var fetches atomic.Int32
	var failFetch atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fetches.Add(1)
		if failFetch.Load() {
			http.Error(w, "UPSTREAM_BODY_SENTINEL", http.StatusServiceUnavailable)
			return
		}
		key := activeKey.Load()
		kid := "first"
		if key == secondKey {
			kid = "second"
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{
			Key: &key.PublicKey, KeyID: kid, Algorithm: string(jose.RS256), Use: "sig",
		}}}); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	var clock atomic.Int64
	clock.Store(time.Date(2026, time.October, 12, 12, 0, 0, 0, time.UTC).UnixNano())
	now := func() time.Time { return time.Unix(0, clock.Load()) }
	client := &http.Client{
		Timeout:   time.Second,
		Transport: &keyFetchTransport{base: http.DefaultTransport, limiter: rate.NewLimiter(rate.Every(time.Minute), 1), now: now},
	}
	keys := oidc.NewRemoteKeySet(oidc.ClientContext(context.Background(), client), server.URL)
	verifier := newDexTokenVerifier(keys, now)
	firstToken := signClaims(t, firstKey, "first", jose.RS256, testClaims(now()))
	if _, err := verifier.Verify(context.Background(), firstToken); err != nil {
		t.Fatal(err)
	}
	if _, err := verifier.Verify(context.Background(), firstToken); err != nil {
		t.Fatal(err)
	}
	if got := fetches.Load(); got != 1 {
		t.Fatalf("cached verification fetched keys %d times, want 1", got)
	}

	activeKey.Store(secondKey)
	secondToken := signClaims(t, secondKey, "second", jose.RS256, testClaims(now()))
	for i := range 10 {
		unknownToken := signClaims(t, secondKey, fmt.Sprintf("unknown-%d", i), jose.RS256, testClaims(now()))
		if _, err := verifier.Verify(context.Background(), unknownToken); !errors.Is(err, ErrUnauthenticated) {
			t.Fatalf("unknown kid during cooldown: %v", err)
		}
	}
	if _, err := verifier.Verify(context.Background(), secondToken); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("rotation during cooldown: %v", err)
	}
	badSignatureToken := signClaims(t, secondKey, "first", jose.RS256, testClaims(now()))
	if _, err := verifier.Verify(context.Background(), badSignatureToken); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("invalid signature with a cached kid: %v", err)
	}
	if got := fetches.Load(); got != 1 {
		t.Fatalf("unknown kid bypassed fetch limit: %d fetches", got)
	}
	if _, err := verifier.Verify(context.Background(), firstToken); err != nil {
		t.Fatalf("rate limit blocked cached key: %v", err)
	}

	clock.Add(int64(time.Minute + time.Second))
	failFetch.Store(true)
	if _, err := verifier.Verify(context.Background(), secondToken); !errors.Is(err, ErrUnauthenticated) || strings.Contains(err.Error(), "UPSTREAM_BODY_SENTINEL") {
		t.Fatalf("failed key fetch returned unsafe error: %v", err)
	}
	if got := fetches.Load(); got != 2 {
		t.Fatalf("fetch count = %d, want 2", got)
	}
	if _, err := verifier.Verify(context.Background(), firstToken); err != nil {
		t.Fatalf("upstream failure discarded cached key: %v", err)
	}

	clock.Add(int64(time.Minute + time.Second))
	failFetch.Store(false)
	if _, err := verifier.Verify(context.Background(), secondToken); err != nil {
		t.Fatalf("rotated key was not accepted after cooldown: %v", err)
	}
	if _, err := verifier.Verify(context.Background(), secondToken); err != nil {
		t.Fatal(err)
	}
	if got := fetches.Load(); got != 3 {
		t.Fatalf("fetch count = %d, want 3", got)
	}
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
