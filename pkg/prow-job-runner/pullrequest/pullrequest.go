// Package pullrequest resolves a GitHub pull request or Prow job into pinned checkout refs.
package pullrequest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"time"

	"github.com/sirupsen/logrus"

	prowapi "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	"sigs.k8s.io/prow/pkg/github"
	"sigs.k8s.io/prow/pkg/pod-utils/downwardapi"
)

// githubTimeout bounds an anonymous pull request lookup.
const githubTimeout = 30 * time.Second

// prPattern matches a GitHub repository and a positive pull request number.
var prPattern = regexp.MustCompile(`^([A-Za-z0-9-]+/[A-Za-z0-9_.-]+)#([1-9][0-9]*)$`)

// Reader resolves PR inputs. Its zero value uses an anonymous HTTP client with a 30-second timeout.
type Reader struct {
	Client *http.Client
}

// fetch reads ref anonymously and returns an error if GitHub denies the request.
func (r Reader) fetch(ctx context.Context, ref string) ([]byte, error) {
	m := prPattern.FindStringSubmatch(ref)
	if m == nil {
		return nil, fmt.Errorf("invalid --pull-request %q: expected <org>/<repo>#<number>", ref)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("https://api.github.com/repos/%s/pulls/%s", m[1], m[2]), nil)
	if err != nil {
		return nil, fmt.Errorf("building request for %s: %w", ref, err)
	}
	logrus.WithField("pull_request", ref).Info("Reading pull request from GitHub")
	client := r.Client
	if client == nil {
		client = &http.Client{Timeout: githubTimeout}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("getting %s: %w", ref, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("getting %s: %s", ref, resp.Status)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response for %s: %w", ref, err)
	}
	return data, nil
}

// Read returns validated base and pull refs from GitHub, a saved API response, or JOB_SPEC.
// The GitHub reference and filename are mutually exclusive; omitting both selects JOB_SPEC.
func (r Reader) Read(ctx context.Context, ref, filename string) (*prowapi.Refs, error) {
	refs, err := r.readSource(ctx, ref, filename)
	if err != nil {
		return nil, err
	}
	if refs == nil || refs.Org == "" || refs.Repo == "" || refs.BaseRef == "" || refs.BaseSHA == "" ||
		len(refs.Pulls) != 1 || refs.Pulls[0].Number < 1 || refs.Pulls[0].SHA == "" {
		return nil, errors.New("expected a base repository, branch and SHA, and one pull request with a number and head SHA")
	}
	pull := refs.Pulls[0]
	logrus.WithFields(logrus.Fields{
		"org": refs.Org, "repo": refs.Repo, "pr": pull.Number,
		"base_ref": refs.BaseRef, "base_sha": refs.BaseSHA, "head_sha": pull.SHA,
	}).Info("Resolved pull request")
	return refs, nil
}

// readSource selects exactly one of the GitHub API, a saved response, and the current Prow job.
func (r Reader) readSource(ctx context.Context, ref, filename string) (*prowapi.Refs, error) {
	switch {
	case ref != "" && filename != "":
		return nil, errors.New("--pull-request and --pull-request-data are mutually exclusive")
	case ref != "":
		data, err := r.fetch(ctx, ref)
		if err != nil {
			return nil, err
		}
		return decode(data)
	case filename != "":
		logrus.WithField("path", filename).Info("Reading pull request from file")
		data, err := os.ReadFile(filename)
		if err != nil {
			return nil, fmt.Errorf("reading saved GitHub response: %w", err)
		}
		return decode(data)
	default:
		logrus.Info("Reading pull request from JOB_SPEC")
		spec, err := downwardapi.ResolveSpecFromEnv()
		if err != nil {
			return nil, fmt.Errorf("provide --pull-request or --pull-request-data outside a Prow presubmit: %w", err)
		}
		return spec.Refs, nil
	}
}

// decode converts a GitHub pull request response into pinned checkout refs.
func decode(data []byte) (*prowapi.Refs, error) {
	var pr github.PullRequest
	if err := json.Unmarshal(data, &pr); err != nil {
		return nil, fmt.Errorf("decoding GitHub pull request: %w", err)
	}
	return &prowapi.Refs{
		Org: pr.Base.Repo.Owner.Login, Repo: pr.Base.Repo.Name, RepoLink: pr.Base.Repo.HTMLURL,
		BaseRef: pr.Base.Ref, BaseSHA: pr.Base.SHA,
		Pulls: []prowapi.Pull{{Number: pr.Number, SHA: pr.Head.SHA, HeadRef: pr.Head.Ref,
			Author: pr.User.Login, AuthorLink: pr.User.HTMLURL, Title: pr.Title, Link: pr.HTMLURL}},
	}, nil
}
