package prcreation

import (
	"crypto/rsa"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/dgrijalva/jwt-go/v4"
	"github.com/sirupsen/logrus"

	"sigs.k8s.io/prow/pkg/config/secret"
	"sigs.k8s.io/prow/pkg/github"
)

// GitHubAppOptions holds configuration for a secondary GitHub App client
// used exclusively for PR creation. Flag names are prefixed with "pr-" to
// avoid collision with prow's built-in --github-app-id / --github-app-private-key-path.
type GitHubAppOptions struct {
	AppID          string
	PrivateKeyPath string

	// client is lazily constructed via Finalize.
	client github.Client
}

// AddFlags registers GitHub App flags on the given flag set.
func (o *GitHubAppOptions) AddFlags(fs *flag.FlagSet) {
	fs.StringVar(&o.AppID, "pr-app-id", "", "GitHub App ID for PR creation (requires --pr-app-private-key-path)")
	fs.StringVar(&o.PrivateKeyPath, "pr-app-private-key-path", "", "Path to the GitHub App private key PEM for PR creation (requires --pr-app-id)")
}

// Enabled returns true when both App fields are configured.
func (o *GitHubAppOptions) Enabled() bool {
	return o.AppID != "" && o.PrivateKeyPath != ""
}

// Validate checks both-or-neither consistency and that the key file exists.
func (o *GitHubAppOptions) Validate() error {
	if o.AppID == "" && o.PrivateKeyPath == "" {
		return nil
	}
	if o.AppID == "" || o.PrivateKeyPath == "" {
		return fmt.Errorf("--pr-app-id and --pr-app-private-key-path must both be set or both be empty")
	}
	if _, err := os.Stat(o.PrivateKeyPath); err != nil {
		return fmt.Errorf("cannot access private key file %s: %w", o.PrivateKeyPath, err)
	}
	return nil
}

// Finalize constructs the App-authenticated GitHub client. Must be called
// after Validate and only when Enabled returns true.
func (o *GitHubAppOptions) Finalize() error {
	if !o.Enabled() {
		return nil
	}

	keyGenerator, err := secret.AddWithParser(
		o.PrivateKeyPath,
		func(raw []byte) (*rsa.PrivateKey, error) {
			key, err := jwt.ParseRSAPrivateKeyFromPEM(raw)
			if err != nil {
				return nil, fmt.Errorf("failed to parse RSA private key from PEM: %w", err)
			}
			return key, nil
		},
	)
	if err != nil {
		return fmt.Errorf("failed to load GitHub App private key: %w", err)
	}

	opts := github.ClientOptions{
		Censor:        secret.Censor,
		AppID:         o.AppID,
		AppPrivateKey: keyGenerator,
		Bases:         []string{github.DefaultAPIEndpoint},
	}

	_, _, client, err := github.NewClientFromOptions(logrus.Fields{
		"client": "pr-github-app",
	}, opts)
	if err != nil {
		return fmt.Errorf("failed to construct GitHub App client: %w", err)
	}

	o.client = client
	return nil
}

// Client returns the App-authenticated GitHub client.
func (o *GitHubAppOptions) Client() github.Client {
	return o.client
}

// UpsertPR creates or updates a pull request using the App client.
// It sets canModify=false to avoid fork_collab errors on cross-fork PRs.
func (o *GitHubAppOptions) UpsertPR(org, repo, base, head, title, body string, prLabels []string) error {
	l := logrus.WithFields(logrus.Fields{"org": org, "repo": repo, "head": head})

	prNumber, err := o.client.CreatePullRequest(org, repo, title, body, head, base, false)
	if err != nil {
		// GitHub returns 422 with "already exists" when a PR for this head already exists.
		if !strings.Contains(err.Error(), "already exists") {
			return fmt.Errorf("failed to create PR: %w", err)
		}
		l.Info("PR already exists, updating existing PR")
		prNumber, err = o.findExistingPR(org, repo, head)
		if err != nil {
			return fmt.Errorf("failed to find existing PR: %w", err)
		}
		if err := o.client.UpdatePullRequest(org, repo, prNumber, &title, &body, nil, nil, nil); err != nil {
			return fmt.Errorf("failed to update PR #%d: %w", prNumber, err)
		}
	}

	l.WithField("number", prNumber).Info("PR created/updated via GitHub App")

	if len(prLabels) > 0 {
		if err := o.client.AddLabels(org, repo, prNumber, prLabels...); err != nil {
			l.WithError(err).Warn("Failed to add labels to PR — continuing anyway")
		}
	}

	return nil
}

// findExistingPR lists open PRs and finds one matching the given head ref.
// head is in "owner:branch" format; we match against both the raw Ref and
// the fully qualified "repo-owner:ref" form.
func (o *GitHubAppOptions) findExistingPR(org, repo, head string) (int, error) {
	prs, err := o.client.GetPullRequests(org, repo)
	if err != nil {
		return 0, fmt.Errorf("failed to list PRs: %w", err)
	}
	for _, pr := range prs {
		qualifiedRef := pr.Head.Repo.Owner.Login + ":" + pr.Head.Ref
		if pr.Head.Ref == head || qualifiedRef == head {
			return pr.Number, nil
		}
	}
	return 0, fmt.Errorf("no open PR found matching head %q", head)
}
