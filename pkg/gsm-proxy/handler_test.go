package gsmproxy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/sirupsen/logrus"
)

func TestListSecretsHandler(t *testing.T) {
	sentinel := "upstream-detail-must-not-leak"

	testCases := []struct {
		name       string
		collection string
		token      string
		verifier   *fakeVerifier
		authorizer *fakeAuthorizer
		lister     *fakeLister

		wantStatus int
		wantBody   string
		wantVerify bool
		wantAuthz  bool
		wantList   bool
	}{
		{
			name:       "malformed collection is rejected before any dependency is called",
			collection: "Bad_Name_",
			token:      "good",
			wantStatus: http.StatusBadRequest,
			wantBody:   MessageBadCollection,
		},
		{
			name:       "missing token is rejected before the verifier",
			collection: "acm-qe",
			wantStatus: http.StatusUnauthorized,
			wantBody:   MessageUnauthenticated,
		},
		{
			name:       "rejected token does not authorize or list",
			collection: "acm-qe",
			token:      "bad",
			verifier:   &fakeVerifier{err: fmtWrap(ErrUnauthenticated, "expired")},
			wantStatus: http.StatusUnauthorized,
			wantBody:   MessageUnauthenticated,
			wantVerify: true,
		},
		{
			name:       "forbidden caller does not list",
			collection: "acm-qe",
			token:      "good",
			verifier:   &fakeVerifier{id: Identity{Username: "psalajov", Groups: []string{"acm-pics"}}},
			authorizer: &fakeAuthorizer{err: fmtWrap(ErrForbidden, "not an owner")},
			wantStatus: http.StatusForbidden,
			wantBody:   MessageForbidden,
			wantVerify: true,
			wantAuthz:  true,
		},
		{
			name:       "lister failure returns the fixed message",
			collection: "acm-qe",
			token:      "good",
			verifier:   &fakeVerifier{id: Identity{Username: "psalajov", Groups: []string{"acm-pics"}}},
			authorizer: &fakeAuthorizer{},
			lister:     &fakeLister{err: errors.New(sentinel)},
			wantStatus: http.StatusInternalServerError,
			wantBody:   MessageInternal,
			wantVerify: true,
			wantAuthz:  true,
			wantList:   true,
		},
		{
			name:       "success returns the lister's names and nothing else",
			collection: "acm-qe",
			token:      "good",
			verifier:   &fakeVerifier{id: Identity{Username: "psalajov", Groups: []string{"acm-pics"}}},
			authorizer: &fakeAuthorizer{},
			lister:     &fakeLister{names: []string{"aws/key-id", "gcp/sa"}},
			wantStatus: http.StatusOK,
			wantVerify: true,
			wantAuthz:  true,
			wantList:   true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.verifier == nil {
				tc.verifier = &fakeVerifier{}
			}
			if tc.authorizer == nil {
				tc.authorizer = &fakeAuthorizer{}
			}
			if tc.lister == nil {
				tc.lister = &fakeLister{}
			}
			log := logrus.New()
			log.SetOutput(io.Discard)
			h := &ListSecretsHandler{
				Verifier:   tc.verifier,
				Authorizer: tc.authorizer,
				Lister:     tc.lister,
				Log:        logrus.NewEntry(log),
			}

			req := httptest.NewRequest(http.MethodGet, "/api/v1/collections/"+tc.collection+"/secrets", nil)
			req.SetPathValue("collection", tc.collection)
			if tc.token != "" {
				req.Header.Set("Authorization", "Bearer "+tc.token)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d, body %s", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if tc.wantStatus == http.StatusOK {
				var got ListSecretsResponse
				if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
					t.Fatal(err)
				}
				if diff := cmp.Diff(tc.lister.names, got.Secrets); diff != "" {
					t.Fatalf("secrets mismatch: %s", diff)
				}
			} else {
				var got ErrorResponse
				if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
					t.Fatal(err)
				}
				if got.Error != tc.wantBody {
					t.Fatalf("error = %q, want %q", got.Error, tc.wantBody)
				}
				if tc.lister.err != nil && strings.Contains(rec.Body.String(), sentinel) {
					t.Fatalf("response leaked upstream error: %s", rec.Body.String())
				}
			}
			if tc.verifier.called != tc.wantVerify {
				t.Fatalf("verifier called = %v, want %v", tc.verifier.called, tc.wantVerify)
			}
			if tc.verifier.called && tc.verifier.gotToken != tc.token {
				t.Fatalf("verifier token = %q, want %q", tc.verifier.gotToken, tc.token)
			}
			if tc.authorizer.called != tc.wantAuthz {
				t.Fatalf("authorizer called = %v, want %v", tc.authorizer.called, tc.wantAuthz)
			}
			if tc.lister.called != tc.wantList {
				t.Fatalf("lister called = %v, want %v", tc.lister.called, tc.wantList)
			}
		})
	}
}

func TestNewServeMuxOmitsListRouteUntilHandlerExists(t *testing.T) {
	mux := NewServeMux(nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("healthz status = %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/collections/acm-qe/secrets", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("list route status = %d, want 404 while the handler is unwired", rec.Code)
	}
}

func TestBearerToken(t *testing.T) {
	testCases := []struct {
		name    string
		header  string
		want    string
		wantErr bool
	}{
		{name: "bearer", header: "Bearer abc", want: "abc"},
		{name: "case insensitive scheme", header: "bearer abc", want: "abc"},
		{name: "missing", wantErr: true},
		{name: "empty token", header: "Bearer   ", wantErr: true},
		{name: "not bearer", header: "Basic abc", wantErr: true},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := BearerToken(tc.header)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected an error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("token = %q, want %q", got, tc.want)
			}
		})
	}
}

func fmtWrap(sentinel error, msg string) error {
	return fmt.Errorf("%s: %w", msg, sentinel)
}

type fakeVerifier struct {
	id       Identity
	err      error
	called   bool
	gotToken string
}

func (f *fakeVerifier) Verify(_ context.Context, rawToken string) (Identity, error) {
	f.called = true
	f.gotToken = rawToken
	return f.id, f.err
}

type fakeAuthorizer struct {
	err    error
	called bool
}

func (f *fakeAuthorizer) Authorize(_ Identity, _ string) error {
	f.called = true
	return f.err
}

type fakeLister struct {
	names  []string
	err    error
	called bool
}

func (f *fakeLister) List(_ context.Context, _ string) ([]string, error) {
	f.called = true
	return f.names, f.err
}
