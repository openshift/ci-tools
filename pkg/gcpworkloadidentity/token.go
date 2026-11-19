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
	for ctx.Err() == nil {
		// Start a fresh Google exchange before expiry while retaining the AWS cache.
		requestCtx, cancel := context.WithTimeout(ctx, time.Minute)
		next, err := refreshOnce(requestCtx, conf)
		cancel()
		if err != nil {
			// Upstream response bodies can contain authentication material.
			logrus.Warn("Google credential refresh failed; retrying")
			next = 15 * time.Second
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(next):
		}
	}
	return ctx.Err()
}

func refreshOnce(ctx context.Context, conf externalaccount.Config) (time.Duration, error) {
	src, err := externalaccount.NewTokenSource(ctx, conf)
	if err != nil {
		return 0, err
	}
	tok, err := src.Token()
	if err != nil {
		return 0, err
	}
	if tok.AccessToken == "" || time.Until(tok.Expiry) < time.Minute {
		return 0, fmt.Errorf("received an empty or expiring Google token")
	}
	if err := writeToken(TokenFile, tok.AccessToken); err != nil {
		return 0, err
	}
	return time.Until(tok.Expiry) * 4 / 5, nil
}

func writeToken(path, token string) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".token-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.WriteString(token); err == nil {
		err = f.Chmod(0444)
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), path)
}
