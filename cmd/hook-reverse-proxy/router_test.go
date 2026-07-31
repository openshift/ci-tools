package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"sync"
	"testing"

	"github.com/sirupsen/logrus"

	"sigs.k8s.io/prow/pkg/github"
)

func req(method, target string, body io.Reader, headers map[string]string) *http.Request {
	r := httptest.NewRequest(method, target, body)
	for k, v := range headers {
		r.Header.Add(k, v)
	}
	return r
}

func TestRewrite(t *testing.T) {
	t.Parallel()

	u := func(rawURL string) *URL {
		parsed, err := url.Parse(rawURL)
		if err != nil {
			t.Fatalf("parse url %s: %s", rawURL, err.Error())
		}
		return &URL{URL: parsed}
	}
	ge := func(genericEvent github.GenericEvent) *bytes.Buffer {
		geBytes, err := json.Marshal(genericEvent)
		if err != nil {
			t.Fatalf("marshal github event: %s", err)
		}
		return bytes.NewBuffer(geBytes)
	}
	stdHeaders := map[string]string{
		"X-GitHub-Event":    "gh-event",
		"X-GitHub-Delivery": "gh-delivery",
		"content-type":      "application/json",
	}

	for _, tc := range []struct {
		name    string
		config  Config
		pr      *httputil.ProxyRequest
		wantURL string
	}{
		{
			name:   "GH event header missing: match default route",
			config: Config{DefaultRoute: &Route{Target: u("https://hook/")}},
			pr: &httputil.ProxyRequest{
				In:  req("POST", "https://in", nil, nil),
				Out: req("POST", "https://out", nil, nil),
			},
			wantURL: "https://hook/",
		},
		{
			name:   "GH delivery header missing: match default route",
			config: Config{DefaultRoute: &Route{Target: u("https://hook/")}},
			pr: &httputil.ProxyRequest{
				In:  req("POST", "https://in", nil, map[string]string{"X-GitHub-Event": "gh-event"}),
				Out: req("POST", "https://out", nil, nil),
			},
			wantURL: "https://hook/",
		},
		{
			name:   "Wrong content-type: match default route",
			config: Config{DefaultRoute: &Route{Target: u("https://hook/")}},
			pr: &httputil.ProxyRequest{
				In: req("POST", "https://in", nil, map[string]string{
					"X-GitHub-Event":    "gh-event",
					"X-GitHub-Delivery": "gh-delivery",
					"content-type":      "application/yaml",
				}),
				Out: req("POST", "https://out", nil, nil),
			},
			wantURL: "https://hook/",
		},
		{
			name:   "Invalid payload: match default route",
			config: Config{DefaultRoute: &Route{Target: u("https://hook/")}},
			pr: &httputil.ProxyRequest{
				In:  req("POST", "https://in", bytes.NewBuffer([]byte("xxx")), stdHeaders),
				Out: req("POST", "https://out", nil, nil),
			},
			wantURL: "https://hook/",
		},
		{
			name:   "Invalid HTTP method: match default route",
			config: Config{DefaultRoute: &Route{Target: u("https://hook/")}},
			pr: &httputil.ProxyRequest{
				In:  req("GET", "https://in", bytes.NewBuffer([]byte("xxx")), stdHeaders),
				Out: req("GET", "https://out", nil, nil),
			},
			wantURL: "https://hook/",
		},
		{
			name:   "No routes defined: match default",
			config: Config{DefaultRoute: &Route{Target: u("https://hook/")}},
			pr: &httputil.ProxyRequest{
				In:  req("POST", "https://in", nil, stdHeaders),
				Out: req("POST", "https://out", nil, nil),
			},
			wantURL: "https://hook/",
		},
		{
			name: "Match org and repo",
			config: Config{
				Routes: []Route{{
					Target: u("https://hook-proxy"),
					Matches: []Match{{
						Org:   "openshift",
						Repos: []string{"ci-tools"},
					}},
				}},
				DefaultRoute: &Route{Target: u("https://hook/")},
			},
			pr: &httputil.ProxyRequest{
				In: req("POST", "https://in", ge(github.GenericEvent{
					Org:  github.Organization{Login: "openshift"},
					Repo: github.Repo{Name: "ci-tools"},
				}), stdHeaders),
				Out: req("POST", "https://out/", nil, nil),
			},
			wantURL: "https://hook-proxy",
		},
		{
			name: "Match org wildcard",
			config: Config{
				Routes: []Route{{
					Target: u("https://hook-proxy"),
					Matches: []Match{{
						Org: "*",
					}},
				}},
				DefaultRoute: &Route{Target: u("https://hook/")},
			},
			pr: &httputil.ProxyRequest{
				In: req("POST", "https://in", ge(github.GenericEvent{
					Org:  github.Organization{Login: "openshift"},
					Repo: github.Repo{Name: "ci-tools"},
				}), stdHeaders),
				Out: req("POST", "https://out/", nil, nil),
			},
			wantURL: "https://hook-proxy",
		},
		{
			name: "Match repo wildcard",
			config: Config{
				Routes: []Route{{
					Target: u("https://hook-proxy"),
					Matches: []Match{{
						Org:   "openshift",
						Repos: []string{"*"},
					}},
				}},
				DefaultRoute: &Route{Target: u("https://hook/")},
			},
			pr: &httputil.ProxyRequest{
				In: req("POST", "https://in", ge(github.GenericEvent{
					Org:  github.Organization{Login: "openshift"},
					Repo: github.Repo{Name: "ci-tools"},
				}), stdHeaders),
				Out: req("POST", "https://out/", nil, nil),
			},
			wantURL: "https://hook-proxy",
		},
		{
			name: "Repo missing: match default route",
			config: Config{
				Routes: []Route{{
					Target: u("https://hook-proxy"),
					Matches: []Match{{
						Org:   "openshift",
						Repos: []string{"api"},
					}},
				}},
				DefaultRoute: &Route{Target: u("https://hook/")},
			},
			pr: &httputil.ProxyRequest{
				In: req("POST", "https://in", ge(github.GenericEvent{
					Org: github.Organization{Login: "openshift"},
				}), stdHeaders),
				Out: req("POST", "https://out/", nil, nil),
			},
			wantURL: "https://hook/",
		},
		{
			name: "Org missing: match default route",
			config: Config{
				Routes: []Route{{
					Target: u("https://hook-proxy"),
					Matches: []Match{{
						Org:   "openshift",
						Repos: []string{"api"},
					}},
				}},
				DefaultRoute: &Route{Target: u("https://hook/")},
			},
			pr: &httputil.ProxyRequest{
				In:  req("POST", "https://in", ge(github.GenericEvent{}), stdHeaders),
				Out: req("POST", "https://out/", nil, nil),
			},
			wantURL: "https://hook/",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			logger := logrus.NewEntry(logrus.StandardLogger())
			router := router{
				log:  logger,
				m:    &sync.RWMutex{},
				conf: &tc.config,
			}
			router.rewrite(tc.pr)

			gotURL := tc.pr.Out.URL.String()
			if tc.wantURL != gotURL {
				t.Errorf("want URL %s but got %s", tc.wantURL, gotURL)
			}
		})
	}
}
