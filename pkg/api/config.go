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

// testDependencyNames collects the dependency names referenced anywhere in a test's
// expanded (literal_steps) form. Callers are responsible for resolving the test's
// workflow, chains and references first: a dependency declared inside a referenced
// step is invisible here otherwise, since it does not exist yet in the test's own
// steps.
func testDependencyNames(test *TestStepConfiguration) []string {
	var names []string
	literal := test.MultiStageTestConfigurationLiteral
	if literal == nil {
		return names
	}
	for _, steps := range [][]LiteralTestStep{literal.Pre, literal.Test, literal.Post} {
		for _, step := range steps {
			for _, dep := range step.Dependencies {
				names = append(names, dep.Name)
			}
		}
	}
	return names
}

// imageParentNames returns the names of the pipeline images an image build pulls
// from: its `from` image, if set, and any `inputs` image used to populate its
// build context.
func imageParentNames(image *ProjectDirectoryImageBuildStepConfiguration) []string {
	var names []string
	if image.From != "" {
		names = append(names, string(image.From))
	}
	for name := range image.Inputs {
		names = append(names, name)
	}
	return names
}

// renameSourceParent determines the name a project image build's parent (its `from`
// field or an `inputs` key) should carry in the copied, namespaced image, bringing in
// whatever additional source configuration that parent needs to build under that name:
//
//   - A parent that is itself one of the namespaced images is renamed to match it.
//   - A parent that is an imported base image alias is left unchanged: WithPresubmitFrom
//     already carries every base image alias over into result verbatim, so the alias,
//     not a namespaced copy, is what is available to build from.
//   - A parent produced from source's binary (or test binary) build commands is
//     namespaced like the other images, and those commands are carried over under the
//     same ref so that the namespaced "bin"/"test-bin" image actually gets built.
//   - Any other parent (e.g. "src", which is namespaced automatically once its
//     repository is checked out as an extra ref) is namespaced like the other images.
func renameSourceParent(result, source *ReleaseBuildConfiguration, renamed map[PipelineImageStreamTagReference]PipelineImageStreamTagReference, ref, parent string) string {
	if newName, ok := renamed[PipelineImageStreamTagReference(parent)]; ok {
		return string(newName)
	}
	if _, ok := source.BaseImages[parent]; ok {
		return parent
	}

	hasRefCommands := func(list []RefCommands) bool {
		for _, entry := range list {
			if entry.Ref == ref {
				return true
			}
		}
		return false
	}
	switch PipelineImageStreamTagReference(parent) {
	case PipelineImageStreamTagReferenceBinaries:
		if source.BinaryBuildCommands != "" && !hasRefCommands(result.BinaryBuildCommandsList) {
			result.BinaryBuildCommandsList = append(result.BinaryBuildCommandsList, RefCommands{Ref: ref, Commands: source.BinaryBuildCommands})
		}
	case PipelineImageStreamTagReferenceTestBinaries:
		if source.TestBinaryBuildCommands != "" && !hasRefCommands(result.TestBinaryBuildCommandsList) {
			result.TestBinaryBuildCommandsList = append(result.TestBinaryBuildCommandsList, RefCommands{Ref: ref, Commands: source.TestBinaryBuildCommands})
		}
	}
	return fmt.Sprintf("%s-%s", parent, ref)
}

// includeInjectedTestSourceImages copies any project-built pipeline images that the
// injected test depends on (transitively, following `from` and `inputs`) from source
// into result, so that "no base image import, project image build, or bundle image
// build is configured to provide this dependency" is not raised for them at validation
// time.
//
// The images are namespaced with the source repository's "org.repo" ref, matching the
// convention used elsewhere (see registry/server.ResolveAndMergeConfigsAndInjectTest)
// for images that must be built from a repository other than the base config's own
// checkout. The injected test's dependencies are rewritten to point at the namespaced
// images. Callers are responsible for ensuring that repository is actually checked out
// (as an extra ref) wherever this configuration is ultimately run, or the added images
// will fail to build.
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
			if source.Images.Items[i].To == name {
				queue = append(queue, imageParentNames(&source.Images.Items[i])...)
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
		result.BuildRootImages[ref] = *source.BuildRootImage.DeepCopy()
	}

	for i := range source.Images.Items {
		newTo, ok := renamed[source.Images.Items[i].To]
		if !ok {
			continue
		}
		image := source.Images.Items[i]
		image.To = newTo
		if image.From != "" {
			image.From = PipelineImageStreamTagReference(renameSourceParent(result, source, renamed, ref, string(image.From)))
		}
		if len(image.Inputs) > 0 {
			newInputs := make(map[string]ImageBuildInputs, len(image.Inputs))
			for name, input := range image.Inputs {
				newInputs[renameSourceParent(result, source, renamed, ref, name)] = input
			}
			image.Inputs = newInputs
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
	literal := test.MultiStageTestConfigurationLiteral
	if literal == nil {
		return
	}
	for _, steps := range [][]LiteralTestStep{literal.Pre, literal.Test, literal.Post} {
		for i := range steps {
			for j := range steps[i].Dependencies {
				steps[i].Dependencies[j].Name = renameDependency(steps[i].Dependencies[j].Name)
			}
		}
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
//
// resolve is used to expand the selected test's workflow, chains and references before
// its dependencies are examined: a dependency declared inside a referenced step is not
// visible otherwise, since it does not exist yet in the test's own (unresolved) steps.
func (config *ReleaseBuildConfiguration) WithPresubmitFrom(source *ReleaseBuildConfiguration, test string, resolve func(ReleaseBuildConfiguration) (ReleaseBuildConfiguration, error)) (*ReleaseBuildConfiguration, error) {
	var result ReleaseBuildConfiguration
	config.DeepCopyInto(&result)

	for name, isTagRef := range source.BaseImages {
		// TODO: handle conflicts better
		if destIsTagRef, ok := result.BaseImages[name]; ok && isTagRef != destIsTagRef {
			return nil, fmt.Errorf("conflicting base_images: %s", name)
		}
		if result.BaseImages == nil {
			result.BaseImages = map[string]ImageStreamTagReference{}
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

	var found bool
	for i := range source.Tests {
		if source.Tests[i].As == test {
			found = true
			break
		}
	}
	if !found {
		return nil, fmt.Errorf("test '%s' not found in source configuration", test)
	}

	resolvedSource, err := resolve(*source)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve source configuration to determine test %q's dependencies: %w", test, err)
	}
	for i := range resolvedSource.Tests {
		if resolvedSource.Tests[i].As == test {
			// Deep-copy: MultiStageTestConfigurationLiteral holds slices that
			// includeInjectedTestSourceImages rewrites in place below. A shallow copy would
			// alias that state with source, which may be a shared/cached config (see
			// configAgent.GetMatchingConfig) read concurrently by other requests.
			test := *resolvedSource.Tests[i].DeepCopy()
			test.Interval = nil
			test.Cron = nil
			test.MinimumInterval = nil
			test.Postsubmit = false
			includeInjectedTestSourceImages(&result, source, &test)
			result.Tests = []TestStepConfiguration{test}

			return &result, nil
		}
	}
	return nil, fmt.Errorf("test '%s' not found in resolved source configuration", test)
}
