// Package gcpworkloadidentity obtains Google access tokens for a leased profile.
package gcpworkloadidentity

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/sirupsen/logrus"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google/externalaccount"
)

// TokenFile is the gcloud access-token file mounted read-only in the step.
const TokenFile = "/var/run/secrets/gcp-wif/access-token"

// IdentityFile is the projected AWS-audience token mounted only in the helper.
const IdentityFile = "/var/run/secrets/aws/sts-token/token"

type supplier struct{ aws.CredentialsProvider }

func (s supplier) AwsRegion(context.Context, externalaccount.SupplierOptions) (string, error) {
	return "us-east-1", nil
}
func (s supplier) AwsSecurityCredentials(ctx context.Context, _ externalaccount.SupplierOptions) (*externalaccount.AwsSecurityCredentials, error) {
	creds, err := s.Retrieve(ctx)
	if err != nil {
		return nil, fmt.Errorf("retrieve AWS hub credentials: %w", err)
	}
	return &externalaccount.AwsSecurityCredentials{AccessKeyID: creds.AccessKeyID, SecretAccessKey: creds.SecretAccessKey, SessionToken: creds.SessionToken}, nil
}

// Refresh exchanges the pod identity through the AWS home and hub roles and
// atomically replaces the target Google account's access token before expiry.
func Refresh(ctx context.Context, home, hub, provider, account string) error {
	cfg := aws.Config{Region: "us-east-1", Credentials: aws.AnonymousCredentials{}}
	cfg.Credentials = aws.NewCredentialsCache(stscreds.NewWebIdentityRoleProvider(sts.NewFromConfig(cfg), home, stscreds.IdentityTokenFile(IdentityFile)))
	src := aws.NewCredentialsCache(stscreds.NewAssumeRoleProvider(sts.NewFromConfig(cfg), hub))
	conf := externalaccount.Config{
		Audience:                       "//iam.googleapis.com/" + provider,
		SubjectTokenType:               "urn:ietf:params:aws:token-type:aws4_request",
		Scopes:                         []string{"https://www.googleapis.com/auth/cloud-platform"},
		TokenURL:                       "https://sts.googleapis.com/v1/token",
		ServiceAccountImpersonationURL: "https://iamcredentials.googleapis.com/v1/projects/-/serviceAccounts/" + url.PathEscape(account) + ":generateAccessToken",
		AwsSecurityCredentialsSupplier: supplier{src},
	}
	return refreshLoop(ctx, func(ctx context.Context) (time.Duration, error) {
		return refreshOnce(func() (oauth2.TokenSource, error) { return externalaccount.NewTokenSource(ctx, conf) }, TokenFile)
	}, 15*time.Second)
}

func refreshLoop(ctx context.Context, refresh func(context.Context) (time.Duration, error), retryDelay time.Duration) error {
	for ctx.Err() == nil {
		// Start a fresh Google exchange before expiry while retaining the AWS cache.
		requestCtx, cancel := context.WithTimeout(ctx, time.Minute)
		next, err := refresh(requestCtx)
		cancel()
		if err != nil {
			logrus.WithError(err).Warn("Google credential refresh failed; retrying")
			next = retryDelay
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(next):
		}
	}
	return ctx.Err()
}

// Provider errors can embed raw HTTP bodies. Preserve the cause for errors.Is/As
// while exposing only the operation name to logs.
type providerError struct{ cause error }

func (e providerError) Error() string { return "credential provider failed" }
func (e providerError) Unwrap() error { return e.cause }

func refreshOnce(newSource func() (oauth2.TokenSource, error), path string) (time.Duration, error) {
	src, err := newSource()
	if err != nil {
		return 0, fmt.Errorf("create Google token source: %w", providerError{err})
	}
	tok, err := src.Token()
	if err != nil {
		return 0, fmt.Errorf("obtain Google access token: %w", providerError{err})
	}
	if tok == nil || tok.AccessToken == "" || time.Until(tok.Expiry) < time.Minute {
		return 0, fmt.Errorf("received an empty or expiring Google token")
	}
	if err := writeToken(path, tok.AccessToken); err != nil {
		return 0, fmt.Errorf("publish Google access token: %w", err)
	}
	return time.Until(tok.Expiry) * 4 / 5, nil
}

func writeToken(path, token string) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".token-*")
	if err != nil {
		return fmt.Errorf("create temporary token file: %w", err)
	}
	// A successful rename moves the file. Cleanup after a failure is best effort.
	defer func() { _ = os.Remove(f.Name()) }()
	if _, err = f.WriteString(token); err != nil {
		err = fmt.Errorf("write temporary token file: %w", err)
	} else if err = f.Chmod(0444); err != nil {
		err = fmt.Errorf("set token file permissions: %w", err)
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return fmt.Errorf("close temporary token file: %w", closeErr)
	}
	if err := os.Rename(f.Name(), path); err != nil {
		return fmt.Errorf("replace token file: %w", err)
	}
	return nil
}
