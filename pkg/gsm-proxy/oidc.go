package gsmproxy

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/time/rate"
)

const (
	dexIssuerURL = "https://idp.ci.openshift.org"
	dexClientID  = "gsm-cli"
)

// DexTokenVerifier accepts only ID tokens issued by Dex for gsm-cli.
type DexTokenVerifier struct {
	verifier *oidc.IDTokenVerifier
	now      func() time.Time
}

var _ TokenVerifier = (*DexTokenVerifier)(nil)

func NewDexTokenVerifier() *DexTokenVerifier {
	client := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &keyFetchTransport{
			base:    http.DefaultTransport,
			limiter: rate.NewLimiter(rate.Every(time.Minute), 1),
			now:     time.Now,
		},
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	ctx := oidc.ClientContext(context.Background(), client)
	keys := oidc.NewRemoteKeySet(ctx, dexIssuerURL+"/keys")
	return newDexTokenVerifier(keys, time.Now)
}

func newDexTokenVerifier(keys oidc.KeySet, now func() time.Time) *DexTokenVerifier {
	return &DexTokenVerifier{
		verifier: oidc.NewVerifier(dexIssuerURL, keys, &oidc.Config{
			ClientID:             dexClientID,
			SupportedSigningAlgs: []string{oidc.RS256},
			Now:                  now,
		}),
		now: now,
	}
}

func (v *DexTokenVerifier) Verify(ctx context.Context, rawToken string) (Identity, error) {
	token, err := v.verifier.Verify(ctx, rawToken)
	if err != nil {
		// Library errors can contain attacker-controlled claims or upstream bodies.
		return Identity{}, fmt.Errorf("dex token verification failed: %w", ErrUnauthenticated)
	}

	// This API deliberately accepts only tokens dedicated to gsm-cli.
	if token.Issuer != dexIssuerURL || len(token.Audience) != 1 || token.Audience[0] != dexClientID || !token.Expiry.After(v.now()) {
		return Identity{}, fmt.Errorf("dex token claims are invalid: %w", ErrUnauthenticated)
	}

	var claims struct {
		Username string   `json:"preferred_username"`
		Groups   []string `json:"groups"`
	}
	if err := token.Claims(&claims); err != nil {
		return Identity{}, fmt.Errorf("dex identity claims are malformed: %w", ErrUnauthenticated)
	}
	if claims.Username == "" || strings.ContainsFunc(claims.Username, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r)
	}) || claims.Groups == nil {
		return Identity{}, fmt.Errorf("dex identity claims are missing or invalid: %w", ErrUnauthenticated)
	}
	for _, group := range claims.Groups {
		if strings.TrimSpace(group) == "" || strings.ContainsFunc(group, unicode.IsControl) {
			return Identity{}, fmt.Errorf("dex group claim is invalid: %w", ErrUnauthenticated)
		}
	}
	return Identity{Username: claims.Username, Groups: claims.Groups}, nil
}

type keyFetchTransport struct {
	base    http.RoundTripper
	limiter *rate.Limiter
	now     func() time.Time
}

func (t *keyFetchTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if !t.limiter.AllowN(t.now(), 1) {
		return nil, fmt.Errorf("dex signing-key fetch is rate limited")
	}
	return t.base.RoundTrip(request)
}
