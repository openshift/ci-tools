package prerelease

import (
	"fmt"

	"github.com/blang/semver"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/release"
	"github.com/openshift/ci-tools/pkg/release/candidate"
)

// endpoint determines the API endpoint to use for a prerelease
func endpoint(prerelease api.Prerelease) string {
	stream := prerelease.VersionBounds.Stream
	if stream == "" {
		stream = "4-stable"
	}
	return candidate.Endpoint(prerelease.ReleaseDescriptor, "", stream, "/latest")
}

func defaultFields(prerelease api.Prerelease) api.Prerelease {
	if prerelease.Architecture == "" {
		prerelease.Architecture = api.ReleaseArchitectureAMD64
	}
	return prerelease
}

// ResolvePullSpec determines the pull spec for the candidate release
func ResolvePullSpec(client release.HTTPClient, prerelease api.Prerelease) (string, error) {
	return resolvePullSpec(client, endpoint(defaultFields(prerelease)), prerelease.VersionBounds, prerelease.Relative)
}

func resolvePullSpec(client release.HTTPClient, endpoint string, bounds api.VersionBounds, relative int) (string, error) {
	return candidate.ResolvePullSpecCommon(client, endpoint, &bounds, relative)
}

func stable4Latest(client release.HTTPClient) (string, error) {
	endpoint := endpoint(api.Prerelease{ReleaseDescriptor: api.ReleaseDescriptor{Product: api.ReleaseProductOCP, Architecture: api.ReleaseArchitectureAMD64}})
	rel, err := candidate.ResolveReleaseCommon(client, endpoint, nil, 0, true)
	return rel.Name, err
}

// Stable4LatestMajorMinor returns the release name for stable-4 stream without the patch in it
// E.g, return 4.15 if the stable latest is 4.15.1
func Stable4LatestMajorMinor(client release.HTTPClient) (string, error) {
	version, err := stable4Latest(client)
	if err != nil {
		return "", err
	}
	sv, err := semver.Make(version)
	if err != nil {
		return "", fmt.Errorf("failed to make sematic version from %s: %w", version, err)
	}
	return fmt.Sprintf("%d.%d", sv.Major, sv.Minor), nil
}
