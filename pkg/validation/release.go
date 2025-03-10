package validation

import (
	"fmt"
	"regexp"
	"strings"

	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/openshift/ci-tools/pkg/api"
)

func validateReleases(fieldRoot string, releases map[string]api.UnresolvedRelease, hasTagSpec bool) []error {
	var validationErrors []error
	// we need a deterministic iteration for testing
	names := sets.New[string]()
	for name := range releases {
		names.Insert(name)
	}
	for _, name := range sets.List(names) {
		if err := partOfImageStreamName(name); err != nil {
			validationErrors = append(validationErrors, fmt.Errorf("%s[%s]: the release name is not valid: %w", fieldRoot, name, err))
		}
		release := releases[name]
		if hasTagSpec {
			for _, incompatibleName := range []string{api.LatestReleaseName, api.InitialReleaseName} {
				if name == incompatibleName {
					validationErrors = append(validationErrors, fmt.Errorf("%s.%s: cannot request resolving a(n) %s release and set tag_specification", fieldRoot, name, incompatibleName))
				}
			}
		}
		var set int
		if release.Integration != nil {
			set = set + 1
		}
		if release.Candidate != nil {
			set = set + 1
		}
		if release.Release != nil {
			set = set + 1
		}
		if release.Prerelease != nil {
			set = set + 1
		}

		if set > 1 {
			validationErrors = append(validationErrors, fmt.Errorf("%s.%s: cannot set more than one of integration, candidate, prerelease and release", fieldRoot, name))
		} else if set == 0 {
			validationErrors = append(validationErrors, fmt.Errorf("%s.%s: must set integration, candidate, prerelease or release", fieldRoot, name))
		} else if release.Integration != nil {
			validationErrors = append(validationErrors, validateIntegration(fmt.Sprintf("%s.%s", fieldRoot, name), name, *release.Integration)...)
		} else if release.Candidate != nil {
			validationErrors = append(validationErrors, validateCandidate(fmt.Sprintf("%s.%s", fieldRoot, name), *release.Candidate)...)
		} else if release.Release != nil {
			validationErrors = append(validationErrors, validateRelease(fmt.Sprintf("%s.%s", fieldRoot, name), *release.Release)...)
		} else if release.Prerelease != nil {
			validationErrors = append(validationErrors, validatePrerelease(fmt.Sprintf("%s.%s", fieldRoot, name), *release.Prerelease)...)
		}
	}
	return validationErrors
}

func validateIntegration(fieldRoot, name string, integration api.Integration) []error {
	var validationErrors []error
	if integration.Name == "" {
		validationErrors = append(validationErrors, fmt.Errorf("%s.name: must be set", fieldRoot))
	}
	if integration.Namespace == "" {
		validationErrors = append(validationErrors, fmt.Errorf("%s.namespace: must be set", fieldRoot))
	}
	if integration.IncludeBuiltImages && name != api.LatestReleaseName {
		validationErrors = append(validationErrors, fmt.Errorf("%s: only the `latest` release can set `include_built_images`", fieldRoot))
	}
	return validationErrors
}

var minorVersionMatcher = regexp.MustCompile(`[0-9]\.[0-9]+`)

