package candidate

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/openshift/ci-tools/pkg/api"
)

func TestServiceHost(t *testing.T) {
	var testCases = []struct {
		desc   api.ReleaseDescriptor
		output string
	}{
		{
			desc: api.ReleaseDescriptor{
				Product:      api.ReleaseProductOKD,
				Architecture: api.ReleaseArchitectureAMD64,
			},
			output: "https://amd64.origin.releases.ci.openshift.org/api/v1/releasestream",
		},
		{
			desc: api.ReleaseDescriptor{
				Product:      api.ReleaseProductOCP,
				Architecture: api.ReleaseArchitectureAMD64,
			},
			output: "https://amd64.ocp.releases.ci.openshift.org/api/v1/releasestream",
		},
		{
			desc: api.ReleaseDescriptor{
				Product:      api.ReleaseProductOCP,
				Architecture: api.ReleaseArchitecturePPC64le,
			},
			output: "https://ppc64le.ocp.releases.ci.openshift.org/api/v1/releasestream",
		},
		{
			desc: api.ReleaseDescriptor{
				Product:      api.ReleaseProductOCP,
				Architecture: api.ReleaseArchitectureS390x,
			},
			output: "https://s390x.ocp.releases.ci.openshift.org/api/v1/releasestream",
		},
		{
			desc: api.ReleaseDescriptor{
				Product:      api.ReleaseProductOCP,
				Architecture: api.ReleaseArchitectureARM64,
			},
			output: "https://arm64.ocp.releases.ci.openshift.org/api/v1/releasestream",
		},
		{
			desc: api.ReleaseDescriptor{
				Product:      api.ReleaseProductOCP,
				Architecture: api.ReleaseArchitectureMULTI,
			},
			output: "https://multi.ocp.releases.ci.openshift.org/api/v1/releasestream",
		},
	}

	for _, testCase := range testCases {
		if actual, expected := ServiceHost(testCase.desc), testCase.output; actual != expected {
			t.Errorf("got incorrect service host: %v", cmp.Diff(actual, expected))
		}
	}
}

func TestEndpoint(t *testing.T) {
	var testCases = []struct {
		input  api.Candidate
		output string
	}{
		{
			input: api.Candidate{
				ReleaseDescriptor: api.ReleaseDescriptor{
					Product:      api.ReleaseProductOKD,
					Architecture: api.ReleaseArchitectureAMD64,
				},
				Stream:  api.ReleaseStreamOKD,
				Version: "4.4",
			},
			output: "https://amd64.origin.releases.ci.openshift.org/api/v1/releasestream/4.4.0-0.okd/latest",
		},
		{
			input: api.Candidate{
				ReleaseDescriptor: api.ReleaseDescriptor{
					Product:      api.ReleaseProductOCP,
					Architecture: api.ReleaseArchitectureAMD64,
				},
				Stream:  api.ReleaseStreamCI,
				Version: "4.5",
			},
			output: "https://amd64.ocp.releases.ci.openshift.org/api/v1/releasestream/4.5.0-0.ci/latest",
		},
		{
			input: api.Candidate{
				ReleaseDescriptor: api.ReleaseDescriptor{
					Product:      api.ReleaseProductOCP,
					Architecture: api.ReleaseArchitectureAMD64,
				},
				Stream:  api.ReleaseStreamNightly,
				Version: "4.6",
			},
			output: "https://amd64.ocp.releases.ci.openshift.org/api/v1/releasestream/4.6.0-0.nightly/latest",
		},
		{
			input: api.Candidate{
				ReleaseDescriptor: api.ReleaseDescriptor{
					Product:      api.ReleaseProductOCP,
					Architecture: api.ReleaseArchitecturePPC64le,
				},
				Stream:  api.ReleaseStreamCI,
				Version: "4.7",
			},
			output: "https://ppc64le.ocp.releases.ci.openshift.org/api/v1/releasestream/4.7.0-0.ci-ppc64le/latest",
		},
		{
			input: api.Candidate{
				ReleaseDescriptor: api.ReleaseDescriptor{
					Product:      api.ReleaseProductOCP,
					Architecture: api.ReleaseArchitectureS390x,
				},
				Stream:  api.ReleaseStreamNightly,
				Version: "4.8",
			},
			output: "https://s390x.ocp.releases.ci.openshift.org/api/v1/releasestream/4.8.0-0.nightly-s390x/latest",
		},
	}

	for _, testCase := range testCases {
		if actual, expected := endpoint(testCase.input), testCase.output; actual != expected {
			t.Errorf("got incorrect endpoint: %v", cmp.Diff(actual, expected))
		}
	}
}

