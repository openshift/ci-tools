package gsmproxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/sirupsen/logrus"
	logrustest "github.com/sirupsen/logrus/hooks/test"
)

func TestNewServeMux(t *testing.T) {
	const deletePattern = "DELETE /api/v1/collections/{collection}/secrets/{secret}"
	testCases := []struct {
		name        string
		method      string
		path        string
		header      string
		authzErr    error
		wantStatus  int
		wantHandler string
		wantAudit   logrus.Fields
	}{
		{
			name:       "healthz needs no token and is not audited",
			method:     http.MethodGet,
			path:       "/healthz",
			wantStatus: http.StatusOK,
		},
		{
			name:       "route without a token is rejected and audited",
			method:     http.MethodGet,
			path:       "/api/v1/collections/acm-qe/secrets",
			wantStatus: http.StatusUnauthorized,
			wantAudit:  logrus.Fields{"action": ListSecretsAction, "collection": "acm-qe", "status": http.StatusUnauthorized, "outcome": "unauthenticated"},
		},
		{
			name:       "forbidden caller does not reach the route and is audited",
			method:     http.MethodGet,
			path:       "/api/v1/collections/acm-qe/secrets",
			header:     "Bearer good",
			authzErr:   ErrForbidden,
			wantStatus: http.StatusForbidden,
			wantAudit:  logrus.Fields{"action": ListSecretsAction, "collection": "acm-qe", "user": "test-user", "status": http.StatusForbidden, "outcome": "forbidden"},
		},
		{
			name:        "authorized caller reaches the list route",
			method:      http.MethodGet,
			path:        "/api/v1/collections/acm-qe/secrets",
			header:      "Bearer good",
			wantStatus:  http.StatusNoContent,
			wantHandler: "list",
			wantAudit:   logrus.Fields{"action": ListSecretsAction, "collection": "acm-qe", "user": "test-user", "status": http.StatusNoContent, "outcome": "success"},
		},
		{
			name:        "each route is audited with its own action",
			method:      http.MethodDelete,
			path:        "/api/v1/collections/acm-qe/secrets/aws",
			header:      "Bearer good",
			wantStatus:  http.StatusNoContent,
			wantHandler: "delete",
			wantAudit:   logrus.Fields{"action": "delete-secret", "collection": "acm-qe", "user": "test-user", "status": http.StatusNoContent, "outcome": "success"},
		},
		{
			name:       "unknown path is not audited",
			method:     http.MethodGet,
			path:       "/api/v1/unknown",
			header:     "Bearer good",
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "unregistered method is not audited",
			method:     http.MethodPost,
			path:       "/api/v1/collections/acm-qe/secrets",
			header:     "Bearer good",
			wantStatus: http.StatusMethodNotAllowed,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			logger, hook := logrustest.NewNullLogger()
			var calledHandler string
			route := func(name string) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					calledHandler = name
					w.WriteHeader(http.StatusNoContent)
				})
			}
			mux := NewServeMux(
				&fakeVerifier{id: Identity{Username: "test-user", Groups: []string{"team-a"}}},
				&fakeAuthorizer{err: tc.authzErr},
				logrus.NewEntry(logger),
				Route{Pattern: ListSecretsPattern, Action: ListSecretsAction, Handler: route("list")},
				Route{Pattern: deletePattern, Action: "delete-secret", Handler: route("delete")},
			)
			request := httptest.NewRequest(tc.method, tc.path, nil)
			if tc.header != "" {
				request.Header.Set("Authorization", tc.header)
			}
			recorder := httptest.NewRecorder()
			mux.ServeHTTP(recorder, request)

			if recorder.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d", recorder.Code, tc.wantStatus)
			}
			if calledHandler != tc.wantHandler {
				t.Errorf("called handler = %q, want %q", calledHandler, tc.wantHandler)
			}
			var audits []logrus.Fields
			for _, entry := range hook.AllEntries() {
				if entry.Message == "audit" {
					audits = append(audits, entry.Data)
				}
			}
			var wantAudits []logrus.Fields
			if tc.wantAudit != nil {
				wantAudits = []logrus.Fields{tc.wantAudit}
			}
			if diff := cmp.Diff(wantAudits, audits); diff != "" {
				t.Errorf("audit entries mismatch (-want +got): %s", diff)
			}
		})
	}
}

