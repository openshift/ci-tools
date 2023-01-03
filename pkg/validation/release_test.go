package validation

import (
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/openshift/ci-tools/pkg/api"
)

func TestValidateReleases(t *testing.T) {
	var testCases = []struct {
		name       string
		input      map[string]api.UnresolvedRelease
		hasTagSpec bool
		output     []error
	}{
		{
			name:  "no releases",
			input: map[string]api.UnresolvedRelease{},
		},
		{
			name: "valid releases",
			input: map[string]api.UnresolvedRelease{
				"first": {
					Candidate: &api.Candidate{
						ReleaseDescriptor: api.ReleaseDescriptor{
							Product:      api.ReleaseProductOKD,
							Architecture: api.ReleaseArchitectureAMD64,
						},
						Stream:  api.ReleaseStreamOKD,
						Version: "4.4",
					},
				},
				"second": {
					Release: &api.Release{
						Architecture: api.ReleaseArchitectureAMD64,
						Channel:      api.ReleaseChannelCandidate,
						Version:      "4.4",
					},
				},
				"third": {
					Prerelease: &api.Prerelease{
						ReleaseDescriptor: api.ReleaseDescriptor{
							Product:      api.ReleaseProductOCP,
							Architecture: api.ReleaseArchitectureS390x,
						},
						VersionBounds: api.VersionBounds{
							Lower: "4.1.0",
							Upper: "4.2.0",
						},
					},
				},
			},
		},
		{
			name: "invalid use of latest release with tag spec",
			input: map[string]api.UnresolvedRelease{
				"latest": {
					Candidate: &api.Candidate{
						ReleaseDescriptor: api.ReleaseDescriptor{
							Product:      api.ReleaseProductOKD,
							Architecture: api.ReleaseArchitectureAMD64,
						},
						Stream:  api.ReleaseStreamOKD,
						Version: "4.4",
					},
				},
			},
			hasTagSpec: true,
			output: []error{
				errors.New("root.latest: cannot request resolving a(n) latest release and set tag_specification"),
			},
		},
		{
			name: "invalid use of initial release with tag spec",
			input: map[string]api.UnresolvedRelease{
				"initial": {
					Candidate: &api.Candidate{
						ReleaseDescriptor: api.ReleaseDescriptor{
							Product:      api.ReleaseProductOKD,
							Architecture: api.ReleaseArchitectureAMD64,
						},
						Stream:  api.ReleaseStreamOKD,
						Version: "4.4",
					},
				},
			},
			hasTagSpec: true,
			output: []error{
				errors.New("root.initial: cannot request resolving a(n) initial release and set tag_specification"),
			},
		},
		{
			name: "invalid release with no options set",
			input: map[string]api.UnresolvedRelease{
				"latest": {},
			},
			output: []error{
				errors.New("root.latest: must set integration, candidate, prerelease or release"),
			},
		},
		{
			name: "invalid release with two options set",
			input: map[string]api.UnresolvedRelease{
				"latest": {
					Candidate: &api.Candidate{},
					Release:   &api.Release{},
				},
			},
			output: []error{
				errors.New("root.latest: cannot set more than one of integration, candidate, prerelease and release"),
			},
		},
		{
			name: "invalid release with all options set",
			input: map[string]api.UnresolvedRelease{
				"latest": {
					Candidate:  &api.Candidate{},
					Release:    &api.Release{},
					Prerelease: &api.Prerelease{},
				},
			},
			output: []error{
				errors.New("root.latest: cannot set more than one of integration, candidate, prerelease and release"),
			},
		},
		{
			name: "invalid releases",
			input: map[string]api.UnresolvedRelease{
				"first": {
					Candidate: &api.Candidate{
						ReleaseDescriptor: api.ReleaseDescriptor{
							Product:      "bad",
							Architecture: api.ReleaseArchitectureAMD64,
						},
						Stream:  api.ReleaseStreamOKD,
						Version: "4.4",
					},
				},
				"second": {
					Release: &api.Release{
						Architecture: api.ReleaseArchitectureAMD64,
						Channel:      "ew",
						Version:      "4.4",
					},
				},
				"third": {
					Prerelease: &api.Prerelease{
						ReleaseDescriptor: api.ReleaseDescriptor{
							Product:      api.ReleaseProductOCP,
							Architecture: api.ReleaseArchitectureS390x,
						},
						VersionBounds: api.VersionBounds{
							Lower: "4.1.0",
						},
					},
				},
			},
			hasTagSpec: true,
			output: []error{
				errors.New("root.first.product: must be one of ocp, okd"),
				errors.New("root.second.channel: must be one of candidate, fast, stable"),
				errors.New("root.third.version_bounds.upper: must be set"),
			},
		},
		{
			name: "invalid release name",
			input: map[string]api.UnresolvedRelease{
				"ocp-4.11": {
					Integration: &api.Integration{
						Name:      "4.11",
						Namespace: "ocp",
					},
				},
				"ocp-4-11": {
					Integration: &api.Integration{
						Name:      "4.11",
						Namespace: "ocp",
					},
				},
				"e2e-hub-4.9": {
					Candidate: &api.Candidate{
						ReleaseDescriptor: api.ReleaseDescriptor{
							Product: api.ReleaseProductOCP,
						},
						Stream:  api.ReleaseStreamNightly,
						Version: "4.8",
					},
				},
			},
			output: []error{
				fmt.Errorf("root[e2e-hub-4.9]: the release name is not valid: %w", fmt.Errorf("must not contain '.'")),
				fmt.Errorf("root[ocp-4.11]: the release name is not valid: %w", fmt.Errorf("must not contain '.'")),
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if actual, expected := validateReleases("root", testCase.input, testCase.hasTagSpec), testCase.output; !reflect.DeepEqual(actual, expected) {
				t.Errorf("%s: got incorrect errors: %s", testCase.name, cmp.Diff(actual, expected, cmp.Comparer(func(x, y error) bool {
					return x.Error() == y.Error()
				})))
			}
		})
	}
}

