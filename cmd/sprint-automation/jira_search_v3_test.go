package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	jiraapi "github.com/andygrunwald/go-jira"
)

func jiraTestWriteJSON(w http.ResponseWriter, v interface{}) {
	if err := json.NewEncoder(w).Encode(v); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func fields() *jiraapi.IssueFields { return &jiraapi.IssueFields{} }

func TestSearchIssuesByJQL_validation(t *testing.T) {
	ctx := context.Background()
	c, err := jiraapi.NewClient(nil, "https://example.com/")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := searchIssuesByJQL(ctx, c, "   ", 0, nil); err == nil || !strings.Contains(err.Error(), "empty jql") {
		t.Errorf("empty jql: got %v", err)
	}
	if _, err := searchIssuesByJQL(ctx, nil, "x", 0, nil); err == nil || !strings.Contains(err.Error(), "nil client") {
		t.Errorf("nil client: got %v", err)
	}
}

func TestSearchIssuesByJQL_maxResultsCapped(t *testing.T) {
	var mu sync.Mutex
	var gotMax string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotMax = r.URL.Query().Get("maxResults")
		mu.Unlock()
		jiraTestWriteJSON(w, jiraSearchJQLV3Response{IsLast: true, Issues: nil})
	}))
	t.Cleanup(srv.Close)
	c, err := jiraapi.NewClient(nil, srv.URL+"/")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := searchIssuesByJQL(context.Background(), c, "project=X", 9999, nil); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	g := gotMax
	mu.Unlock()
	if g != "100" {
		t.Errorf("maxResults = %q, want 100", g)
	}
}

func TestSearchIssuesByJQL_singleAndPaginated(t *testing.T) {
	tests := []struct {
		name    string
		handler func(*testing.T) http.HandlerFunc
		wantN   int
		wantErr string
		limit   int
	}{
		{
			name: "one page",
			handler: func(t *testing.T) http.HandlerFunc {
				return func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path != "/rest/api/3/search/jql" {
						t.Errorf("path %s", r.URL.Path)
					}
					jiraTestWriteJSON(w, jiraSearchJQLV3Response{
						IsLast: true,
						Issues: []jiraapi.Issue{{ID: "1", Key: "K-1", Fields: fields()}},
					})
				}
			},
			wantN: 1,
			limit: 10,
		},
		{
			name: "two pages",
			handler: func(t *testing.T) http.HandlerFunc {
				var n int
				return func(w http.ResponseWriter, r *http.Request) {
					n++
					switch n {
					case 1:
						if r.URL.Query().Get("nextPageToken") != "" {
							t.Error("first page must not send nextPageToken")
						}
						jiraTestWriteJSON(w, jiraSearchJQLV3Response{
							IsLast:        false,
							NextPageToken: "p2",
							Issues:        []jiraapi.Issue{{Key: "A", Fields: fields()}},
						})
					case 2:
						if r.URL.Query().Get("nextPageToken") != "p2" {
							t.Errorf("token %q", r.URL.Query().Get("nextPageToken"))
						}
						jiraTestWriteJSON(w, jiraSearchJQLV3Response{
							IsLast: true,
							Issues: []jiraapi.Issue{{Key: "B", Fields: fields()}},
						})
					default:
						t.Fatalf("extra request %d", n)
					}
				}
			},
			wantN: 2,
			limit: 10,
		},
		{
			name: "missing next token",
			handler: func(_ *testing.T) http.HandlerFunc {
				return func(w http.ResponseWriter, r *http.Request) {
					jiraTestWriteJSON(w, jiraSearchJQLV3Response{IsLast: false, Issues: []jiraapi.Issue{{Key: "x", Fields: fields()}}})
				}
			},
			wantErr: "nextPageToken empty",
			limit:   5,
		},
		{
			name: "token loop",
			handler: func(_ *testing.T) http.HandlerFunc {
				return func(w http.ResponseWriter, r *http.Request) {
					tok := r.URL.Query().Get("nextPageToken")
					if tok == "" {
						jiraTestWriteJSON(w, jiraSearchJQLV3Response{
							IsLast:        false,
							NextPageToken: "same",
							Issues:        []jiraapi.Issue{{Key: "1", Fields: fields()}},
						})
						return
					}
					jiraTestWriteJSON(w, jiraSearchJQLV3Response{
						IsLast:        false,
						NextPageToken: "same",
						Issues:        []jiraapi.Issue{{Key: "2", Fields: fields()}},
					})
				}
			},
			wantErr: "server loop",
			limit:   10,
		},
		{
			name: "reject issue empty key",
			handler: func(_ *testing.T) http.HandlerFunc {
				return func(w http.ResponseWriter, r *http.Request) {
					jiraTestWriteJSON(w, jiraSearchJQLV3Response{
						IsLast: true,
						Issues: []jiraapi.Issue{{ID: "1", Key: " ", Fields: fields()}},
					})
				}
			},
			wantErr: "empty key",
			limit:   5,
		},
		{
			name: "reject nil fields",
			handler: func(_ *testing.T) http.HandlerFunc {
				return func(w http.ResponseWriter, r *http.Request) {
					jiraTestWriteJSON(w, jiraSearchJQLV3Response{
						IsLast: true,
						Issues: []jiraapi.Issue{{ID: "1", Key: "K-1", Fields: nil}},
					})
				}
			},
			wantErr: "nil fields",
			limit:   5,
		},
		{
			name: "page limit",
			handler: func(_ *testing.T) http.HandlerFunc {
				var p int
				return func(w http.ResponseWriter, r *http.Request) {
					p++
					jiraTestWriteJSON(w, jiraSearchJQLV3Response{
						IsLast:        false,
						NextPageToken: string(rune('a' + p)),
						Issues:        []jiraapi.Issue{{Key: "x", Fields: fields()}},
					})
				}
			},
			wantErr: "exceeded 2 pages",
			limit:   2,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(tt.handler(t))
			t.Cleanup(srv.Close)
			c, err := jiraapi.NewClient(nil, srv.URL+"/")
			if err != nil {
				t.Fatal(err)
			}
			got, err := searchIssuesByJQLPageLimit(context.Background(), c, "project=DPTP", 10, []string{"*navigable"}, tt.limit)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want substring %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != tt.wantN {
				t.Fatalf("len = %d want %d", len(got), tt.wantN)
			}
		})
	}
}

