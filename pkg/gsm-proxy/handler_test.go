package gsmproxy

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/sirupsen/logrus"
)

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
			logger := logrus.New()
			logger.SetOutput(io.Discard)
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
