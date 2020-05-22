package prerelease

import (
	"encoding/json"
	"fmt"
	"github.com/openshift/ci-tools/pkg/release/candidate"
	"io/ioutil"
	"net/http"

	"github.com/openshift/ci-tools/pkg/api"
)

// endpoint determines the API endpoint to use for a prerelease
func endpoint(prerelease api.Prerelease) string {
	return fmt.Sprintf("%s/4-stable/latest", candidate.ServiceHost(prerelease.Product, prerelease.Architecture))
}

func defaultFields(prerelease api.Prerelease) api.Prerelease {
	out := prerelease

	if out.Architecture == "" {
		out.Architecture = api.ReleaseArchitectureAMD64
	}

	return out
}

// ResolvePullSpec determines the pull spec for the candidate release
func ResolvePullSpec(prerelease api.Prerelease) (string, error) {
	return resolvePullSpec(endpoint(defaultFields(prerelease)), prerelease.VersionBounds)
}

func resolvePullSpec(endpoint string, bounds api.VersionBounds) (string, error) {
	req, err := http.NewRequest("GET", endpoint, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/json")
	q := req.URL.Query()
	q.Add("in", bounds.Query())
	req.URL.RawQuery = q.Encode()
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to request latest release: %v", err)
	}
	data, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response body: %s", err)
	}
	release := candidate.Release{}
	err = json.Unmarshal(data, &release)
	if err != nil {
		return "", fmt.Errorf("failed to unmarshal release: %s (%s)", err, data)
	}
	return release.PullSpec, nil
}
