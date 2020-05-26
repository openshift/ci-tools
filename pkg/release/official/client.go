package official

import (
	"encoding/json"
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

// ResolvePullSpec determines the pull spec for the official release
func ResolvePullSpec(release api.Release) (string, error) {
	return resolvePullSpec(cincinnatiAddress, defaultFields(release))
}

func resolvePullSpec(endpoint string, release api.Release) (string, error) {
	req, err := http.NewRequest("GET", endpoint, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/json")
	query := req.URL.Query()
	query.Add("channel", fmt.Sprintf("%s-%s", release.Channel, release.Version))
	query.Add("arch", string(release.Architecture))
	req.URL.RawQuery = query.Encode()
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to request latest release: %v", err)
	}
	data, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response body: %s", err)
	}
	response := Response{}
	err = json.Unmarshal(data, &response)
	if err != nil {
		return "", fmt.Errorf("failed to unmarshal response: %s", err)
	}
	return latestPullSpec(response.Nodes), nil
}

// latestPullSpec returns the pullSpec of the latest release in the list
func latestPullSpec(options []Release) string {
	sort.Slice(options, func(i, j int) bool {
		vi := semver.MustParse(options[i].Version)
		vj := semver.MustParse(options[j].Version)
		return vi.GTE(vj) // greater, not less, so we get descending order
	})
	return options[0].Payload
}