func TestListSecretsHandlerServeHTTP(t *testing.T) {
	const sentinel = "upstream-detail-must-not-leak"
	testCases := []struct {
		name       string
		collection string
		names      []string
		err        error
		wantStatus int
		wantNames  []string
		wantError  string
	}{
		{
			name:       "lister failure returns the fixed message",
			collection: "acm-qe",
			err:        errors.New(sentinel),
			wantStatus: http.StatusInternalServerError,
			wantError:  MessageInternal,
		},
		{
			name:       "success returns the lister's names",
			collection: "acm-qe",
			names:      []string{"aws/key-id", "gcp/sa"},
			wantStatus: http.StatusOK,
			wantNames:  []string{"aws/key-id", "gcp/sa"},
		},
		{
			name:       "nil list returns an empty array",
			collection: "foo",
			wantStatus: http.StatusOK,
			wantNames:  []string{},
		},
		{
			name:       "empty list returns an empty array",
			collection: "foo",
			names:      []string{},
			wantStatus: http.StatusOK,
			wantNames:  []string{},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			lister := &fakeLister{names: tc.names, err: tc.err}
			var logs bytes.Buffer
			logger := logrus.New()
			logger.SetOutput(&logs)
			logger.SetLevel(logrus.TraceLevel)
			handler := &ListSecretsHandler{
				Lister: lister,
				Log:    logrus.NewEntry(logger),
			}
			ctx := context.WithValue(context.Background(), identityContextKey{}, Identity{Username: "test-user"})
			request := httptest.NewRequest(http.MethodGet, "/api/v1/collections/"+tc.collection+"/secrets", nil).WithContext(ctx)
			request.SetPathValue("collection", tc.collection)
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)

			if recorder.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d, body %s", recorder.Code, tc.wantStatus, recorder.Body.String())
			}
			if tc.wantStatus == http.StatusOK {
				var got ListSecretsResponse
				if err := json.Unmarshal(recorder.Body.Bytes(), &got); err != nil {
					t.Fatal(err)
				}
				if diff := cmp.Diff(tc.wantNames, got.Secrets); diff != "" {
					t.Errorf("secrets mismatch (-want +got): %s", diff)
				}
			} else {
				var got ErrorResponse
				if err := json.Unmarshal(recorder.Body.Bytes(), &got); err != nil {
					t.Fatal(err)
				}
				if got.Error != tc.wantError {
					t.Errorf("error = %q, want %q", got.Error, tc.wantError)
				}
				if strings.Contains(recorder.Body.String(), sentinel) {
					t.Errorf("response leaked upstream error: %s", recorder.Body.String())
				}
			}
			for _, sensitive := range append([]string{"test-user", "kerberos", tc.collection, sentinel}, tc.names...) {
				if strings.Contains(logs.String(), sensitive) {
					t.Errorf("logs contain sensitive request data %q: %s", sensitive, logs.String())
				}
			}
			if !lister.called || lister.gotCollection != tc.collection {
				t.Errorf("lister called = %t, collection = %q, want %q", lister.called, lister.gotCollection, tc.collection)
			}
			if lister.gotContext != ctx {
				t.Error("lister did not receive the request context")
			}
		})
	}
}

type fakeLister struct {
	names         []string
	err           error
	called        bool
	gotCollection string
	gotContext    context.Context
}

func (f *fakeLister) List(ctx context.Context, collection string) ([]string, error) {
	f.called = true
	f.gotCollection = collection
	f.gotContext = ctx
	return f.names, f.err
}
