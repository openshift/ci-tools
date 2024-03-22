package candidate

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"

	"github.com/sirupsen/logrus"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/release"
)

func ServiceHost(d api.ReleaseDescriptor) string {
	var product string
	switch d.Product {
	case api.ReleaseProductOCP:
		product = "ocp"
	case api.ReleaseProductOKD:
		product = "origin"
	}

	return fmt.Sprintf("https://%s.%s.releases.%s/api/v1/releasestream", d.Architecture, product, api.ServiceDomainCI)
}

// architecture determines the architecture in the Release Controllers' endpoints
func architecture(architecture api.ReleaseArchitecture) string {
	switch architecture {
	case api.ReleaseArchitectureAMD64:
		// default, no postfix
		return ""
	case api.ReleaseArchitecturePPC64le, api.ReleaseArchitectureS390x, api.ReleaseArchitectureARM64, api.ReleaseArchitectureMULTI:
		return "-" + string(architecture)
	}
	return ""
}

func Endpoint(d api.ReleaseDescriptor, version, stream, suffix string) string {
	return fmt.Sprintf("%s/%s%s%s%s", ServiceHost(d), version, stream, architecture(d.Architecture), suffix)
}

// endpoint determines the API endpoint to use for a candidate release
func endpoint(candidate api.Candidate) string {
	return Endpoint(candidate.ReleaseDescriptor, candidate.Version+".0-0.", string(candidate.Stream), "/latest")
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
	return ResolvePullSpecCommon(client, endpoint(DefaultFields(candidate)), nil, candidate.Relative)
}

func ResolvePullSpecCommon(client release.HTTPClient, endpoint string, bounds *api.VersionBounds, relative int) (string, error) {
	rel, err := ResolveReleaseCommon(client, endpoint, bounds, relative)
	return rel.PullSpec, err
}

func ResolveReleaseCommon(client release.HTTPClient, endpoint string, bounds *api.VersionBounds, relative int) (Release, error) {
	ret := Release{}
	req, err := http.NewRequest("GET", endpoint, nil)
	if err != nil {
		return ret, err
	}
	req.Header.Set("Accept", "application/json")
	q := req.URL.Query()
	if bounds != nil {
		q.Add("in", bounds.Query())
	}
	if relative != 0 {
		q.Add("rel", strconv.Itoa(relative))
	}
	if s := q.Encode(); s != "" {
		req.URL.RawQuery = s
	}
	logrus.Infof("Requesting a release from %s", req.URL.String())
	resp, err := client.Do(req)
	if err != nil {
		return ret, fmt.Errorf("failed to request latest release: %w", err)
	}
	if resp == nil {
		return ret, errors.New("failed to request latest release: got a nil response")
	}
	defer resp.Body.Close()
	data, readErr := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return ret, fmt.Errorf("failed to request latest release: server responded with %d: %s", resp.StatusCode, data)
	}
	if readErr != nil {
		return ret, fmt.Errorf("failed to read response body: %w", readErr)
	}
	err = json.Unmarshal(data, &ret)
	if err != nil {
		return ret, fmt.Errorf("failed to unmarshal release: %w (%s)", err, data)
	}
	return ret, nil
}
