package prerelease

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"

	"github.com/sirupsen/logrus"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/release"
	"github.com/openshift/ci-tools/pkg/release/candidate"
)

// endpoint determines the API endpoint to use for a prerelease
func endpoint(prerelease api.Prerelease) string {
	return fmt.Sprintf("%s/4-stable%s/latest", candidate.ServiceHost(prerelease.Product, prerelease.Architecture), candidate.Architecture(prerelease.Architecture))
}

func defaultFields(prerelease api.Prerelease) api.Prerelease {
	if prerelease.Architecture == "" {
		prerelease.Architecture = api.ReleaseArchitectureAMD64
	}
	return prerelease
}

// ResolvePullSpec determines the pull spec for the candidate release
func ResolvePullSpec(client release.HTTPClient, prerelease api.Prerelease) (string, error) {
	return resolvePullSpec(client, endpoint(defaultFields(prerelease)), prerelease.VersionBounds)
}

func resolvePullSpec(client release.HTTPClient, endpoint string, bounds api.VersionBounds) (string, error) {
	req, err := http.NewRequest("GET", endpoint, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/json")
	q := req.URL.Query()
	q.Add("in", bounds.Query())
	req.URL.RawQuery = q.Encode()
	logrus.Debugf("Requesting a release from %s", req.URL.String())
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to request latest release: %w", err)
	}
	if resp == nil {
		return "", errors.New("failed to request latest release: got a nil response")
	}
	defer resp.Body.Close()
	data, readErr := ioutil.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to request latest release: server responded with %d: %s", resp.StatusCode, data)
	}
	if readErr != nil {
		return "", fmt.Errorf("failed to read response body: %w", readErr)
	}
	release := candidate.Release{}
	err = json.Unmarshal(data, &release)
	if err != nil {
		return "", fmt.Errorf("failed to unmarshal release: %w (%s)", err, data)
	}
	return release.PullSpec, nil
}