func TestValidateCandidate(t *testing.T) {
	var testCases = []struct {
		name   string
		input  api.Candidate
		output []error
	}{
		{
			name: "valid candidate",
			input: api.Candidate{
				ReleaseDescriptor: api.ReleaseDescriptor{
					Product:      api.ReleaseProductOKD,
					Architecture: api.ReleaseArchitectureAMD64,
					Relative:     10,
				},
				Stream:  api.ReleaseStreamOKD,
				Version: "4.4",
			},
		},
		{
			name: "valid candidate for ocp",
			input: api.Candidate{
				ReleaseDescriptor: api.ReleaseDescriptor{
					Product:      api.ReleaseProductOCP,
					Architecture: api.ReleaseArchitectureS390x,
				},
				Stream:  api.ReleaseStreamNightly,
				Version: "4.5",
			},
		},
		{
			name: "valid candidate with defaulted arch",
			input: api.Candidate{
				ReleaseDescriptor: api.ReleaseDescriptor{
					Product: api.ReleaseProductOKD,
				},
				Stream:  api.ReleaseStreamOKD,
				Version: "4.4",
			},
		},
		{
			name: "valid candidate with defaulted arch and okd stream",
			input: api.Candidate{
				ReleaseDescriptor: api.ReleaseDescriptor{
					Product: api.ReleaseProductOKD,
				},
				Version: "4.4",
			},
		},
		{
			name: "invalid candidate from arch",
			input: api.Candidate{
				ReleaseDescriptor: api.ReleaseDescriptor{
					Product:      api.ReleaseProductOKD,
					Architecture: "oops",
				},
				Stream:  api.ReleaseStreamOKD,
				Version: "4.4",
			},
			output: []error{
				errors.New("root.architecture: must be one of amd64, arm64, multi, ppc64le, s390x"),
			},
		},
		{
			name: "invalid candidate from product",
			input: api.Candidate{
				ReleaseDescriptor: api.ReleaseDescriptor{
					Product:      "whoa",
					Architecture: api.ReleaseArchitectureAMD64,
				},
				Stream:  api.ReleaseStreamOKD,
				Version: "4.4",
			},
			output: []error{
				errors.New("root.product: must be one of ocp, okd"),
			},
		},
		{
			name: "invalid candidate from stream",
			input: api.Candidate{
				ReleaseDescriptor: api.ReleaseDescriptor{
					Product:      api.ReleaseProductOKD,
					Architecture: api.ReleaseArchitectureAMD64,
				},
				Stream:  api.ReleaseStreamCI,
				Version: "4.4",
			},
			output: []error{
				errors.New("root.stream: must be one of , okd, okd-scos"),
			},
		},
		{
			name: "invalid candidate from version",
			input: api.Candidate{
				ReleaseDescriptor: api.ReleaseDescriptor{
					Product:      api.ReleaseProductOKD,
					Architecture: api.ReleaseArchitectureAMD64,
				},
				Stream:  api.ReleaseStreamOKD,
				Version: "4",
			},
			output: []error{
				errors.New(`root.version: must be a minor version in the form [0-9]\.[0-9]+`),
			},
		},
		{
			name: "invalid candidate from ocp stream",
			input: api.Candidate{
				ReleaseDescriptor: api.ReleaseDescriptor{
					Product:      api.ReleaseProductOCP,
					Architecture: api.ReleaseArchitectureAMD64,
				},
				Stream:  api.ReleaseStreamOKD,
				Version: "4.4",
			},
			output: []error{
				errors.New("root.stream: must be one of ci, nightly"),
			},
		},
		{
			name: "invalid relative",
			input: api.Candidate{
				ReleaseDescriptor: api.ReleaseDescriptor{
					Product:      api.ReleaseProductOCP,
					Architecture: api.ReleaseArchitectureAMD64,
					Relative:     -1,
				},
				Stream:  api.ReleaseStreamCI,
				Version: "4.4",
			},
			output: []error{
				errors.New("root.relative: must be a positive integer"),
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if actual, expected := validateCandidate("root", testCase.input), testCase.output; !reflect.DeepEqual(actual, expected) {
				t.Errorf("%s: got incorrect errors: %s", testCase.name, cmp.Diff(actual, expected, cmp.Comparer(func(x, y error) bool {
					return x.Error() == y.Error()
				})))
			}
		})
	}
}

