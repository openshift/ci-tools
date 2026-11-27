package pullrequest

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	prowapi "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
)

const (
	// prJSON is a saved GitHub response for org/repo#42.
	prJSON = `{"number":42,"user":{"login":"author"},"base":{"repo":{"owner":{"login":"org"},"name":"repo"},"ref":"main","sha":"base"},"head":{"ref":"topic","sha":"head"}}`
	// pull is the head revision referenced by the equivalent Prow jobSpec.
	pull    = `{"number":42,"sha":"head"}`
	jobSpec = `{"refs":{"org":"org","repo":"repo","base_ref":"main","base_sha":"base","pulls":[` + pull + `]}}`
	// invalidRefs is the diagnostic for a response without a complete set of pinned refs.
	invalidRefs = "expected a base repository, branch and SHA, and one pull request with a number and head SHA"
)

// pullTransport checks for anonymous requests and returns a canned GitHub response.
type pullTransport struct {
	status int
	calls  int
	body   string
}

// TestRead checks conversion of saved GitHub data and rejection of incomplete or ambiguous Prow refs.
func TestRead(t *testing.T) {
	for _, tc := range []struct {
		name     string
		file     bool
		ref      string
		filename string
		data     string
		spec     string
		want     *prowapi.Refs
		calls    int
		wantErr  string
	}{
		{name: "saved GitHub PR", file: true, want: &prowapi.Refs{Org: "org", Repo: "repo", BaseRef: "main", BaseSHA: "base", Pulls: []prowapi.Pull{{Number: 42, SHA: "head", Author: "author", HeadRef: "topic"}}}},
		{name: "anonymous GitHub PR takes precedence over JOB_SPEC", ref: "org/repo#42", spec: "invalid", calls: 1, want: &prowapi.Refs{Org: "org", Repo: "repo", BaseRef: "main", BaseSHA: "base", Pulls: []prowapi.Pull{{Number: 42, SHA: "head", Author: "author", HeadRef: "topic"}}}},
		{name: "current Prow job", spec: jobSpec, want: &prowapi.Refs{Org: "org", Repo: "repo", BaseRef: "main", BaseSHA: "base", Pulls: []prowapi.Pull{{Number: 42, SHA: "head"}}}},
		{name: "mutually exclusive inputs", file: true, ref: "org/repo#42", wantErr: "--pull-request and --pull-request-data are mutually exclusive"},
		{name: "missing input", wantErr: "provide --pull-request or --pull-request-data outside a Prow presubmit"},
		{name: "missing file", filename: "missing.json", wantErr: "reading saved GitHub response"},
		{name: "malformed response", file: true, data: "[", wantErr: "decoding GitHub pull request"},
		{name: "incomplete saved response", file: true, data: "{}", wantErr: invalidRefs},
		{name: "invalid GitHub reference", ref: "invalid", wantErr: "invalid --pull-request"},
		{name: "missing refs", spec: `{}`, wantErr: invalidRefs},
		{name: "missing org", spec: strings.Replace(jobSpec, `"org":"org",`, "", 1), wantErr: invalidRefs},
		{name: "missing repo", spec: strings.Replace(jobSpec, `"repo":"repo",`, "", 1), wantErr: invalidRefs},
		{name: "missing base ref", spec: strings.Replace(jobSpec, `"base_ref":"main",`, "", 1), wantErr: invalidRefs},
		{name: "missing base SHA", spec: strings.Replace(jobSpec, `"base_sha":"base",`, "", 1), wantErr: invalidRefs},
		{name: "no pulls", spec: strings.Replace(jobSpec, pull, "", 1), wantErr: invalidRefs},
		{name: "multiple pulls", spec: strings.Replace(jobSpec, pull, pull+`,{"number":43,"sha":"other"}`, 1), wantErr: invalidRefs},
		{name: "zero pull number", spec: strings.Replace(jobSpec, `"number":42`, `"number":0`, 1), wantErr: invalidRefs},
		{name: "missing head SHA", spec: strings.Replace(jobSpec, `,"sha":"head"`, "", 1), wantErr: invalidRefs},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("JOB_SPEC", tc.spec)
			filename := tc.filename
			if tc.file {
				filename = filepath.Join(t.TempDir(), "pr.json")
				data := prJSON
				if tc.data != "" {
					data = tc.data
				}
				if err := os.WriteFile(filename, []byte(data), 0600); err != nil {
					t.Fatal(err)
				}
			}
			transport := &pullTransport{status: http.StatusOK, body: prJSON}
			reader := Reader{Client: &http.Client{Transport: transport}}
			refs, err := reader.Read(t.Context(), tc.ref, filename)
			if diff := cmp.Diff(tc.calls, transport.calls); diff != "" {
				t.Errorf("unexpected GitHub requests (-want +got):\n%s", diff)
			}
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("want error containing %q, got %v", tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(tc.want, refs); diff != "" {
				t.Errorf("unexpected refs (-want +got):\n%s", diff)
			}
		})
	}
}

// RoundTrip checks the lookup request and returns the configured response.
func (p *pullTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	p.calls++
	if req.Method != http.MethodGet || req.URL.String() != "https://api.github.com/repos/org/repo/pulls/42" {
		return nil, fmt.Errorf("unexpected GitHub request: %s %s", req.Method, req.URL)
	}
	if req.Header.Get("Authorization") != "" {
		return nil, fmt.Errorf("unexpected GitHub authorization header")
	}
	body := `{"number":42}`
	if p.body != "" {
		body = p.body
	}
	return &http.Response{StatusCode: p.status, Status: http.StatusText(p.status), Body: io.NopCloser(strings.NewReader(body))}, nil
}

// TestFetch checks that lookups stay anonymous even when tokens are available and GitHub denies access.
func TestFetch(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "unused")
	t.Setenv("GH_TOKEN", "unused")
	for _, tc := range []struct {
		name    string
		status  int
		ref     string
		calls   int
		wantErr string
	}{
		{name: "public PR", ref: "org/repo#42", calls: 1, status: http.StatusOK},
		{name: "private or missing PR", ref: "org/repo#42", calls: 1, status: http.StatusNotFound, wantErr: "Not Found"},
		{name: "unauthorized", ref: "org/repo#42", calls: 1, status: http.StatusUnauthorized, wantErr: "Unauthorized"},
		{name: "forbidden", ref: "org/repo#42", calls: 1, status: http.StatusForbidden, wantErr: "Forbidden"},
		{name: "server error", ref: "org/repo#42", calls: 1, status: http.StatusInternalServerError, wantErr: "Internal Server Error"},
		{name: "rate limited", ref: "org/repo#42", calls: 1, status: http.StatusTooManyRequests, wantErr: "Too Many Requests"},
		{name: "empty reference", wantErr: `invalid --pull-request ""`},
		{name: "missing repository", ref: "org#42", wantErr: `invalid --pull-request "org#42"`},
		{name: "zero PR number", ref: "org/repo#0", wantErr: `invalid --pull-request "org/repo#0"`},
		{name: "non-numeric PR number", ref: "org/repo#bad", wantErr: `invalid --pull-request "org/repo#bad"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			transport := &pullTransport{status: tc.status}
			reader := Reader{Client: &http.Client{Transport: transport}}
			data, err := reader.fetch(t.Context(), tc.ref)
			if transport.calls != tc.calls {
				t.Errorf("want %d anonymous requests, got %d", tc.calls, transport.calls)
			}
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("want error containing %q, got %v", tc.wantErr, err)
				}
				return
			}
			if err != nil || string(data) != `{"number":42}` {
				t.Fatalf("unexpected response: %s, %v", data, err)
			}
		})
	}
}
