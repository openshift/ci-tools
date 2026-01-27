package prerelease

import (
	"fmt"
	"strings"

	"github.com/blang/semver"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/release"
	"github.com/openshift/ci-tools/pkg/release/candidate"
)

func endpoint(prerelease api.Prerelease) string {
	stream := prerelease.VersionBounds.Stream
	if stream == "" {
		stream = deriveStreamFromBounds(prerelease.VersionBounds)
	}
	return candidate.Endpoint(prerelease.ReleaseDescriptor, "", stream, "/latest")
}

func deriveStreamFromBounds(bounds api.VersionBounds) string {
	for _, version := range []string{bounds.Lower, bounds.Upper} {
		if version == "" {
			continue
		}
		parts := strings.Split(version, ".")
		if len(parts) >= 1 && parts[0] != "" {
			return fmt.Sprintf("%s-stable", parts[0])
		}
	}
	return "4-stable"
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

func stableLatest(client release.HTTPClient, stream string) (string, error) {
	ep := candidate.Endpoint(
		api.ReleaseDescriptor{Product: api.ReleaseProductOCP, Architecture: api.ReleaseArchitectureAMD64},
		"", stream, "/latest",
	)
	rel, err := candidate.ResolveReleaseCommon(client, ep, nil, 0, true)
	return rel.Name, err
}

// StableLatestMajorMinor returns the latest major.minor from the specified stable stream.
func StableLatestMajorMinor(client release.HTTPClient, stream string) (string, error) {
	version, err := stableLatest(client, stream)
	if err != nil {
		return "", err
	}
	sv, err := semver.Make(version)
	if err != nil {
		return "", fmt.Errorf("failed to make semantic version from %s: %w", version, err)
	}
	return fmt.Sprintf("%d.%d", sv.Major, sv.Minor), nil
}