func validateCandidate(fieldRoot string, candidate api.Candidate) []error {
	var validationErrors []error
	if err := validateProduct(fmt.Sprintf("%s.product", fieldRoot), candidate.Product); err != nil {
		validationErrors = append(validationErrors, err)
		return validationErrors
	}

	// we allow an unset architecture, we will default it later
	if candidate.Architecture != "" {
		if err := validateArchitecture(fmt.Sprintf("%s.architecture", fieldRoot), candidate.Architecture); err != nil {
			validationErrors = append(validationErrors, err)
		}
	}

	streamsByProduct := map[api.ReleaseProduct]sets.Set[string]{
		api.ReleaseProductOKD: sets.New[string]("", string(api.ReleaseStreamOKD),
			string(api.ReleaseStreamOKDScos)), // we allow unset and will default it
		api.ReleaseProductOKDScos: sets.New[string]("", string(api.ReleaseStreamOKD),
			string(api.ReleaseStreamOKDScos)),
		api.ReleaseProductOCP: sets.New[string](string(api.ReleaseStreamCI), string(api.ReleaseStreamNightly), string(api.ReleaseStreamKonfluxNightly)),
	}
	if !streamsByProduct[candidate.Product].Has(string(candidate.Stream)) {
		validationErrors = append(validationErrors, fmt.Errorf("%s.stream: must be one of %s", fieldRoot, strings.Join(sets.List(streamsByProduct[candidate.Product]), ", ")))
	}

	if err := validateVersion(fmt.Sprintf("%s.version", fieldRoot), candidate.Version); err != nil {
		validationErrors = append(validationErrors, err)
	}

	if candidate.Relative < 0 {
		validationErrors = append(validationErrors, fmt.Errorf("%s.relative: must be a positive integer", fieldRoot))
	}

	return validationErrors
}

func validateProduct(fieldRoot string, product api.ReleaseProduct) error {
	products := sets.New[string](string(api.ReleaseProductOKD), string(api.ReleaseProductOCP), string(api.ReleaseProductOKDScos))
	if !products.Has(string(product)) {
		return fmt.Errorf("%s: must be one of %s", fieldRoot, strings.Join(sets.List(products), ", "))
	}
	return nil
}

func validateArchitecture(fieldRoot string, architecture api.ReleaseArchitecture) error {
	architectures := sets.New[string](string(api.ReleaseArchitectureAMD64), string(api.ReleaseArchitecturePPC64le), string(api.ReleaseArchitectureS390x), string(api.ReleaseArchitectureARM64), string(api.ReleaseArchitectureMULTI))
	if !architectures.Has(string(architecture)) {
		return fmt.Errorf("%s: must be one of %s", fieldRoot, strings.Join(sets.List(architectures), ", "))
	}
	return nil
}

func validateVersion(fieldRoot, version string) error {
	if !minorVersionMatcher.MatchString(version) {
		return fmt.Errorf("%s: must be a minor version in the form %s", fieldRoot, minorVersionMatcher.String())
	}
	return nil
}

func validateRelease(fieldRoot string, release api.Release) []error {
	var validationErrors []error
	// we allow an unset architecture, we will default it later
	if release.Architecture != "" {
		if err := validateArchitecture(fmt.Sprintf("%s.architecture", fieldRoot), release.Architecture); err != nil {
			validationErrors = append(validationErrors, err)
		}
	}

	channels := sets.New[string](string(api.ReleaseChannelStable), string(api.ReleaseChannelFast), string(api.ReleaseChannelCandidate))
	if !channels.Has(string(release.Channel)) {
		validationErrors = append(validationErrors, fmt.Errorf("%s.channel: must be one of %s", fieldRoot, strings.Join(sets.List(channels), ", ")))
		return validationErrors
	}

	if err := validateVersion(fmt.Sprintf("%s.version", fieldRoot), release.Version); err != nil {
		validationErrors = append(validationErrors, err)
	}

	return validationErrors
}

func validatePrerelease(fieldRoot string, prerelease api.Prerelease) []error {
	var validationErrors []error
	if err := validateProduct(fmt.Sprintf("%s.product", fieldRoot), prerelease.Product); err != nil {
		validationErrors = append(validationErrors, err)
		return validationErrors
	}

	// we allow an unset architecture, we will default it later
	if prerelease.Architecture != "" {
		if err := validateArchitecture(fmt.Sprintf("%s.architecture", fieldRoot), prerelease.Architecture); err != nil {
			validationErrors = append(validationErrors, err)
		}
	}

	if prerelease.VersionBounds.Lower == "" {
		validationErrors = append(validationErrors, fmt.Errorf("%s.version_bounds.lower: must be set", fieldRoot))
	}
	if prerelease.VersionBounds.Upper == "" {
		validationErrors = append(validationErrors, fmt.Errorf("%s.version_bounds.upper: must be set", fieldRoot))
	}

	return validationErrors
}
