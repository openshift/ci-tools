package proxy

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/sirupsen/logrus"

	"github.com/openshift/ci-tools/pkg/lease"
)

func TestProxy(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name                    string
		leaseClientFailingCalls map[string]error
		method                  string
		url                     string
		body                    string
		endpointFunc            func(http.ResponseWriter, *http.Request)
		wantCode                int
		wantBody                string
		wantCalls               []string
	}{
		{
			name:      "Acquire: get 1 lease",
			method:    http.MethodPost,
			url:       "/lease/acquire?type=aws-1",
			wantCode:  http.StatusOK,
			wantBody:  `{"names":["aws-1_0"]}`,
			wantCalls: []string{`acquireWaitWithPriority owner aws-1 free leased random`},
		},
		{
			name:      "Acquire: count zero returns no leases",
			method:    http.MethodPost,
			url:       "/lease/acquire?type=aws-1&count=0",
			wantCode:  http.StatusOK,
			wantBody:  `{"names":[]}`,
			wantCalls: []string{},
		},
		{
			name:      "Acquire: wrong http method",
			method:    http.MethodGet,
			url:       "/lease/acquire?type=aws-1",
			wantCode:  http.StatusMethodNotAllowed,
			wantBody:  string("Method GET not allowed, POST requests only.\n"),
			wantCalls: []string{},
		},
		{
			name:      "Acquire: type param is required",
			method:    http.MethodPost,
			url:       "/lease/acquire",
			wantCode:  http.StatusBadRequest,
			wantBody:  string("type is required\n"),
			wantCalls: []string{},
		},
		{
			name:   "Acquire: type not found",
			method: http.MethodPost,
			url:    "/lease/acquire?type=invalid",
			leaseClientFailingCalls: map[string]error{
				"acquireWaitWithPriority owner invalid free leased random": lease.ErrTypeNotFound,
			},
			wantCode:  http.StatusNotFound,
			wantBody:  "Failed to acquire lease \"invalid\": resource type not found\n",
			wantCalls: []string{`acquireWaitWithPriority owner invalid free leased random`},
		},
		{
			name:      "Release: wrong http method",
			method:    http.MethodGet,
			url:       "/lease/release",
			wantCode:  http.StatusMethodNotAllowed,
			wantBody:  string("Method GET not allowed, POST requests only.\n"),
			wantCalls: []string{},
		},
		{
			name:      "Release: release 1 lease",
			method:    http.MethodPost,
			url:       "/lease/release",
			body:      `{"names":["foo"]}`,
			wantCode:  http.StatusNoContent,
			wantCalls: []string{"releaseone owner foo free"},
		},
		{
			name: "Release: release 1 lease only",
			leaseClientFailingCalls: map[string]error{
				"releaseone owner bar free": errors.New("injected"),
			},
			method:    http.MethodPost,
			url:       "/lease/release",
			body:      `{"names":["foo", "bar"]}`,
			wantCode:  http.StatusInternalServerError,
			wantCalls: []string{"releaseone owner foo free", "releaseone owner bar free"},
			wantBody:  "Failed to release lease bar: injected\nReleased: foo\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			logger := logrus.NewEntry(&logrus.Logger{})
			gotCalls := make([]string, 0)
			leaseClient := lease.NewFakeClient("owner", "", 1, tc.leaseClientFailingCalls, &gotCalls, nil)

			srvMux := &http.ServeMux{}
			proxy := New(logger, func() lease.Client { return leaseClient })
			proxy.RegisterHandlers(srvMux)

			req, err := http.NewRequest(tc.method, tc.url, strings.NewReader(tc.body))
			if err != nil {
				t.Fatalf("unexpected failure when creating the http request: %s", err)
			}

			res := httptest.NewRecorder()
			srvMux.ServeHTTP(res, req)

			gotCode := res.Code
			if gotCode != tc.wantCode {
				t.Errorf("expect http code %d but got %d", tc.wantCode, gotCode)
			}

			gotBody, err := io.ReadAll(res.Body)
			if err != nil {
				t.Fatalf("unexpected failure when reading the response body: %s", err)
			}
			if diff := cmp.Diff(tc.wantBody, string(gotBody)); diff != "" {
				t.Errorf("unexpected response:\n%s", diff)
			}

			if diff := cmp.Diff(tc.wantCalls, gotCalls); diff != "" {
				t.Errorf("unexpected calls:\n%s", diff)
			}
		})
	}
}
