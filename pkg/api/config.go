package api

import (
	"fmt"
	"strings"
	"time"

	prowv1 "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
)

// Default sets default values after loading but before validation
func (config *ReleaseBuildConfiguration) Default() {
	defLeases := func(l []StepLease) {
		for i := range l {
			if l[i].Count == 0 {
				l[i].Count = 1
			}
		}
	}
	def := func(s *LiteralTestStep) {
		defLeases(s.Leases)
	}
	defClusterClaim := func(c *ClusterClaim) {
		if c == nil {
			return
		}
		if c.Product == "" {
			c.Product = ReleaseProductOCP
		}
		if c.Architecture == "" {
			c.Architecture = ReleaseArchitectureAMD64
		}
		if c.Timeout == nil {
			c.Timeout = &prowv1.Duration{Duration: time.Hour}
		}
	}
	defTest := func(t *TestStepConfiguration) {
		defClusterClaim(t.ClusterClaim)
		if s := t.MultiStageTestConfigurationLiteral; s != nil {
			defLeases(s.Leases)
			for i := range s.Pre {
				def(&s.Pre[i])
			}
			for i := range s.Test {
				def(&s.Test[i])
			}
			for i := range s.Post {
				def(&s.Post[i])
			}
		}
	}
	for i := range config.RawSteps {
		if test := config.RawSteps[i].TestStepConfiguration; test != nil {
			defTest(test)
		}
	}
	for i := range config.Tests {
		defTest(&config.Tests[i])
	}
}

// ImageStreamFor guesses at the ImageStream that will hold a tag.
// We use this to decipher the user's intent when they provide a
// naked tag in configuration; we support such behavior in order to
// allow users a simpler workflow for the most common cases, like
// referring to `pipeline:src`. If they refer to an ambiguous image,
// however, they will get bad behavior and will need to specify an
// ImageStream as well, for instance release-initial:installer.
// We also return whether the stream is explicit or inferred.
func (config *ReleaseBuildConfiguration) ImageStreamFor(image string) (string, bool) {
	if config.IsPipelineImage(image) || config.BuildsImage(image) {
		return PipelineImageStream, true
	} else {
		return StableImageStream, false
	}
}

// DependencyParts returns the imageStream and tag name from a user-provided
// reference to an image in the test namespace. In situations where a user
// defines a cluster claim and wants to import the cluster claim's release, the
// user may provide a release name that conflicts with a release defined at the
// global config level (e.g. the `latest` release, or `stable` imagestream). To
// prevent conflicts, the name of the imagestream is modified based on the test
// name. ClaimRelease is used in this function to identify whether to override
// the imagestream provided by the user to use the cluster claim's imagestream.
func (config *ReleaseBuildConfiguration) DependencyParts(dependency StepDependency, claimRelease *ClaimRelease) (stream string, name string, explicit bool) {
	if !strings.Contains(dependency.Name, ":") {
		stream, explicit = config.ImageStreamFor(dependency.Name)
		name = dependency.Name
	} else {
		parts := strings.Split(dependency.Name, ":")
		stream = parts[0]
		name = parts[1]
		explicit = true
	}
	if claimRelease != nil {
		if stream == ReleaseImageStream && claimRelease.OverrideName == name {
			// handle release images like `release:latest`
			name = claimRelease.ReleaseName
		} else if stream == ReleaseStreamFor(claimRelease.OverrideName) {
			// handle images from release streams like `stable:cli`
			stream = ReleaseStreamFor(claimRelease.ReleaseName)
		}
	}
	return stream, name, explicit
}

// pipelineImageDependency returns the pipeline image tag a dependency name refers to,
// and whether that tag is a project image built by source (as opposed to a base image,
// a release image, or an image built by whichever config the dependency ends up in).
func pipelineImageDependency(source *ReleaseBuildConfiguration, dependency string) (PipelineImageStreamTagReference, bool) {
	name := dependency
	if idx := strings.Index(dependency, ":"); idx >= 0 {
		if dependency[:idx] != PipelineImageStream {
			return "", false
		}
		name = dependency[idx+1:]
	}
	if !source.BuildsImage(name) {
		return "", false
	}
	return PipelineImageStreamTagReference(name), true
}

