package prcreation

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/sirupsen/logrus"

	"sigs.k8s.io/prow/pkg/flagutil"
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

// Finalize constructs the App-authenticated GitHub client using prow's
// flagutil.GitHubOptions, which handles secret management, JWT signing,
// and installation token acquisition. Must be called after Validate and
// only when Enabled returns true.
func (o *GitHubAppOptions) Finalize() error {
	if !o.Enabled() {
		return nil
	}

	// Initialize a GitHubOptions with proper defaults (endpoints, timeouts)
	// via a throwaway FlagSet, then configure it for App auth. This reuses
	// prow's built-in client construction rather than reimplementing it.
	ghOpts := &flagutil.GitHubOptions{}
	ghOpts.AddCustomizedFlags(
		flag.NewFlagSet("pr-app", flag.ContinueOnError),
		flagutil.DisableThrottlerOptions(),
	)
	ghOpts.AppID = o.AppID
	ghOpts.AppPrivateKeyPath = o.PrivateKeyPath

	client, err := ghOpts.GitHubClientWithLogFields(false, logrus.Fields{"client": "pr-github-app"})
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
// It uses org-aware API calls (CreatePullRequest, GetPullRequests, etc.)
// because prow's apps auth round tripper requires the org to resolve the
// correct installation token. The generic bumper.UpdatePullRequestWithLabels
// cannot be used here as it calls FindIssues (which passes an empty org).
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
			return fmt.Errorf("failed to add labels to PR #%d: %w", prNumber, err)
		}
	}

	return nil
}

// findExistingPR searches for an open PR matching the given head ref using
// the GitHub search API via FindIssuesWithOrg (org-aware, required for App auth).
func (o *GitHubAppOptions) findExistingPR(org, repo, head string) (int, error) {
	// Derive the branch name from head, which may be "owner:branch" or just "branch".
	headBranch := head
	if i := strings.Index(head, ":"); i != -1 && i+1 < len(head) {
		headBranch = head[i+1:]
	}
	query := fmt.Sprintf("is:open is:pr repo:%s/%s head:%s", org, repo, headBranch)
	issues, err := o.client.FindIssuesWithOrg(org, query, "updated", false)
	if err != nil {
		return 0, fmt.Errorf("failed to search for existing PR: %w", err)
	}
	if len(issues) == 0 {
		return 0, fmt.Errorf("no open PR found matching head %q", head)
	}
	return issues[0].Number, nil
}
