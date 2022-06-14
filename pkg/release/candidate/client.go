package candidate

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"strconv"

	"github.com/sirupsen/logrus"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/release"
)

func ServiceHost(releaseProduct api.ReleaseProduct, arch api.ReleaseArchitecture) string {
	var product string
	switch releaseProduct {
	case api.ReleaseProductOCP:
		product = "ocp"
	case api.ReleaseProductOKD:
		product = "origin"
	}

	return fmt.Sprintf("https://%s.%s.releases.%s/api/v1/releasestream", arch, product, api.ServiceDomainCI)
}

// Architecture determines the architecture in the Release Controllers' endpoints
func Architecture(architecture api.ReleaseArchitecture) string {
	switch architecture {
	case api.ReleaseArchitectureAMD64:
		// default, no postfix
		return ""
	case api.ReleaseArchitecturePPC64le, api.ReleaseArchitectureS390x, api.ReleaseArchitectureARM64, api.ReleaseArchitectureMULTI:
		return "-" + string(architecture)
	}
	return ""
}

// endpoint determines the API endpoint to use for a candidate release
func endpoint(candidate api.Candidate) string {
	return fmt.Sprintf("%s/%s.0-0.%s%s/latest", ServiceHost(candidate.Product, candidate.Architecture), candidate.Version, candidate.Stream, Architecture(candidate.Architecture))
}

// DefaultFields add default values to the fields of candidate
func DefaultFields(candidate api.Candidate) api.Candidate {
	if candidate.Product == api.ReleaseProductOKD && candidate.Stream == "" {
		candidate.Stream = api.ReleaseStreamOKD
	}

	if candidate.Architecture == "" {
		candidate.Architecture = api.ReleaseArchitectureAMD64
	}

	return candidate
}

// ResolvePullSpec determines the pull spec for the candidate release
func ResolvePullSpec(client release.HTTPClient, candidate api.Candidate) (string, error) {
	return resolvePullSpec(client, endpoint(DefaultFields(candidate)), candidate.Relative)
}

func resolvePullSpec(client release.HTTPClient, endpoint string, relative int) (string, error) {
	req, err := http.NewRequest("GET", endpoint, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/json")
	if relative != 0 {
		q := req.URL.Query()
		q.Add("rel", strconv.Itoa(relative))
		req.URL.RawQuery = q.Encode()
	}
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
	release := Release{}
	err = json.Unmarshal(data, &release)
	if err != nil {
		return "", fmt.Errorf("failed to unmarshal release: %w (%s)", err, data)
	}
	return release.PullSpec, nil
}