// testDependencyNames collects the dependency names referenced anywhere in a test,
// whether it is still workflow-based (steps) or already expanded (literal_steps).
func testDependencyNames(test *TestStepConfiguration) []string {
	var names []string
	literalStepNames := func(steps []LiteralTestStep) {
		for _, step := range steps {
			for _, dep := range step.Dependencies {
				names = append(names, dep.Name)
			}
		}
	}
	if steps := test.MultiStageTestConfiguration; steps != nil {
		for _, name := range steps.Dependencies {
			names = append(names, name)
		}
		for _, step := range append(append(append([]TestStep{}, steps.Pre...), steps.Test...), steps.Post...) {
			if step.LiteralTestStep != nil {
				literalStepNames([]LiteralTestStep{*step.LiteralTestStep})
			}
		}
	}
	if literal := test.MultiStageTestConfigurationLiteral; literal != nil {
		literalStepNames(literal.Pre)
		literalStepNames(literal.Test)
		literalStepNames(literal.Post)
	}
	return names
}

// includeInjectedTestSourceImages copies any project-built pipeline images that the
// injected test depends on (transitively, following `from`) from source into result,
// so that "no base image import, project image build, or bundle image build is
// configured to provide this dependency" is not raised for them at validation time.
//
// The images (and, if needed, the default "src" image they build from) are namespaced
// with the source repository's "org.repo" ref, matching the convention used elsewhere
// (see registry/server.ResolveAndMergeConfigsAndInjectTest) for images that must be
// built from a repository other than the base config's own checkout. The injected
// test's dependencies are rewritten to point at the namespaced images. Callers are
// responsible for ensuring that repository is actually checked out (as an extra ref)
// wherever this configuration is ultimately run, or the added images will fail to build.
func includeInjectedTestSourceImages(result, source *ReleaseBuildConfiguration, test *TestStepConfiguration) {
	needed := map[PipelineImageStreamTagReference]bool{}
	queue := testDependencyNames(test)
	for len(queue) > 0 {
		dependency := queue[0]
		queue = queue[1:]
		name, ok := pipelineImageDependency(source, dependency)
		if !ok || needed[name] {
			continue
		}
		needed[name] = true
		for i := range source.Images.Items {
			if source.Images.Items[i].To == name && source.Images.Items[i].From != "" {
				queue = append(queue, string(source.Images.Items[i].From))
			}
		}
	}
	if len(needed) == 0 {
		return
	}

	ref := fmt.Sprintf("%s.%s", source.Metadata.Org, source.Metadata.Repo)
	renamed := map[PipelineImageStreamTagReference]PipelineImageStreamTagReference{}
	for name := range needed {
		renamed[name] = PipelineImageStreamTagReference(fmt.Sprintf("%s-%s", name, ref))
	}

	if source.BuildRootImage != nil {
		if result.BuildRootImages == nil {
			result.BuildRootImages = map[string]BuildRootImageConfiguration{}
		}
		result.BuildRootImages[ref] = *source.BuildRootImage
	}

	for i := range source.Images.Items {
		newTo, ok := renamed[source.Images.Items[i].To]
		if !ok {
			continue
		}
		image := source.Images.Items[i]
		image.To = newTo
		from := image.From
		if from == "" {
			from = PipelineImageStreamTagReferenceSource
		}
		if newFrom, ok := renamed[from]; ok {
			image.From = newFrom
		} else {
			image.From = PipelineImageStreamTagReference(fmt.Sprintf("%s-%s", from, ref))
		}
		image.Ref = ref
		result.Images.Items = append(result.Images.Items, image)
	}

	renameDependency := func(dependency string) string {
		name, ok := pipelineImageDependency(source, dependency)
		if !ok {
			return dependency
		}
		newName, ok := renamed[name]
		if !ok {
			return dependency
		}
		return fmt.Sprintf("%s:%s", PipelineImageStream, newName)
	}
	renameLiteralSteps := func(steps []LiteralTestStep) {
		for i := range steps {
			for j := range steps[i].Dependencies {
				steps[i].Dependencies[j].Name = renameDependency(steps[i].Dependencies[j].Name)
			}
		}
	}
	if steps := test.MultiStageTestConfiguration; steps != nil {
		for env, dependency := range steps.Dependencies {
			steps.Dependencies[env] = renameDependency(dependency)
		}
		for _, stepList := range [][]TestStep{steps.Pre, steps.Test, steps.Post} {
			for i := range stepList {
				if stepList[i].LiteralTestStep != nil {
					renameLiteralSteps([]LiteralTestStep{*stepList[i].LiteralTestStep})
				}
			}
		}
	}
	if literal := test.MultiStageTestConfigurationLiteral; literal != nil {
		renameLiteralSteps(literal.Pre)
		renameLiteralSteps(literal.Test)
		renameLiteralSteps(literal.Post)
	}
}

