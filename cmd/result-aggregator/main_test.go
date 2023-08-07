package main

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/openshift/ci-tools/pkg/results"
	"github.com/openshift/ci-tools/pkg/testhelper"
)

func TestValidator(t *testing.T) {
	dir, err := os.MkdirTemp("", "test")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Errorf("failed to remove the temp dir: %s", dir)
		}
	}()

	passwdFileRaw := filepath.Join(dir, "passwdFile")
	if err := os.WriteFile(passwdFileRaw, []byte(`a:b
c:d
:`), 0644); err != nil {
		t.Fatal(err)
	}

	passwdFileRawAbnormalContent := filepath.Join(dir, "passwdFileRawAbnormalContent")
	if err := os.WriteFile(passwdFileRawAbnormalContent, []byte("some"), 0644); err != nil {
		t.Fatal(err)
	}

	testCases := []struct {
		name           string
		username       string
		password       func() []byte
		passwdFile     string
		validateInputs map[string]map[string]bool
	}{
		{
			name:           "username and password only",
			username:       "andy",
			password:       func() []byte { return []byte("secret") },
			validateInputs: map[string]map[string]bool{"andy": {"secret": true}, "bob": {"secret": false}, "a": {"b": false}, "c": {"d": false}},
		},
		{
			name:           "username and password only and both empty",
			password:       func() []byte { return []byte("") },
			validateInputs: map[string]map[string]bool{"": {"": false}, "andy": {"secret": false}, "bob": {"secret": false}, "a": {"b": false}, "c": {"d": false}},
		},
		{
			name:           "username and password and passwdFileRaw",
			username:       "andy",
			password:       func() []byte { return []byte("secret") },
			passwdFile:     passwdFileRaw,
			validateInputs: map[string]map[string]bool{"": {"": false}, "andy": {"secret": true}, "bob": {"secret": false}, "a": {"b": true}, "c": {"d": true}},
		},
		{
			name:           "only passwdFileRaw",
			passwdFile:     passwdFileRaw,
			validateInputs: map[string]map[string]bool{"": {"": false}, "andy": {"secret": false}, "bob": {"secret": false}, "a": {"b": true}, "c": {"d": true}},
		},
		{
			name:           "abnormal content",
			passwdFile:     passwdFileRawAbnormalContent,
			validateInputs: map[string]map[string]bool{"": {"": false}, "andy": {"secret": false}, "bob": {"secret": false}, "a": {"b": false}, "c": {"d": false}},
		},
		{
			name:           "nil password func",
			username:       "andy",
			validateInputs: map[string]map[string]bool{"": {"": false}, "andy": {"secret": false}, "bob": {"secret": false}, "a": {"b": false}, "c": {"d": false}},
		},
	}

	for _, tc := range testCases {
		validator := &multi{delegates: []validator{
			&passwdFile{file: tc.passwdFile},
			&literal{username: tc.username, password: tc.password}},
		}
		for user := range tc.validateInputs {
			for password, expected := range tc.validateInputs[user] {
				actual := validator.Validate(user, password)
				if diff := cmp.Diff(expected, actual); diff != "" {
					t.Errorf("%s: actual does not match expected, diff: %s", tc.name, diff)
				}
			}
		}
	}
}

func someHandlerFunc() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		if _, err := io.WriteString(w, "OK"); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
		}
	}
}

func TestLoginHandler(t *testing.T) {
	v := &literal{username: "a", password: func() []byte { return []byte("b") }}

	testCases := []struct {
		name               string
		username           string
		password           string
		validator          validator
		next               http.Handler
		expectedStatusCode int
		expectedBody       string
	}{
		{
			name:               "no username or password",
			validator:          v,
			next:               someHandlerFunc(),
			expectedStatusCode: http.StatusUnauthorized,
			expectedBody:       "Unauthorized\n",
		},
		{
			name:               "valid a",
			validator:          v,
			next:               someHandlerFunc(),
			username:           "a",
			password:           "b",
			expectedStatusCode: http.StatusOK,
			expectedBody:       "OK",
		},
	}

	for _, tc := range testCases {
		request := httptest.NewRequest(http.MethodGet, "/result", nil)
		if tc.username != "" {
			request.SetBasicAuth(tc.username, tc.password)
		}
		responseRecorder := httptest.NewRecorder()
		loginHandler(tc.validator, tc.next).ServeHTTP(responseRecorder, request)

		if diff := cmp.Diff(tc.expectedStatusCode, responseRecorder.Code); diff != "" {
			t.Errorf("%s: actual does not match expected, diff: %s", tc.name, diff)
		}
		if diff := cmp.Diff(tc.expectedBody, responseRecorder.Body.String()); diff != "" {
			t.Errorf("%s: actual does not match expected, diff: %s", tc.name, diff)
		}
	}
}

func TestValidatePodScalerRequest(t *testing.T) {
	var testCases = []struct {
		name     string
		request  *results.PodScalerRequest
		expected error
	}{
		{
			name: "everything ok",
			request: &results.PodScalerRequest{
				WorkloadName:     "name",
				WorkloadType:     "build",
				ConfiguredAmount: "100",
				DeterminedAmount: "400",
				ResourceType:     "memory",
			},
			expected: nil,
		},
		{
			name: "empty workload name",
			request: &results.PodScalerRequest{
				WorkloadName:     "",
				ConfiguredAmount: "100",
				DeterminedAmount: "400",
				ResourceType:     "cpu",
			},
			expected: fmt.Errorf("workload_name field in request is empty"),
		},
		{
			name: "empty configured memory",
			request: &results.PodScalerRequest{
				WorkloadName:     "name",
				WorkloadType:     "build",
				ConfiguredAmount: "",
				DeterminedAmount: "400",
				ResourceType:     "memory",
			},
			expected: fmt.Errorf("configured_amount field in request is empty"),
		},
		{
			name: "empty determined memory",
			request: &results.PodScalerRequest{
				WorkloadName:     "name",
				WorkloadType:     "build",
				ConfiguredAmount: "100",
				DeterminedAmount: "",
				ResourceType:     "memory",
			},
			expected: fmt.Errorf("determined_amount field in request is empty"),
		},
		{
			name: "empty workload type",
			request: &results.PodScalerRequest{
				WorkloadName:     "name",
				WorkloadType:     "",
				ConfiguredAmount: "100",
				DeterminedAmount: "200",
				ResourceType:     "cpu",
			},
			expected: fmt.Errorf("workload_type field in request is empty"),
		},
		{
			name: "empty resource type",
			request: &results.PodScalerRequest{
				WorkloadName:     "name",
				WorkloadType:     "step",
				ConfiguredAmount: "100",
				DeterminedAmount: "200",
				ResourceType:     "",
			},
			expected: fmt.Errorf("resource_type field in request is empty"),
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			actual := validatePodScalerRequest(testCase.request)
			if diff := cmp.Diff(testCase.expected, actual, testhelper.EquateErrorMessage); diff != "" {
				t.Fatalf("actual error doesn't match expected error, diff: %v", diff)
			}
		})
	}
}
