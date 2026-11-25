package prowjobrunner

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

// pullTransport checks for anonymous requests and returns a canned GitHub response.
type pullTransport struct {
	status int
	calls  int
}

// TestReadRefs checks conversion of saved GitHub data and rejection of incomplete or ambiguous Prow refs.
func TestReadRefs(t *testing.T) {
	const prJSON = `{"number":42,"user":{"login":"author"},"base":{"repo":{"owner":{"login":"org"},"name":"repo"},"ref":"main","sha":"base"},"head":{"ref":"topic","sha":"head"}}`
	const base = `"org":"org","repo":"repo","base_ref":"main","base_sha":"base"`
	const pull = `{"number":42,"sha":"head"}`
	const jobSpec = `{"refs":{` + base + `,"pulls":[` + pull + `]}}`
	for _, tc := range []struct {
		name string
		file bool
		spec string
		want *prowapi.Refs
	}{
		{name: "saved GitHub PR", file: true, want: &prowapi.Refs{Org: "org", Repo: "repo", BaseRef: "main", BaseSHA: "base", Pulls: []prowapi.Pull{{Number: 42, SHA: "head", Author: "author", HeadRef: "topic"}}}},
		{name: "missing refs", spec: `{}`},
		{name: "missing org", spec: strings.Replace(jobSpec, `"org":"org",`, "", 1)},
		{name: "missing repo", spec: strings.Replace(jobSpec, `"repo":"repo",`, "", 1)},
		{name: "missing base ref", spec: strings.Replace(jobSpec, `"base_ref":"main",`, "", 1)},
		{name: "missing base SHA", spec: strings.Replace(jobSpec, `"base_sha":"base",`, "", 1)},
		{name: "no pulls", spec: strings.Replace(jobSpec, pull, "", 1)},
		{name: "multiple pulls", spec: strings.Replace(jobSpec, pull, pull+`,{"number":43,"sha":"other"}`, 1)},
		{name: "zero pull number", spec: strings.Replace(jobSpec, `"number":42`, `"number":0`, 1)},
		{name: "missing head SHA", spec: strings.Replace(jobSpec, `,"sha":"head"`, "", 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("JOB_SPEC", tc.spec)
			var filename string
			if tc.file {
				filename = filepath.Join(t.TempDir(), "pr.json")
				if err := os.WriteFile(filename, []byte(prJSON), 0600); err != nil {
					t.Fatal(err)
				}
			}
			refs, err := readRefs(t.Context(), "", filename)
			if tc.want == nil {
				if err == nil || !strings.Contains(err.Error(), "expected a base repository, branch and SHA, and one pull request with a number and head SHA") {
					t.Fatalf("expected invalid refs error, got %v", err)
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
	return &http.Response{StatusCode: p.status, Status: http.StatusText(p.status), Body: io.NopCloser(strings.NewReader(`{"number":42}`))}, nil
}

// TestFetchPR checks that lookups stay anonymous even when tokens are available and GitHub denies access.
func TestFetchPR(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "unused")
	t.Setenv("GH_TOKEN", "unused")
	for _, tc := range []struct {
		name    string
		status  int
		wantErr string
	}{
		{name: "public PR", status: http.StatusOK},
		{name: "private or missing PR", status: http.StatusNotFound, wantErr: "Not Found"},
		{name: "unauthorized", status: http.StatusUnauthorized, wantErr: "Unauthorized"},
		{name: "forbidden", status: http.StatusForbidden, wantErr: "Forbidden"},
		{name: "server error", status: http.StatusInternalServerError, wantErr: "Internal Server Error"},
		{name: "rate limited", status: http.StatusTooManyRequests, wantErr: "Too Many Requests"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			transport := &pullTransport{status: tc.status}
			data, err := fetchPR(t.Context(), &http.Client{Transport: transport}, "org/repo#42")
			if transport.calls != 1 {
				t.Errorf("want one anonymous request, got %d requests", transport.calls)
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
