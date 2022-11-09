package official

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"regexp"
	"sort"
	"strings"

	"github.com/blang/semver"
	"github.com/sirupsen/logrus"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/release"
)

const cincinnatiAddress = "https://api.openshift.com/api/upgrades_info/v1/graph"

// majorMinorRegExp allows for parsing major and minor versions from SemVer values.
var majorMinorRegExp = regexp.MustCompile(`^(?P<majorMinor>(?P<major>0|[1-9]\d*)\.(?P<minor>0|[1-9]\d*))\.?.*`)

func defaultFields(release api.Release) api.Release {
	if release.Architecture == "" {
		release.Architecture = api.ReleaseArchitectureAMD64
	}
	return release
}

// ResolvePullSpecAndVersion determines the pull spec and version for the official release
func ResolvePullSpecAndVersion(client release.HTTPClient, release api.Release) (string, string, error) {
	return resolvePullSpec(client, cincinnatiAddress, defaultFields(release))
}

func resolvePullSpec(client release.HTTPClient, endpoint string, release api.Release) (string, string, error) {
	req, err := http.NewRequest("GET", endpoint, nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Accept", "application/json")
	query := req.URL.Query()
	explicitVersion, channel, err := processVersionChannel(release.Version, release.Channel)
	if err != nil {
		return "", "", err
	}
	targetName := "latest release"
	if !explicitVersion {
		targetName = release.Version
	}
	query.Add("channel", channel)
	query.Add("arch", string(release.Architecture))
	req.URL.RawQuery = query.Encode()
	logrus.Infof("Requesting %s from %s", targetName, req.URL.String())
	resp, err := client.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("failed to request %s: %w", targetName, err)
	}
	if resp == nil {
		return "", "", fmt.Errorf("failed to request %s: got a nil response", targetName)
	}
	defer resp.Body.Close()
	data, readErr := ioutil.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("failed to request %s: server responded with %d: %s", targetName, resp.StatusCode, data)
	}
	if readErr != nil {
		return "", "", fmt.Errorf("failed to read response body: %w", readErr)
	}
	response := Response{}
	err = json.Unmarshal(data, &response)
	if err != nil {
		return "", "", fmt.Errorf("failed to unmarshal response: %w", err)
	}
	if len(response.Nodes) == 0 {
		return "", "", fmt.Errorf("failed to request %s from %s: server returned empty list of releases (despite status code 200)", targetName, req.URL.String())
	}

	if explicitVersion {
		for _, node := range response.Nodes {
			if node.Version == release.Version {
				return node.Payload, node.Version, nil
			}
		}
		return "", "", fmt.Errorf("failed to request %s from %s: version not found in list of releases", release.Version, req.URL.String())
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

// processVersionChannel takes the configured version and channel and
// returns:
//
//   - Whether the version is explicit (e.g. 4.7.0) or just a
//     major.minor (e.g. 4.7).
//   - The appropriate channel for a Cincinnati request, e.g. stable-4.7.
//   - Any errors that turn up while processing.
func processVersionChannel(version string, channel api.ReleaseChannel) (explicitVersion bool, cincinnatiChannel string, err error) {
	explicitVersion, majorMinor, err := extractMajorMinor(version)
	if err != nil {
		return false, "", err
	}
	if strings.HasSuffix(string(channel), fmt.Sprintf("-%s", majorMinor)) {
		return explicitVersion, string(channel), nil
	}

	return explicitVersion, fmt.Sprintf("%s-%s", channel, majorMinor), nil
}

func ExtractMajorMinor(version string) (string, error) {
	_, majorMinor, err := extractMajorMinor(version)
	return majorMinor, err
}

func extractMajorMinor(version string) (explicitVersion bool, majorMinor string, err error) {
	majorMinorMatch := majorMinorRegExp.FindStringSubmatch(version)
	if majorMinorMatch == nil {
		return false, "", fmt.Errorf("version %q does not begin with a major.minor version", version)
	}

	majorMinorIndex := majorMinorRegExp.SubexpIndex("majorMinor")
	majorMinor = majorMinorMatch[majorMinorIndex]
	explicitVersion = version != majorMinor

	return explicitVersion, majorMinor, nil
}
