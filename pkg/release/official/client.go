package official

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"sort"

	"github.com/blang/semver"

	"github.com/openshift/ci-tools/pkg/api"
)

const cincinnatiAddress = "https://api.openshift.com/api/upgrades_info/v1/graph"

func defaultFields(release api.Release) api.Release {
	if release.Architecture == "" {
		release.Architecture = api.ReleaseArchitectureAMD64
	}
	return release
}

// ResolvePullSpecAndVersion determines the pull spec and version for the official release
func ResolvePullSpecAndVersion(release api.Release) (string, string, error) {
	return resolvePullSpec(cincinnatiAddress, defaultFields(release))
}

func resolvePullSpec(endpoint string, release api.Release) (string, string, error) {
	req, err := http.NewRequest("GET", endpoint, nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Accept", "application/json")
	query := req.URL.Query()
	query.Add("channel", fmt.Sprintf("%s-%s", release.Channel, release.Version))
	query.Add("arch", string(release.Architecture))
	req.URL.RawQuery = query.Encode()
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("failed to request latest release: %w", err)
	}
	if resp == nil {
		return "", "", errors.New("failed to request latest release: got a nil response")
	}
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("failed to request latest release: server responded with %d: %s", resp.StatusCode, resp.Body)
	}
	data, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return "", "", fmt.Errorf("failed to read response body: %w", err)
	}
	response := Response{}
	err = json.Unmarshal(data, &response)
	if err != nil {
		return "", "", fmt.Errorf("failed to unmarshal response: %w", err)
	}
	if len(response.Nodes) == 0 {
		return "", "", errors.New("failed to request latest release: server returned empty list of releases (despite status code 200)")
	}
	pullspec, version := latestPullSpecAndVersion(response.Nodes)
	return pullspec, version, nil
}

// latestPullSpecAndVersion returns the pullSpec of the latest release in the list as a payload and version
func latestPullSpecAndVersion(options []Release) (string, string) {
	sort.Slice(options, func(i, j int) bool {
		vi := semver.MustParse(options[i].Version)
		vj := semver.MustParse(options[j].Version)
		return vi.GTE(vj) // greater, not less, so we get descending order
	})
	return options[0].Payload, options[0].Version
}
