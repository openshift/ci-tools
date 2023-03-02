package prerelease

import (
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
