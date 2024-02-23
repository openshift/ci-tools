package main

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/sirupsen/logrus"
)

type fakeClusterTokenService struct {
}

func (s *fakeClusterTokenService) Validate(token string) (bool, error) {
	if token == "w" {
		return false, nil
	}
	return true, nil
}

type fakeQuayService struct {
}

func (s *fakeQuayService) GetRobotToken() (string, error) {
	return "fake-token", nil
}

func init() {
	logrus.SetLevel(logrus.TraceLevel)
}

func TestProxyHandler(t *testing.T) {
	fakeQuayMux := http.NewServeMux()
	fakeQuayMux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		value := "empty"
		if v := r.Header.Get("Authorization"); strings.HasPrefix(v, "Bearer ") {
			value = strings.TrimPrefix(v, "Bearer ")
		}
		w.Header().Set("bearer-token", value)
		value = "empty"
		if v := r.URL.Query().Get("service"); v != "" {
			value = v
		}
		w.Header().Set("service-param", value)
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintln(w, "OK")
	})
	fakeQuayServer := httptest.NewServer(fakeQuayMux)
	defer fakeQuayServer.Close()

	testCases := []struct {
		name               string
		url                string
		requestHeaders     map[string]string
		expectedStatusCode int
		expectedBody       string
		expectedHeaders    map[string]string
	}{
		{
			name:            "neither token nor param",
			url:             "/some-url",
			expectedHeaders: map[string]string{"bearer-token": "empty", "service-param": "empty"},
		},
		{
			name:            "token only",
			url:             "/some-url",
			requestHeaders:  map[string]string{"Authorization": "Bearer some"},
			expectedHeaders: map[string]string{"bearer-token": "fake-token", "service-param": "empty"},
		},
		{
			name:            "param only",
			url:             "/v2/auth",
			expectedHeaders: map[string]string{"bearer-token": "empty", "service-param": "quay.io"},
		},
		{
			name:            "both token and param",
			url:             "/v2/auth",
			requestHeaders:  map[string]string{"Authorization": "Bearer some"},
			expectedHeaders: map[string]string{"bearer-token": "some", "service-param": "quay.io"},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequest("GET", tc.url, nil)
			if err != nil {
				t.Fatal(err)
			}
			for k, v := range tc.requestHeaders {
				req.Header.Set(k, v)
			}
			handler, err := proxyHandler(fakeQuayServer.URL, &fakeClusterTokenService{}, &fakeQuayService{})
			if err != nil {
				t.Fatal(err)
			}
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			if diff := cmp.Diff("OK\n", rr.Body.String()); diff != "" {
				t.Errorf("%s body differs from expected:\n%s", tc.name, diff)
			}

			if diff := cmp.Diff(http.StatusOK, rr.Code); diff != "" {
				t.Errorf("%s status code differs from expected:\n%s", tc.name, diff)
			}

			for k, v := range tc.expectedHeaders {
				if diff := cmp.Diff(v, rr.Header().Get(k)); diff != "" {
					t.Errorf("%s header %s value differs from expected:\n%s", tc.name, k, diff)
				}
			}
		})
	}
}

func TestGetRouter(t *testing.T) {
	fakeProxy := http.NewServeMux()
	fakeProxy.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Server", "fake-quay-server")
		_, _ = fmt.Fprintln(w, "OK")
		w.WriteHeader(http.StatusOK)
	})

	testCases := []struct {
		name               string
		url                string
		requestHeaders     map[string]string
		expectedStatusCode int
		expectedBody       string
		expectedHeaders    map[string]string
	}{
		{
			name:               "http.StatusUnauthorized on random url",
			url:                "/some-url",
			expectedStatusCode: http.StatusUnauthorized,
			expectedBody:       "Unauthorized\n",
			expectedHeaders:    map[string]string{"Server": "", "Www-Authenticate": `Bearer realm="https://host:12321/v2/auth",service="host:12321"`},
		},
		{
			name:               "http.StatusOK on health check",
			url:                "/healthz",
			expectedStatusCode: http.StatusOK,
			expectedBody:       "OK\n",
			expectedHeaders:    map[string]string{"Server": ""},
		},
		{
			name:               "http.StatusUnauthorized on auth without basic auth header",
			url:                "/v2/auth",
			expectedStatusCode: http.StatusUnauthorized,
			expectedBody:       "Unauthorized\n",
			expectedHeaders:    map[string]string{"Server": "", "Www-Authenticate": `Bearer realm="https://host:12321/v2/auth",service="host:12321"`},
		},
		{
			name:               "http.StatusOK on auth with correct robot's basic auth header",
			url:                "/v2/auth",
			requestHeaders:     map[string]string{"Authorization": fmt.Sprintf("Basic %s", base64.StdEncoding.EncodeToString([]byte("u:p")))},
			expectedStatusCode: http.StatusOK,
			expectedBody:       "OK\n",
			expectedHeaders:    map[string]string{"Server": "fake-quay-server"},
		},
		{
			name:               "http.StatusUnauthorized on auth with wrong basic auth header",
			url:                "/v2/auth",
			requestHeaders:     map[string]string{"Authorization": fmt.Sprintf("Basic %s", base64.StdEncoding.EncodeToString([]byte("u:w")))},
			expectedStatusCode: http.StatusUnauthorized,
			expectedBody:       "Unauthorized\n",
			expectedHeaders:    map[string]string{"Server": "", "Www-Authenticate": `Bearer realm="https://host:12321/v2/auth",service="host:12321"`},
		},
		{
			name:               "http.StatusOK on auth with correct basic auth header",
			url:                "/v2/auth",
			requestHeaders:     map[string]string{"Authorization": fmt.Sprintf("Basic %s", base64.StdEncoding.EncodeToString([]byte("a:p")))},
			expectedStatusCode: http.StatusOK,
			expectedBody:       "{\"token\":\"p\"}\n",
			expectedHeaders:    map[string]string{"Server": ""},
		},
		{
			name:               "http.StatusOK on v2 with token header",
			url:                "/v2",
			requestHeaders:     map[string]string{"Authorization": "Bearer t"},
			expectedStatusCode: http.StatusOK,
			expectedBody:       "OK\n",
			expectedHeaders:    map[string]string{"Server": "fake-quay-server"},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequest("GET", tc.url, nil)
			if err != nil {
				t.Fatal(err)
			}
			for k, v := range tc.requestHeaders {
				req.Header.Set(k, v)
			}
			handler := getRouter(fakeProxy, "host:12321", &fakeClusterTokenService{}, func(s string) []byte { return []byte(s) }, "u", "p")
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			if diff := cmp.Diff(tc.expectedBody, rr.Body.String()); diff != "" {
				t.Errorf("%s body differs from expected:\n%s", tc.name, diff)
			}

			if diff := cmp.Diff(tc.expectedStatusCode, rr.Code); diff != "" {
				t.Errorf("%s status code differs from expected:\n%s", tc.name, diff)
			}

			for k, v := range tc.expectedHeaders {
				if diff := cmp.Diff(v, rr.Header().Get(k)); diff != "" {
					t.Errorf("%s header %s value differs from expected:\n%s", tc.name, k, diff)
				}
			}
		})
	}
}
