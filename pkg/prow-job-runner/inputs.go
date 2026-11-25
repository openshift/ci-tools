package prowjobrunner

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

// prPattern matches a GitHub repository and a positive pull request number.
var prPattern = regexp.MustCompile(`^([A-Za-z0-9-]+/[A-Za-z0-9_.-]+)#([1-9][0-9]*)$`)

// fetchPR reads ref anonymously and returns an error if GitHub denies the request.
func fetchPR(ctx context.Context, client *http.Client, ref string) ([]byte, error) {
	m := prPattern.FindStringSubmatch(ref)
	if m == nil {
		return nil, fmt.Errorf("invalid --pull-request %q: expected <org>/<repo>#<number>", ref)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("https://api.github.com/repos/%s/pulls/%s", m[1], m[2]), nil)
	if err != nil {
		return nil, fmt.Errorf("building request for %s: %w", ref, err)
	}
	logrus.WithField("pull_request", ref).Info("Reading pull request from GitHub")
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

// readRefs returns the pinned base and pull refs from GitHub, a saved API response, or the current Prow job.
func readRefs(ctx context.Context, ref, filename string) (*prowapi.Refs, error) {
	var refs *prowapi.Refs
	if ref == "" && filename == "" {
		logrus.Info("Reading pull request from JOB_SPEC")
		spec, err := downwardapi.ResolveSpecFromEnv()
		if err != nil {
			return nil, fmt.Errorf("provide --pull-request or --pull-request-data outside a Prow presubmit: %w", err)
		}
		refs = spec.Refs
	} else {
		var data []byte
		var err error
		if filename != "" {
			logrus.WithField("path", filename).Info("Reading pull request from file")
			data, err = os.ReadFile(filename)
			if err != nil {
				return nil, fmt.Errorf("reading saved GitHub response: %w", err)
			}
		} else {
			data, err = fetchPR(ctx, &http.Client{Timeout: 30 * time.Second}, ref)
			if err != nil {
				return nil, err
			}
		}
		var pr github.PullRequest
		if err := json.Unmarshal(data, &pr); err != nil {
			return nil, fmt.Errorf("decoding GitHub pull request: %w", err)
		}
		refs = &prowapi.Refs{
			Org: pr.Base.Repo.Owner.Login, Repo: pr.Base.Repo.Name, RepoLink: pr.Base.Repo.HTMLURL,
			BaseRef: pr.Base.Ref, BaseSHA: pr.Base.SHA,
			Pulls: []prowapi.Pull{{Number: pr.Number, SHA: pr.Head.SHA, HeadRef: pr.Head.Ref,
				Author: pr.User.Login, AuthorLink: pr.User.HTMLURL, Title: pr.Title, Link: pr.HTMLURL}},
		}
	}
	if refs == nil || refs.Org == "" || refs.Repo == "" || refs.BaseRef == "" || refs.BaseSHA == "" ||
		len(refs.Pulls) != 1 || refs.Pulls[0].Number < 1 || refs.Pulls[0].SHA == "" {
		return nil, errors.New("expected a base repository, branch and SHA, and one pull request with a number and head SHA")
	}
	return refs, nil
}
