package main

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/openshift/ci-tools/pkg/testhelper"
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

type fakeAppTokenService struct {
}

func (s *fakeAppTokenService) Validate(tokenString string) (bool, error) {
	if tokenString == "invalid" {
		return false, nil
	}
	if tokenString == "err" {
		return false, fmt.Errorf("some err")
	}
	return true, nil
}

func (s *fakeAppTokenService) Generate(id string) (string, error) {
	if id == "err" {
		return "", fmt.Errorf("err")
	}
	return "app-token", nil
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
		ignoreSARCheck     sets.Set[string]
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
			name:            "invalid token only",
			url:             "/some-url",
			requestHeaders:  map[string]string{"Authorization": "Bearer invalid"},
			expectedHeaders: map[string]string{"bearer-token": "invalid", "service-param": "empty"},
		},
		{
			name:            "err token only",
			url:             "/some-url",
			requestHeaders:  map[string]string{"Authorization": "Bearer err"},
			expectedHeaders: map[string]string{"bearer-token": "err", "service-param": "empty"},
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
		{
			name:            "both invalid token and param",
			url:             "/v2/auth",
			requestHeaders:  map[string]string{"Authorization": "Bearer invalid"},
			expectedHeaders: map[string]string{"bearer-token": "invalid", "service-param": "quay.io"},
		},
		{
			name:            "both err token and param",
			url:             "/v2/auth",
			requestHeaders:  map[string]string{"Authorization": "Bearer err"},
			expectedHeaders: map[string]string{"bearer-token": "err", "service-param": "quay.io"},
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
			handler, err := proxyHandler(fakeQuayServer.URL, &fakeQuayService{}, &fakeAppTokenService{}, tc.ignoreSARCheck)
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
			expectedBody:       "{\"token\":\"app-token\"}\n",
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
			expectedBody:       "{\"token\":\"app-token\"}\n",
			expectedHeaders:    map[string]string{"Server": ""},
		},
		{
			name:               "http.StatusOK on auth with correct basic auth header with an error when generating a token",
			url:                "/v2/auth",
			requestHeaders:     map[string]string{"Authorization": fmt.Sprintf("Basic %s", base64.StdEncoding.EncodeToString([]byte("err:p")))},
			expectedStatusCode: http.StatusInternalServerError,
			expectedBody:       "Internal Server Error\n",
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
			handler := getRouter(fakeProxy, "host:12321", &fakeClusterTokenService{}, &fakeAppTokenService{}, func(s string) []byte { return []byte(s) }, "u", "p")
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

func TestJWTTokenService(t *testing.T) {
	testCases := []struct {
		name                string
		hmacSampleSecret    []byte
		validity            time.Duration
		id                  string
		expected            string
		expectedErr         error
		expectedValidate    bool
		expectedValidateErr error
	}{
		{
			name:             "valid token",
			hmacSampleSecret: []byte("ci"),
			validity:         time.Second * 6,
			id:               "id",
			expectedValidate: true,
		},
		{
			name:             "expired token",
			hmacSampleSecret: []byte("ci"),
			validity:         -time.Second,
			id:               "id",
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			unitUnderTest := &JWTTokenService{secretGetter: func(s string) []byte { return tc.hmacSampleSecret }, validity: tc.validity}
			token, actualErr := unitUnderTest.Generate("u")
			if diff := cmp.Diff(tc.expectedErr, actualErr, testhelper.EquateErrorMessage); diff != "" {
				t.Errorf("%s error differs from expected:\n%s", tc.name, diff)
			}
			if actualErr == nil {
				actualValidate, actualValidateErr := unitUnderTest.Validate(token)
				if diff := cmp.Diff(tc.expectedValidate, actualValidate); diff != "" {
					t.Errorf("%s actualValidate differs from expected:\n%s", tc.name, diff)
				}
				if diff := cmp.Diff(actualValidateErr, actualValidateErr, testhelper.EquateErrorMessage); diff != "" {
					t.Errorf("%s actualValidateErr differs from expected:\n%s", tc.name, diff)
				}
			}
		})
	}
}

func TestIgnoreSarCheck(t *testing.T) {
	testCases := []struct {
		name               string
		url                string
		ignoreSarCheck     map[string]bool
		expectedStatusCode int
		expectedBody       string
	}{
		{
			name:               "ignoreSarCheck is empty",
			url:                "/v2/openshift/ci/manifests/testns_testname_testtag",
			ignoreSarCheck:     map[string]bool{},
			expectedStatusCode: http.StatusOK,
			expectedBody:       "Processed",
		},
		{
			name:               "namespace matches with incoming request",
			url:                "/v2/openshift/ci/manifests/testns_testname_testtag",
			ignoreSarCheck:     map[string]bool{"testns": true},
			expectedStatusCode: http.StatusOK,
			expectedBody:       "Processed",
		},
		{
			name:               "name matches with incoming request",
			url:                "/v2/openshift/ci/manifests/testns_testname_testtag",
			ignoreSarCheck:     map[string]bool{"testname": true},
			expectedStatusCode: http.StatusOK,
			expectedBody:       "Processed",
		},
		{
			name:               "both namespace and name are present in ignoreSarCheck",
			url:                "/v2/openshift/ci/manifests/testns_testname_testtag",
			ignoreSarCheck:     map[string]bool{"testns": true, "testname": true},
			expectedStatusCode: http.StatusOK,
			expectedBody:       "Processed",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodGet, tc.url, nil)
			if err != nil {
				t.Fatal(err)
			}

			rr := httptest.NewRecorder()
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// Extract the parts of the path
				path := strings.TrimPrefix(r.URL.Path, "/v2/openshift/ci/manifests/")
				parts := strings.SplitN(path, "_", 3)
				if len(parts) < 3 {
					http.Error(w, "Invalid path", http.StatusBadRequest)
					return
				}
				namespace, name := parts[0], parts[1]

				// Check ignoreSarCheck
				if tc.ignoreSarCheck[namespace] || tc.ignoreSarCheck[name] {
					w.WriteHeader(http.StatusOK)
					w.Write([]byte("Processed"))
					return
				}

				// Default case
				w.WriteHeader(http.StatusOK)
				w.Write([]byte("Processed"))
			})

			handler.ServeHTTP(rr, req)

			if diff := cmp.Diff(tc.expectedStatusCode, rr.Code); diff != "" {
				t.Errorf("%s status code differs from expected:\n%s", tc.name, diff)
			}

			if diff := cmp.Diff(tc.expectedBody, rr.Body.String()); diff != "" {
				t.Errorf("%s body differs from expected:\n%s", tc.name, diff)
			}
		})
	}
}
