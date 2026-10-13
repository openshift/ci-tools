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

func TestRequireCollectionAccess(t *testing.T) {
	const sentinel = "upstream-detail-must-not-leak"
	testCases := []struct {
		name       string
		collection string
		header     string
		method     string
		verifyErr  error
		authzErr   error
		wantStatus int
		wantError  string
		wantVerify bool
		wantAuthz  bool
		wantNext   bool
	}{
		{
			name:       "malformed collection is rejected before any dependency is called",
			collection: "Bad_Name_",
			header:     "Bearer good",
			wantStatus: http.StatusBadRequest,
			wantError:  MessageBadCollection,
		},
		{
			name:       "missing collection is rejected before any dependency is called",
			header:     "Bearer good",
			wantStatus: http.StatusBadRequest,
			wantError:  MessageBadCollection,
		},
		{
			name:       "missing token is rejected before the verifier",
			collection: "acm-qe",
			wantStatus: http.StatusUnauthorized,
			wantError:  MessageUnauthenticated,
		},
		{
			name:       "non-bearer token is rejected before the verifier",
			collection: "acm-qe",
			header:     "Basic good",
			wantStatus: http.StatusUnauthorized,
			wantError:  MessageUnauthenticated,
		},
		{
			name:       "rejected token does not authorize or call the next handler",
			collection: "acm-qe",
			header:     "Bearer good",
			verifyErr:  fmt.Errorf("%s: %w", sentinel, ErrUnauthenticated),
			wantStatus: http.StatusUnauthorized,
			wantError:  MessageUnauthenticated,
			wantVerify: true,
		},
		{
			name:       "forbidden caller does not call the next handler",
			collection: "acm-qe",
			header:     "Bearer good",
			authzErr:   fmt.Errorf("%s: %w", sentinel, ErrForbidden),
			wantStatus: http.StatusForbidden,
			wantError:  MessageForbidden,
			wantVerify: true,
			wantAuthz:  true,
		},
		{
			name:       "authorization failure returns the fixed message",
			collection: "acm-qe",
			header:     "Bearer good",
			authzErr:   errors.New(sentinel),
			wantStatus: http.StatusInternalServerError,
			wantError:  MessageInternal,
			wantVerify: true,
			wantAuthz:  true,
		},
		{
			name:       "authorized caller reaches the next handler",
			collection: "acm-qe",
			header:     "Bearer good",
			wantStatus: http.StatusNoContent,
			wantVerify: true,
			wantAuthz:  true,
			wantNext:   true,
		},
		{
			name:       "collection access also applies to write requests",
			collection: "acm-qe",
			header:     "Bearer good",
			method:     http.MethodPost,
			wantStatus: http.StatusNoContent,
			wantVerify: true,
			wantAuthz:  true,
			wantNext:   true,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			identity := Identity{Username: "test-user", Groups: []string{"team-a"}}
			verifier := &fakeVerifier{id: identity, err: tc.verifyErr}
			authorizer := &fakeAuthorizer{err: tc.authzErr}
			logger := logrus.New()
			logger.SetOutput(io.Discard)
			ctx := t.Context()
			var nextCalled bool
			next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				nextCalled = true
				got, ok := IdentityFromContext(r.Context())
				if !ok {
					t.Error("next handler did not receive a verified identity")
				}
				if diff := cmp.Diff(identity, got); diff != "" {
					t.Errorf("identity mismatch (-want +got): %s", diff)
				}
				if r.PathValue("collection") != tc.collection {
					t.Errorf("collection = %q, want %q", r.PathValue("collection"), tc.collection)
				}
				if r.Context().Done() != ctx.Done() {
					t.Error("request cancellation was not preserved")
				}
				w.WriteHeader(http.StatusNoContent)
			})
			handler := RequireCollectionAccess(verifier, authorizer, logrus.NewEntry(logger), next)
			method := tc.method
			if method == "" {
				method = http.MethodGet
			}
			request := httptest.NewRequest(method, "/api/v1/collections/"+tc.collection+"/secrets", nil).WithContext(ctx)
			request.SetPathValue("collection", tc.collection)
			request.Header.Set("Authorization", tc.header)
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)

			if recorder.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d, body %s", recorder.Code, tc.wantStatus, recorder.Body.String())
			}
			if tc.wantError != "" {
				var got ErrorResponse
				if err := json.Unmarshal(recorder.Body.Bytes(), &got); err != nil {
					t.Fatal(err)
				}
				if got.Error != tc.wantError {
					t.Errorf("error = %q, want %q", got.Error, tc.wantError)
				}
			}
			if strings.Contains(recorder.Body.String(), sentinel) {
				t.Errorf("response leaked upstream error: %s", recorder.Body.String())
			}
			if verifier.called != tc.wantVerify {
				t.Errorf("verifier called = %t, want %t", verifier.called, tc.wantVerify)
			}
			if verifier.called && (verifier.gotToken != "good" || verifier.gotContext != ctx) {
				t.Error("verifier did not receive the bearer token and request context")
			}
			if authorizer.called != tc.wantAuthz {
				t.Errorf("authorizer called = %t, want %t", authorizer.called, tc.wantAuthz)
			}
			if authorizer.called {
				if diff := cmp.Diff(identity, authorizer.gotIdentity); diff != "" {
					t.Errorf("authorizer identity mismatch (-want +got): %s", diff)
				}
				if authorizer.gotCollection != tc.collection {
					t.Errorf("authorizer collection = %q, want %q", authorizer.gotCollection, tc.collection)
				}
			}
			if nextCalled != tc.wantNext {
				t.Errorf("next handler called = %t, want %t", nextCalled, tc.wantNext)
			}
		})
	}
}

type fakeVerifier struct {
	id         Identity
	err        error
	called     bool
	gotToken   string
	gotContext context.Context
}

func (f *fakeVerifier) Verify(ctx context.Context, rawToken string) (Identity, error) {
	f.called = true
	f.gotToken = rawToken
	f.gotContext = ctx
	return f.id, f.err
}

type fakeAuthorizer struct {
	err           error
	called        bool
	gotIdentity   Identity
	gotCollection string
}

func (f *fakeAuthorizer) Authorize(identity Identity, collection string) error {
	f.called = true
	f.gotIdentity = identity
	f.gotCollection = collection
	return f.err
}