func TestValidateRelease(t *testing.T) {
	var testCases = []struct {
		name   string
		input  api.Release
		output []error
	}{
		{
			name: "valid release",
			input: api.Release{
				Architecture: api.ReleaseArchitectureAMD64,
				Channel:      api.ReleaseChannelCandidate,
				Version:      "4.4",
			},
		},
		{
			name: "valid release with defaulted arch",
			input: api.Release{
				Version: "4.4",
				Channel: api.ReleaseChannelCandidate,
			},
		},
		{
			name: "invalid release from arch",
			input: api.Release{
				Architecture: "oops",
				Channel:      api.ReleaseChannelFast,
				Version:      "4.4",
			},
			output: []error{
				errors.New("root.architecture: must be one of amd64, arm64, multi, ppc64le, s390x"),
			},
		},
		{
			name: "invalid release from channel",
			input: api.Release{
				Architecture: api.ReleaseArchitectureAMD64,
				Channel:      "oops",
				Version:      "4.4",
			},
			output: []error{
				errors.New("root.channel: must be one of candidate, fast, stable"),
			},
		},

		{
			name: "invalid release from version",
			input: api.Release{
				Architecture: api.ReleaseArchitectureAMD64,
				Channel:      api.ReleaseChannelStable,
				Version:      "4",
			},
			output: []error{
				errors.New(`root.version: must be a minor version in the form [0-9]\.[0-9]+`),
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if actual, expected := validateRelease("root", testCase.input), testCase.output; !reflect.DeepEqual(actual, expected) {
				t.Errorf("%s: got incorrect errors: %s", testCase.name, cmp.Diff(actual, expected, cmp.Comparer(func(x, y error) bool {
					return x.Error() == y.Error()
				})))
			}
		})
	}
}