func TestSearchIssuesByJQL_HTTPErrorBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		if _, err := w.Write([]byte(`{"errorMessages":["no-access"]}`)); err != nil {
			return
		}
	}))
	t.Cleanup(srv.Close)
	c, err := jiraapi.NewClient(nil, srv.URL+"/")
	if err != nil {
		t.Fatal(err)
	}
	_, err = searchIssuesByJQL(context.Background(), c, "x", 10, nil)
	if err == nil {
		t.Fatal("want error")
	}
	if !strings.Contains(err.Error(), "403") || !strings.Contains(err.Error(), "no-access") {
		t.Fatalf("got %v", err)
	}
}

func TestSearchIssuesByJQL_decodeError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := w.Write([]byte(`not json`)); err != nil {
			return
		}
	}))
	t.Cleanup(srv.Close)
	c, err := jiraapi.NewClient(nil, srv.URL+"/")
	if err != nil {
		t.Fatal(err)
	}
	_, err = searchIssuesByJQL(context.Background(), c, "x", 10, nil)
	if err == nil || !strings.Contains(err.Error(), "decode") {
		t.Fatalf("got %v", err)
	}
}

func TestSearchIssuesByJQL_contextTimeoutSecondPage(t *testing.T) {
	var first bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !first {
			first = true
			jiraTestWriteJSON(w, jiraSearchJQLV3Response{
				IsLast:        false,
				NextPageToken: "n",
				Issues:        nil,
			})
			return
		}
		time.Sleep(300 * time.Millisecond)
		jiraTestWriteJSON(w, jiraSearchJQLV3Response{IsLast: true})
	}))
	t.Cleanup(srv.Close)
	c, err := jiraapi.NewClient(http.DefaultClient, srv.URL+"/")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err = searchIssuesByJQLPageLimit(ctx, c, "jql", 10, nil, 10)
	if err == nil {
		t.Fatal("want ctx error")
	}
	errStr := err.Error()
	if !strings.Contains(errStr, "deadline") && !strings.Contains(errStr, "canceled") {
		t.Fatalf("got %v", err)
	}
}

func TestTruncateJiraErrBody(t *testing.T) {
	long := strings.Repeat("é", jiraErrBodyRunes+50)
	s := truncateJiraErrBody([]byte(long))
	if !strings.HasSuffix(s, "…") {
		t.Errorf("expected truncated suffix …, got len %d", len(s))
	}
	if utf8.RuneCountInString(strings.TrimSuffix(s, "…")) != jiraErrBodyRunes {
		t.Errorf("truncated rune count = %d, want %d", utf8.RuneCountInString(strings.TrimSuffix(s, "…")), jiraErrBodyRunes)
	}
	short := strings.Repeat("a", 100)
	if truncateJiraErrBody([]byte(short)) != short {
		t.Errorf("short string should be unchanged")
	}
}
