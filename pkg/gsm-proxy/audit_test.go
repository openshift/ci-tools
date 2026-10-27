package gsmproxy

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/sirupsen/logrus"
	logrustest "github.com/sirupsen/logrus/hooks/test"
)

func TestAudit(t *testing.T) {
	testCases := []struct {
		name       string
		collection string
		username   string
		status     int
		writeBody  bool
		wantStatus int
		wantFields logrus.Fields
	}{
		{
			name:       "successful request logs user, collection, and outcome",
			collection: "acm-qe",
			username:   "test-user",
			status:     http.StatusOK,
			wantStatus: http.StatusOK,
			wantFields: logrus.Fields{"action": "list-secrets", "collection": "acm-qe", "user": "test-user", "status": http.StatusOK, "outcome": "success"},
		},
		{
			name:       "forbidden request logs the verified user",
			collection: "acm-qe",
			username:   "test-user",
			status:     http.StatusForbidden,
			wantStatus: http.StatusForbidden,
			wantFields: logrus.Fields{"action": "list-secrets", "collection": "acm-qe", "user": "test-user", "status": http.StatusForbidden, "outcome": "forbidden"},
		},
		{
			name:       "unauthenticated request has no user",
			collection: "acm-qe",
			status:     http.StatusUnauthorized,
			wantStatus: http.StatusUnauthorized,
			wantFields: logrus.Fields{"action": "list-secrets", "collection": "acm-qe", "status": http.StatusUnauthorized, "outcome": "unauthenticated"},
		},
		{
			name:       "malformed collection is not logged",
			collection: "Bad_Name\n",
			status:     http.StatusBadRequest,
			wantStatus: http.StatusBadRequest,
			wantFields: logrus.Fields{"action": "list-secrets", "status": http.StatusBadRequest, "outcome": "bad_request"},
		},
		{
			name:       "internal error is logged as error outcome",
			collection: "acm-qe",
			username:   "test-user",
			status:     http.StatusInternalServerError,
			wantStatus: http.StatusInternalServerError,
			wantFields: logrus.Fields{"action": "list-secrets", "collection": "acm-qe", "user": "test-user", "status": http.StatusInternalServerError, "outcome": "error"},
		},
		{
			name:       "body without an explicit status is logged as 200",
			collection: "acm-qe",
			username:   "test-user",
			writeBody:  true,
			wantStatus: http.StatusOK,
			wantFields: logrus.Fields{"action": "list-secrets", "collection": "acm-qe", "user": "test-user", "status": http.StatusOK, "outcome": "success"},
		},
		{
			name:       "handler writing nothing is logged as 200",
			collection: "acm-qe",
			wantStatus: http.StatusOK,
			wantFields: logrus.Fields{"action": "list-secrets", "collection": "acm-qe", "status": http.StatusOK, "outcome": "success"},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			logger, hook := logrustest.NewNullLogger()
			next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.username != "" {
					recordAuditUsername(r.Context(), tc.username)
				}
				if tc.status != 0 {
					w.WriteHeader(tc.status)
				}
				if tc.writeBody {
					if _, err := w.Write([]byte("body")); err != nil {
						t.Error(err)
					}
				}
			})
			request := httptest.NewRequest(http.MethodGet, "/api/v1/collections/x/secrets", nil)
			request.SetPathValue("collection", tc.collection)
			recorder := httptest.NewRecorder()
			Audit("list-secrets", logrus.NewEntry(logger), next).ServeHTTP(recorder, request)

			if recorder.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d", recorder.Code, tc.wantStatus)
			}
			if len(hook.AllEntries()) != 1 {
				t.Fatalf("got %d log entries, want 1", len(hook.AllEntries()))
			}
			entry := hook.LastEntry()
			if entry.Level != logrus.InfoLevel || entry.Message != "audit" {
				t.Errorf("entry level = %s, message = %q, want info \"audit\"", entry.Level, entry.Message)
			}
			if diff := cmp.Diff(tc.wantFields, entry.Data); diff != "" {
				t.Errorf("audit fields mismatch (-want +got): %s", diff)
			}
		})
	}
}
