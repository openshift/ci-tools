package config

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/openshift/ci-tools/pkg/api"
)

func TestEndpoint(t *testing.T) {
	var testCases = []struct {
		input  api.Candidate
		output string
	}{
		{
			input: api.Candidate{
				ReleaseDescriptor: api.ReleaseDescriptor{
					Product:      api.ReleaseProductOCP,
					Architecture: api.ReleaseArchitectureAMD64,
				},
				Stream:  api.ReleaseStreamCI,
				Version: "4.10",
			},
			output: "https://amd64.ocp.releases.ci.openshift.org/api/v1/releasestream/4.10.0-0.ci/config",
		},
		{
			input: api.Candidate{
				ReleaseDescriptor: api.ReleaseDescriptor{
					Product:      api.ReleaseProductOCP,
					Architecture: api.ReleaseArchitectureAMD64,
				},
				Stream:  api.ReleaseStreamNightly,
				Version: "4.10",
			},
			output: "https://amd64.ocp.releases.ci.openshift.org/api/v1/releasestream/4.10.0-0.nightly/config",
		},
	}

	for _, testCase := range testCases {
		if actual, expected := endpoint(testCase.input), testCase.output; actual != expected {
			t.Errorf("got incorrect endpoint: %v", cmp.Diff(actual, expected))
		}
	}
}
