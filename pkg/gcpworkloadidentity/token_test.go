package gcpworkloadidentity

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google/externalaccount"
)

func TestTokenReplacement(t *testing.T) {
	for _, tc := range []struct {
		name     string
		previous []byte
	}{{"initial", nil}, {"refresh", []byte("old")}} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "access-token")
			if tc.previous != nil {
				if err := os.WriteFile(path, tc.previous, 0444); err != nil {
					t.Fatal(err)
				}
			}
			if err := writeToken(path, "fresh"); err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(path)
			if err != nil || string(got) != "fresh" {
				t.Fatalf("replacement failed: %v", err)
			}
		})
	}
}

type tokenSourceFunc func() (*oauth2.Token, error)

func (f tokenSourceFunc) Token() (*oauth2.Token, error) { return f() }

func TestRefreshOnce(t *testing.T) {
	providerErr := errors.New("mock private response body")
	for _, tc := range []struct {
		name, value         string
		lifetime            time.Duration
		sourceErr, tokenErr error
		invalid             bool
	}{
		{name: "source error", sourceErr: providerErr, invalid: true},
		{name: "exchange error", tokenErr: providerErr, invalid: true},
		{name: "empty", lifetime: time.Hour, invalid: true},
		{name: "expires soon", value: "test", lifetime: 30 * time.Second, invalid: true},
		{name: "valid", value: "test", lifetime: time.Hour},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "token")
			expiry := time.Now().Add(tc.lifetime)
			next, err := refreshOnce(func() (oauth2.TokenSource, error) {
				return tokenSourceFunc(func() (*oauth2.Token, error) {
					return &oauth2.Token{AccessToken: tc.value, Expiry: expiry}, tc.tokenErr
				}), tc.sourceErr
			}, path)
			if tc.invalid {
				if err == nil || next != 0 {
					t.Fatalf("invalid token result: %v, %v", next, err)
				}
				if tc.sourceErr != nil || tc.tokenErr != nil {
					if !errors.Is(err, providerErr) || strings.Contains(err.Error(), providerErr.Error()) {
						t.Fatal("provider error not safely wrapped")
					}
				}
				if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("unexpected token file: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if next < time.Until(expiry)*4/5 || next > tc.lifetime*4/5 {
				t.Fatalf("unexpected refresh time %s", next)
			}
			b, err := os.ReadFile(path)
			if err != nil || string(b) != tc.value {
				t.Fatal("missing published token", err)
			}
		})
	}
}

func TestRefreshLoop(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	err := refreshLoop(ctx, func(context.Context) (time.Duration, error) {
		calls++
		if calls == 1 {
			return 0, errors.New("temporary failure")
		}
		cancel()
		return time.Hour, nil
	}, 0)
	if calls != 2 || !errors.Is(err, context.Canceled) {
		t.Fatalf("calls=%d error=%v", calls, err)
	}
}

func TestSupplier(t *testing.T) {
	failure := errors.New("retrieve failed")
	for _, wantErr := range []error{nil, failure} {
		s := supplier{aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) {
			return aws.Credentials{AccessKeyID: "id", SecretAccessKey: "secret", SessionToken: "session"}, wantErr
		})}
		ctx := context.Background()
		opts := externalaccount.SupplierOptions{}
		region, err := s.AwsRegion(ctx, opts)
		if region != "us-east-1" || err != nil {
			t.Fatalf("region=%s error=%v", region, err)
		}
		got, err := s.AwsSecurityCredentials(ctx, opts)
		if !errors.Is(err, wantErr) {
			t.Fatal(err)
		}
		if wantErr == nil && (got == nil || *got != (externalaccount.AwsSecurityCredentials{AccessKeyID: "id", SecretAccessKey: "secret", SessionToken: "session"})) {
			t.Fatal("credentials did not match")
		}
	}
}
