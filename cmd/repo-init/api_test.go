package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/openshift/ci-tools/pkg/api"
)

func TestClusterProfiles(t *testing.T) {
	r, err := http.NewRequest(http.MethodGet, "wordup.com", &bytes.Buffer{})
	if err != nil {
		t.Fatalf("could not make request: %v", err)
	}

	writer := &fakeWriter{}
	clusterProfileHandler()(writer, r)

	expected, _ := json.Marshal(getClusterProfiles())
	if diff := cmp.Diff(writer.body, expected); diff != "" {
		t.Fatalf("unexpected response %v", diff)
	}
}

func TestConfigValidation(t *testing.T) {
	testCases := []struct {
		name           string
		data           interface{}
		validationType validationType

		expected *validationResponse
	}{
		{
			name: "Validate whole config - valid",
			data: ConfigValidationRequest{
				initConfig{
					Org:                   "org",
					Repo:                  "repo",
					Branch:                "branch",
					CanonicalGoRepository: "sometimes.com",
					GoVersion:             "1",
					Tests: []test{
						{As: "unit", Command: "make test-unit", From: "src"},
						{As: "cmd", Command: "make test-cmd", From: "bin"},
					},
					CustomE2E: []e2eTest{
						{As: "operator-e2e", Command: "make e2e", Profile: "aws"},
						{As: "operator-e2e-gcp", Command: "make e2e", Profile: "gcp", Cli: true},
					},
				},
			},
			validationType: All,
			expected: &validationResponse{
				Valid: true,
			},
		},
		{
			name: "Validate whole config - Invalid - no tests",
			data: ConfigValidationRequest{
				initConfig{
					Org:                   "org",
					Repo:                  "repo",
					Branch:                "branch",
					CanonicalGoRepository: "sometimes.com",
					GoVersion:             "1",
				},
			},
			validationType: All,
			expected: &validationResponse{
				Valid: false,
				ValidationErrors: []validationError{
					{
						Key:     "generic",
						Message: "invalid configuration: you must define at least one test or image build in 'tests' or 'images'",
					},
				},
			},
		},
		{
			name: "Validate operator substitution - valid",
			data: SubstitutionValidationRequest{
				ConfigValidationRequest: ConfigValidationRequest{
					initConfig{
						Org:                   "org",
						Repo:                  "repo",
						Branch:                "branch",
						CanonicalGoRepository: "sometimes.com",
						GoVersion:             "1",
						OperatorBundle: &operatorBundle{
							DockerfilePath: "Dockerfile.bundle",
							Name:           "ci-index",
						},
					},
				},
				Substitution: api.PullSpecSubstitution{
					PullSpec: "test.io/pullspec:4.1",
					With:     "image1",
				},
			},
			validationType: OperatorSubstitution,
			expected: &validationResponse{
				Valid: true,
			},
		},
		{
			name: "Validate operator substitution - invalid",
			data: SubstitutionValidationRequest{
				ConfigValidationRequest: ConfigValidationRequest{
					initConfig{
						Org:                   "org",
						Repo:                  "repo",
						Branch:                "branch",
						CanonicalGoRepository: "sometimes.com",
						GoVersion:             "1",
						OperatorBundle: &operatorBundle{
							DockerfilePath: "Dockerfile.bundle",
							Name:           "ci-index",
						},
					},
				},
				Substitution: api.PullSpecSubstitution{
					PullSpec: "test.io/pullspec:4.1",
					With:     "thisaintright:image1",
				},
			},
			validationType: OperatorSubstitution,
			expected: &validationResponse{
				Valid: false,
				ValidationErrors: []validationError{
					{
						Field:   "operator_substitution",
						Message: "with: could not resolve 'thisaintright:image1' to an image involved in the config",
					},
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			data, _ := json.Marshal(tc.data)
			marshalled, _ := json.Marshal(ValidationRequest{
				ValidationType: tc.validationType,
				Data:           data,
			})
			body := bytes.NewBuffer(marshalled)

			r, err := http.NewRequest(http.MethodGet, "wordup.com", body)
			if err != nil {
				t.Fatalf("could not make request: %v", err)
			}

			writer := &fakeWriter{}

			validateConfig(writer, r)

			actual := &validationResponse{}
			_ = json.Unmarshal(writer.body, actual)
			if diff := cmp.Diff(tc.expected, actual); diff != "" {
				t.Errorf("%s: got invalid response: %v", tc.name, diff)
			}
		})
	}

}

type fakeWriter struct {
	status int
	body   []byte
}

func (w *fakeWriter) Header() http.Header { return nil }
func (w *fakeWriter) Write(data []byte) (int, error) {
	w.body = data
	return 0, nil
}

func (w *fakeWriter) WriteHeader(statusCode int) {
	w.status = statusCode
}
