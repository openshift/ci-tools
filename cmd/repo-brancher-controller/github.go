package main

import (
	"context"
	"fmt"

	"sigs.k8s.io/prow/pkg/github"
)

type prowRefClient interface {
	GetRefWithContext(context.Context, string, string, string) (string, error)
	CreateRefWithContext(context.Context, string, string, string, string) error
	UpdateRefWithContext(context.Context, string, string, string, string, bool) error
}

// githubRefClient adapts Prow's shared GitHub client to the controller's
// deliberately small refClient interface. Authentication, request execution,
// throttling, and retries remain owned by Prow.
type githubRefClient struct {
	client prowRefClient
	health *runtimeHealth
}

func newGitHubRefClient(client prowRefClient, health *runtimeHealth) *githubRefClient {
	return &githubRefClient{client: client, health: health}
}

func (c *githubRefClient) succeeded() {
	if c.health != nil {
		c.health.githubSucceeded()
	}
}

func (c *githubRefClient) getRef(ctx context.Context, org, repo, branch string) (string, bool, error) {
	sha, err := c.client.GetRefWithContext(ctx, org, repo, "heads/"+branch)
	if github.IsNotFound(err) {
		c.succeeded()
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	c.succeeded()
	return sha, true, nil
}

func (c *githubRefClient) createRef(ctx context.Context, org, repo, branch, sha string) error {
	err := c.client.CreateRefWithContext(ctx, org, repo, "refs/heads/"+branch, sha)
	if err == nil {
		c.succeeded()
		return nil
	}
	if !github.IsUnprocessableEntity(err) {
		return err
	}

	// A concurrent reconciliation may have created the target after it was
	// observed as missing. Re-read it and apply the same non-forced update used
	// for an existing target.
	currentSHA, exists, getErr := c.getRef(ctx, org, repo, branch)
	if getErr != nil {
		return fmt.Errorf("re-read ref after create conflict: %w", getErr)
	}
	if !exists {
		return permanentError{fmt.Errorf("GitHub rejected ref creation: %w", err)}
	}
	if currentSHA == sha {
		return nil
	}
	return c.updateRef(ctx, org, repo, branch, sha)
}

func (c *githubRefClient) updateRef(ctx context.Context, org, repo, branch, sha string) error {
	err := c.client.UpdateRefWithContext(ctx, org, repo, "heads/"+branch, sha, false)
	if err == nil {
		c.succeeded()
		return nil
	}
	if github.IsUnprocessableEntity(err) {
		c.succeeded()
		return permanentError{fmt.Errorf("GitHub rejected non-forced ref update: %w", err)}
	}
	return err
}
