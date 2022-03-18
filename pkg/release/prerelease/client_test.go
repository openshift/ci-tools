package prerelease

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/openshift/ci-tools/pkg/api"
)

func TestEndpoint(t *testing.T) {
	var testCases = []struct {
		input  api.Prerelease
		output string
	}{
		{
			input: api.Prerelease{
				Product:      api.ReleaseProductOKD,
				Architecture: api.ReleaseArchitectureAMD64,
			},
			output: "https://amd64.origin.releases.ci.openshift.org/api/v1/releasestream/4-stable/latest",
		},
		{
			input: api.Prerelease{
				Product:      api.ReleaseProductOCP,
				Architecture: api.ReleaseArchitectureAMD64,
			},
			output: "https://amd64.ocp.releases.ci.openshift.org/api/v1/releasestream/4-stable/latest",
		},
		{
			input: api.Prerelease{
				Product:      api.ReleaseProductOCP,
				Architecture: api.ReleaseArchitecturePPC64le,
			},
			output: "https://ppc64le.ocp.releases.ci.openshift.org/api/v1/releasestream/4-stable-ppc64le/latest",
		},
		{
			input: api.Prerelease{
				Product:      api.ReleaseProductOCP,
				Architecture: api.ReleaseArchitectureS390x,
			},
			output: "https://s390x.ocp.releases.ci.openshift.org/api/v1/releasestream/4-stable-s390x/latest",
		},
		{
			input: api.Prerelease{
				Product:      api.ReleaseProductOCP,
				Architecture: api.ReleaseArchitectureARM64,
			},
			output: "https://arm64.ocp.releases.ci.openshift.org/api/v1/releasestream/4-stable-arm64/latest",
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
		input  api.Prerelease
		output api.Prerelease
	}{
		{
			name: "nothing to do",
			input: api.Prerelease{
				Product:      api.ReleaseProductOKD,
				Architecture: api.ReleaseArchitectureAMD64,
				VersionBounds: api.VersionBounds{
					Lower: "4.4.0",
					Upper: "4.5.0-0",
				},
			},
			output: api.Prerelease{
				Product:      api.ReleaseProductOKD,
				Architecture: api.ReleaseArchitectureAMD64,
				VersionBounds: api.VersionBounds{
					Lower: "4.4.0",
					Upper: "4.5.0-0",
				},
			},
		},
		{
			name: "default architecture",
			input: api.Prerelease{
				Product: api.ReleaseProductOKD,
				VersionBounds: api.VersionBounds{
					Lower: "4.4.0",
					Upper: "4.5.0-0",
				},
			},
			output: api.Prerelease{
				Product:      api.ReleaseProductOKD,
				Architecture: api.ReleaseArchitectureAMD64,
				VersionBounds: api.VersionBounds{
					Lower: "4.4.0",
					Upper: "4.5.0-0",
				},
			},
		},
	}

	for _, testCase := range testCases {
		actual, expected := defaultFields(testCase.input), testCase.output
		if diff := cmp.Diff(actual, expected); diff != "" {
			t.Errorf("%s: got incorrect prerelease: %v", testCase.name, cmp.Diff(actual, expected))
		}
	}
}

func TestResolvePullSpec(t *testing.T) {
	var testCases = []struct {
		name          string
		versionBounds api.VersionBounds
		raw           []byte
		expected      string
		expectedErr   bool
	}{
		{
			name: "normal request",
			versionBounds: api.VersionBounds{
				Lower: "4.4.0",
				Upper: "4.5.0-0",
			},
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
				if bounds := r.URL.Query().Get("in"); bounds != testCase.versionBounds.Query() {
					t.Errorf("incorrect version bounds param: %v", bounds)
					http.Error(w, "400 Bad Request", http.StatusBadRequest)
					return
				}
				if _, err := w.Write(testCase.raw); err != nil {
					t.Errorf("failed to write response: %v", err)
				}
			}))
			defer testServer.Close()
			actual, err := resolvePullSpec(&http.Client{}, testServer.URL, testCase.versionBounds)
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
