package candidate

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"strconv"

	"github.com/openshift/ci-tools/pkg/api"
)

func ServiceHost(product api.ReleaseProduct, arch api.ReleaseArchitecture) string {
	var prefix string
	switch product {
	case api.ReleaseProductOCP:
		prefix = "openshift-"
	case api.ReleaseProductOKD:
		prefix = "origin-"
	}

	postfix := architecture(arch)
	return fmt.Sprintf("https://%srelease%s.%s/api/v1/releasestream", prefix, postfix, api.ServiceDomainAPICI)
}

func architecture(architecture api.ReleaseArchitecture) string {
	switch architecture {
	case api.ReleaseArchitectureAMD64:
		// default, no postfix
		return ""
	case api.ReleaseArchitecturePPC64le, api.ReleaseArchitectureS390x:
		return "-" + string(architecture)
	}
	return ""
}

// endpoint determines the API endpoint to use for a candidate release
func endpoint(candidate api.Candidate) string {
	return fmt.Sprintf("%s/%s.0-0.%s%s/latest", ServiceHost(candidate.Product, candidate.Architecture), candidate.Version, candidate.Stream, architecture(candidate.Architecture))
}

func defaultFields(candidate api.Candidate) api.Candidate {
	if candidate.Product == api.ReleaseProductOKD && candidate.Stream == "" {
		candidate.Stream = api.ReleaseStreamOKD
	}

	if candidate.Architecture == "" {
		candidate.Architecture = api.ReleaseArchitectureAMD64
	}

	return candidate
}

// ResolvePullSpec determines the pull spec for the candidate release
func ResolvePullSpec(candidate api.Candidate) (string, error) {
	return resolvePullSpec(endpoint(defaultFields(candidate)), candidate.Relative)
}

func resolvePullSpec(endpoint string, relative int) (string, error) {
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
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to request latest release: %v", err)
	}
	if resp == nil {
		return "", errors.New("failed to request latest release: got a nil response")
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to request latest release: server responded with %d: %s", resp.StatusCode, resp.Body)
	}
	data, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response body: %s", err)
	}
	release := Release{}
	err = json.Unmarshal(data, &release)
	if err != nil {
		return "", fmt.Errorf("failed to unmarshal release: %s (%s)", err, data)
	}
	return release.PullSpec, nil
}