// WithPresubmitFrom returns a new configuration, where a selected test from the source
// configuration is injected into the base configuration, together with all elements from
// the source configuration that are potentially necessary to allow that test to function
// in the context of the base configuration. The intended use case is to inject the test
// definition of a "release job" (informing/blocking) into a component ci-operator config
// to allow dynamically executing any such job on any component PR, without the need to
// clutter each individual component ci-operator config.
//
// WARNING: This code is currently experimental and should not be used outside of the
// "release jobs on PRs" effort
// TODO: handle the presubmit/periodic better, extract code etc.
func (config *ReleaseBuildConfiguration) WithPresubmitFrom(source *ReleaseBuildConfiguration, test string) (*ReleaseBuildConfiguration, error) {
	var result ReleaseBuildConfiguration
	config.DeepCopyInto(&result)

	for name, isTagRef := range source.BaseImages {
		// TODO: handle conflicts better
		if destIsTagRef, ok := result.BaseImages[name]; ok && isTagRef != destIsTagRef {
			return nil, fmt.Errorf("conflicting base_images: %s", name)
		}
		result.BaseImages[name] = isTagRef
	}

	var hasLatestRelease bool
	if result.Releases != nil {
		_, hasLatestRelease = result.Releases[LatestReleaseName]
	}

	for name, release := range source.Releases {
		if name == LatestReleaseName && (result.ReleaseTagConfiguration != nil || hasLatestRelease) {
			continue
		}
		if result.Releases == nil {
			result.Releases = map[string]UnresolvedRelease{}
		}
		result.Releases[name] = release
	}

	// TODO: handle resources, likely needs to be union, with max(config, source) on conflicts

	// The source configuration governs how the injected test runs, so carry over the
	// prowgen options that affect the generated pod spec. Configs assembled by the
	// resolver's merge endpoint start from an empty base, and would otherwise lose the
	// stanza entirely and be generated without GSM/CSI support.
	if source.Prowgen != nil && source.Prowgen.EnableSecretsStoreCSIDriver {
		if result.Prowgen == nil {
			result.Prowgen = &ProwgenOverrides{}
		}
		result.Prowgen.EnableSecretsStoreCSIDriver = true
	}

	for i := range source.Tests {
		if source.Tests[i].As == test {
			test := source.Tests[i]
			test.Interval = nil
			test.Cron = nil
			test.MinimumInterval = nil
			test.Postsubmit = false
			includeInjectedTestSourceImages(&result, source, &test)
			result.Tests = []TestStepConfiguration{test}

			return &result, nil
		}
	}
	return nil, fmt.Errorf("test '%s' not found in source configuration", test)
}