func TestValidatePrerelease(t *testing.T) {
	var testCases = []struct {
		name   string
		input  api.Prerelease
		output []error
	}{
		{
			name: "valid prerelease",
			input: api.Prerelease{
				ReleaseDescriptor: api.ReleaseDescriptor{
					Product:      api.ReleaseProductOKD,
					Architecture: api.ReleaseArchitectureAMD64,
				},
				VersionBounds: api.VersionBounds{
					Lower: "4.1.0",
					Upper: "4.2.0",
				},
			},
		},
		{
			name: "valid prerelease for ocp",
			input: api.Prerelease{
				ReleaseDescriptor: api.ReleaseDescriptor{
					Product:      api.ReleaseProductOCP,
					Architecture: api.ReleaseArchitectureS390x,
				},
				VersionBounds: api.VersionBounds{
					Lower: "4.1.0",
					Upper: "4.2.0",
				},
			},
		},
		{
			name: "valid prerelease with defaulted arch",
			input: api.Prerelease{
				ReleaseDescriptor: api.ReleaseDescriptor{
					Product: api.ReleaseProductOKD,
				},
				VersionBounds: api.VersionBounds{
					Lower: "4.1.0",
					Upper: "4.2.0",
				},
			},
		},
		{
			name: "invalid prerelease from arch",
			input: api.Prerelease{
				ReleaseDescriptor: api.ReleaseDescriptor{
					Product:      api.ReleaseProductOKD,
					Architecture: "oops",
				},
				VersionBounds: api.VersionBounds{
					Lower: "4.1.0",
					Upper: "4.2.0",
				},
			},
			output: []error{
				errors.New("root.architecture: must be one of amd64, arm64, multi, ppc64le, s390x"),
			},
		},
		{
			name: "invalid prerelease from product",
			input: api.Prerelease{
				ReleaseDescriptor: api.ReleaseDescriptor{
					Product:      "whoa",
					Architecture: api.ReleaseArchitectureAMD64,
				},
				VersionBounds: api.VersionBounds{
					Lower: "4.1.0",
					Upper: "4.2.0",
				},
			},
			output: []error{
				errors.New("root.product: must be one of ocp, okd"),
			},
		},
		{
			name: "invalid prerelease from missing version bounds",
			input: api.Prerelease{
				ReleaseDescriptor: api.ReleaseDescriptor{
					Product:      api.ReleaseProductOCP,
					Architecture: api.ReleaseArchitectureAMD64,
				},
				VersionBounds: api.VersionBounds{},
			},
			output: []error{
				errors.New("root.version_bounds.lower: must be set"),
				errors.New("root.version_bounds.upper: must be set"),
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if actual, expected := validatePrerelease("root", testCase.input), testCase.output; !reflect.DeepEqual(actual, expected) {
				t.Errorf("%s: got incorrect errors: %s", testCase.name, cmp.Diff(actual, expected, cmp.Comparer(func(x, y error) bool {
					return x.Error() == y.Error()
				})))
			}
		})
	}
}

func TestValidateIntegration(t *testing.T) {
	var testCases = []struct {
		name      string
		inputName string
		input     api.Integration
		output    []error
	}{
		{
			name:      "valid integration",
			inputName: "latest",
			input: api.Integration{
				Name:               "4.8",
				Namespace:          "ocp",
				IncludeBuiltImages: true,
			},
		},
		{
			name:      "invalid integration missing namespace",
			inputName: "latest",
			input: api.Integration{
				Name:               "4.8",
				IncludeBuiltImages: true,
			},
			output: []error{
				errors.New("root.namespace: must be set"),
			},
		},
		{
			name:      "invalid integration missing name",
			inputName: "latest",
			input: api.Integration{
				Namespace:          "ocp",
				IncludeBuiltImages: true,
			},
			output: []error{
				errors.New("root.name: must be set"),
			},
		},
		{
			name:      "invalid integration non-latest sets image inclusion",
			inputName: "other",
			input: api.Integration{
				Name:               "4.8",
				Namespace:          "ocp",
				IncludeBuiltImages: true,
			},
			output: []error{
				errors.New("root: only the `latest` release can set `include_built_images`"),
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if actual, expected := validateIntegration("root", testCase.inputName, testCase.input), testCase.output; !reflect.DeepEqual(actual, expected) {
				t.Errorf("%s: got incorrect errors: %s", testCase.name, cmp.Diff(actual, expected, cmp.Comparer(func(x, y error) bool {
					return x.Error() == y.Error()
				})))
			}
		})
	}
}