func TestDefaultFields(t *testing.T) {
	var testCases = []struct {
		name   string
		input  api.Candidate
		output api.Candidate
	}{
		{
			name: "nothing to do",
			input: api.Candidate{
				ReleaseDescriptor: api.ReleaseDescriptor{
					Product:      api.ReleaseProductOKD,
					Architecture: api.ReleaseArchitectureAMD64,
				},
				Stream:  api.ReleaseStreamOKD,
				Version: "4.4",
			},
			output: api.Candidate{
				ReleaseDescriptor: api.ReleaseDescriptor{
					Product:      api.ReleaseProductOKD,
					Architecture: api.ReleaseArchitectureAMD64,
				},
				Stream:  api.ReleaseStreamOKD,
				Version: "4.4",
			},
		},
		{
			name: "default release stream for okd",
			input: api.Candidate{
				ReleaseDescriptor: api.ReleaseDescriptor{
					Product:      api.ReleaseProductOKD,
					Architecture: api.ReleaseArchitectureAMD64,
				},
				Version: "4.4",
			},
			output: api.Candidate{
				ReleaseDescriptor: api.ReleaseDescriptor{
					Product:      api.ReleaseProductOKD,
					Architecture: api.ReleaseArchitectureAMD64,
				},
				Stream:  api.ReleaseStreamOKD,
				Version: "4.4",
			},
		},
		{
			name: "default architecture",
			input: api.Candidate{
				ReleaseDescriptor: api.ReleaseDescriptor{
					Product: api.ReleaseProductOKD,
				},
				Stream:  api.ReleaseStreamOKD,
				Version: "4.4",
			},
			output: api.Candidate{
				ReleaseDescriptor: api.ReleaseDescriptor{
					Product:      api.ReleaseProductOKD,
					Architecture: api.ReleaseArchitectureAMD64,
				},
				Stream:  api.ReleaseStreamOKD,
				Version: "4.4",
			},
		},
	}

	for _, testCase := range testCases {
		actual, expected := DefaultFields(testCase.input), testCase.output
		if diff := cmp.Diff(actual, expected); diff != "" {
			t.Errorf("%s: got incorrect candidate: %v", testCase.name, cmp.Diff(actual, expected))
		}
	}
}

func TestResolvePullSpec(t *testing.T) {
	var testCases = []struct {
		name        string
		relative    int
		raw         []byte
		expected    string
		expectedErr bool
	}{
		{
			name:        "normal request",
			raw:         []byte(`{"name": "4.3.0-0.ci-2020-05-22-121811","phase": "Accepted","pullSpec": "registry.svc.ci.openshift.org/ocp/release:4.3.0-0.ci-2020-05-22-121811","downloadURL": "https://openshift-release-artifacts.svc.ci.openshift.org/4.3.0-0.ci-2020-05-22-121811"}`),
			expected:    "registry.svc.ci.openshift.org/ocp/release:4.3.0-0.ci-2020-05-22-121811",
			expectedErr: false,
		},
		{
			name:        "normal request with relative",
			relative:    10,
			raw:         []byte(`{"name": "4.3.0-0.ci-2020-05-22-121811","phase": "Accepted","pullSpec": "registry.svc.ci.openshift.org/ocp/release:4.3.0-0.ci-2020-05-22-121811","downloadURL": "https://openshift-release-artifacts.svc.ci.openshift.org/4.3.0-0.ci-2020-05-22-121811"}`),
			expected:    "registry.svc.ci.openshift.org/ocp/release:4.3.0-0.ci-2020-05-22-121811",
			expectedErr: false,
		},
		{
			name:        "malformed response errors",
			raw:         []byte(`{"na1":}`),
			expected:    "",
			expectedErr: true,
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Accept") != "application/json" {
					t.Error("did not get correct accept header")
					http.Error(w, "400 Bad Request", http.StatusBadRequest)
					return
				}
				if r.Method != http.MethodGet {
					t.Errorf("incorrect method to get a release: %s", r.Method)
					http.Error(w, "400 Bad Request", http.StatusBadRequest)
					return
				}
				if testCase.relative != 0 {
					if relString := r.URL.Query().Get("rel"); relString != strconv.Itoa(testCase.relative) {
						t.Errorf("incorrect relative query param: %v", relString)
						http.Error(w, "400 Bad Request", http.StatusBadRequest)
						return
					}
				}
				if _, err := w.Write(testCase.raw); err != nil {
					t.Fatalf("http server Write failed: %v", err)
				}
			}))
			defer testServer.Close()
			actual, err := ResolvePullSpecCommon(&http.Client{}, testServer.URL, nil, testCase.relative)
			if err != nil && !testCase.expectedErr {
				t.Errorf("%s: expected no error but got one: %v", testCase.name, err)
			}
			if err == nil && testCase.expectedErr {
				t.Errorf("%s: expected an error but got none", testCase.name)
			}
			if actual != testCase.expected {
				t.Errorf("%s: got incorrect pullspec: %v", testCase.name, cmp.Diff(actual, testCase.expected))
			}
		})
	}
}
