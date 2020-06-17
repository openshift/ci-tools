package api

import (
	"fmt"
)

const (
	// PromotionJobLabelKey marks promotion jobs as such. Only its presence is
	// relevant, its value is not.
	PromotionJobLabelKey = "ci-operator.openshift.io/is-promotion"
)

// IsPromotionJob determines if a given ProwJob is a PromotionJob
func IsPromotionJob(jobLabels map[string]string) bool {
	_, ok := jobLabels[PromotionJobLabelKey]
	return ok
}

// ReleaseBuildConfiguration describes how release
// artifacts are built from a repository of source
// code. The configuration is made up of two parts:
//  - minimal fields that allow the user to buy into
//    our normal conventions without worrying about
//    how the pipeline flows. Use these preferentially
//    for new projects with simple/conventional build
//    configurations.
//  - raw steps that can be used to create custom and
//    fine-grained build flows
type ReleaseBuildConfiguration struct {
	Metadata Metadata `json:"zz_generated_metadata"`

	InputConfiguration `json:",inline"`

	// BinaryBuildCommands will create a "bin" image based on "src" that
	// contains the output of this command. This allows reuse of binary artifacts
	// across other steps. If empty, no "bin" image will be created.
	BinaryBuildCommands string `json:"binary_build_commands,omitempty"`
	// TestBinaryBuildCommands will create a "test-bin" image based on "src" that
	// contains the output of this command. This allows reuse of binary artifacts
	// across other steps. If empty, no "test-bin" image will be created.
	TestBinaryBuildCommands string `json:"test_binary_build_commands,omitempty"`

	// RpmBuildCommands will create an "rpms" image from "bin" (or "src", if no
	// binary build commands were specified) that contains the output of this
	// command. The created RPMs will then be served via HTTP to the "base" image
	// via an injected rpm.repo in the standard location at /etc/yum.repos.d.
	RpmBuildCommands string `json:"rpm_build_commands,omitempty"`
	// RpmBuildLocation is where RPms are deposited after being built. If
	// unset, this will default under the repository root to
	// _output/local/releases/rpms/.
	RpmBuildLocation string `json:"rpm_build_location,omitempty"`

	// CanonicalGoRepository is a directory path that represents
	// the desired location of the contents of this repository in
	// Go. If specified the location of the repository we are
	// cloning from is ignored.
	CanonicalGoRepository *string `json:"canonical_go_repository,omitempty"`

	// Images describes the images that are built
	// baseImage the project as part of the release
	// process. The name of each image is its "to" value
	// and can be used to build only a specific image.
	Images []ProjectDirectoryImageBuildStepConfiguration `json:"images,omitempty"`

	// Tests describes the tests to run inside of built images.
	// The images launched as pods but have no explicit access to
	// the cluster they are running on.
	Tests []TestStepConfiguration `json:"tests,omitempty"`

	// RawSteps are literal Steps that should be
	// included in the final pipeline.
	RawSteps []StepConfiguration `json:"raw_steps,omitempty"`

	// PromotionConfiguration determines how images are promoted
	// by this command. It is ignored unless promotion has specifically
	// been requested. Promotion is performed after all other steps
	// have been completed so that tests can be run prior to promotion.
	// If no promotion is defined, it is defaulted from the ReleaseTagConfiguration.
	PromotionConfiguration *PromotionConfiguration `json:"promotion,omitempty"`

	// Resources is a set of resource requests or limits over the
	// input types. The special name '*' may be used to set default
	// requests and limits.
	Resources ResourceConfiguration `json:"resources,omitempty"`
}

// Metadata describes the source repo for which a config is written
type Metadata struct {
	Org     string `json:"org"`
	Repo    string `json:"repo"`
	Branch  string `json:"branch"`
	Variant string `json:"variant,omitempty"`
}

// BuildsImage checks if an image is built by the release configuration.
func (c ReleaseBuildConfiguration) BuildsImage(name string) bool {
	for _, i := range c.Images {
		if string(i.To) == name {
			return true
		}
	}
	return false
}

// IsPipelineImage checks if `name` will be a tag in the pipeline image stream.
func (c ReleaseBuildConfiguration) IsPipelineImage(name string) bool {
	for i := range c.BaseImages {
		if i == name {
			return true
		}
	}
	for i := range c.BaseRPMImages {
		if i == name {
			return true
		}
	}
	switch name {
	case string(PipelineImageStreamTagReferenceRoot),
		string(PipelineImageStreamTagReferenceSource),
		string(PipelineImageStreamTagReferenceBinaries),
		string(PipelineImageStreamTagReferenceTestBinaries),
		string(PipelineImageStreamTagReferenceRPMs):
		return true
	}
	return false
}

// ResourceConfiguration defines resource overrides for jobs run
// by the operator.
type ResourceConfiguration map[string]ResourceRequirements

func (c ResourceConfiguration) RequirementsForStep(name string) ResourceRequirements {
	req := ResourceRequirements{
		Requests: make(ResourceList),
		Limits:   make(ResourceList),
	}
	if defaults, ok := c["*"]; ok {
		req.Requests.Add(defaults.Requests)
		req.Limits.Add(defaults.Limits)
	}
	if values, ok := c[name]; ok {
		req.Requests.Add(values.Requests)
		req.Limits.Add(values.Limits)
	}
	return req
}

// ResourceRequirements are resource requests and limits applied
// to the individual steps in the job. They are passed directly to
// builds or pods.
type ResourceRequirements struct {
	Requests ResourceList `json:"requests,omitempty"`
	Limits   ResourceList `json:"limits,omitempty"`
}

// ResourceList is a map of string resource names and resource
// quantities, as defined on Kubernetes objects.
type ResourceList map[string]string

func (l ResourceList) Add(values ResourceList) {
	for name, value := range values {
		l[name] = value
	}
}

// InputConfiguration contains the set of image inputs
// to a build and can be used as an override to the
// canonical inputs by a local process.
type InputConfiguration struct {
	// The list of base images describe
	// which images are going to be necessary outside
	// of the pipeline. The key will be the alias that other
	// steps use to refer to this image.
	BaseImages map[string]ImageStreamTagReference `json:"base_images,omitempty"`
	// BaseRPMImages is a list of the images and their aliases that will
	// have RPM repositories injected into them for downstream
	// image builds that require built project RPMs.
	BaseRPMImages map[string]ImageStreamTagReference `json:"base_rpm_images,omitempty"`

	// BuildRootImage supports two ways to get the image that
	// the pipeline will caches on. The one way is to take the reference
	// from an image stream, and the other from a dockerfile.
	BuildRootImage *BuildRootImageConfiguration `json:"build_root,omitempty"`

	// ReleaseTagConfiguration determines how the
	// full release is assembled.
	ReleaseTagConfiguration *ReleaseTagConfiguration `json:"tag_specification,omitempty"`

	// Releases maps semantic release payload identifiers
	// to the names that they will be exposed under. For
	// instance, an 'initial' name will be exposed as
	// $RELEASE_IMAGE_INITIAL. The 'latest' key is special
	// and cannot co-exist with 'tag_specification', as
	// they result in the same output.
	Releases map[string]UnresolvedRelease `json:"releases,omitempty"`
}

// UnresolvedRelease describes a semantic release payload
// identifier we need to resolve to a pull spec.
type UnresolvedRelease struct {
	// Candidate describes a candidate release payload
	Candidate *Candidate `json:"candidate,omitempty"`
	// Prerelease describes a yet-to-be released payload
	Prerelease *Prerelease `json:"prerelease,omitempty"`
	// Release describes a released payload
	Release *Release `json:"release,omitempty"`
}

// Candidate describes a validated candidate release payload
type Candidate struct {
	// Product is the name of the product being released
	Product ReleaseProduct `json:"product"`
	// Architecture is the architecture for the product.
	// Defaults to amd64.
	Architecture ReleaseArchitecture `json:"architecture,omitempty"`
	// ReleaseStream is the stream from which we pick the latest candidate
	Stream ReleaseStream `json:"stream"`
	// Version is the minor version to search for
	Version string `json:"version"`
	// Relative optionally specifies how old of a release
	// is requested from this stream. For instance, a value
	// of 1 will resolve to the previous validated release
	// for this stream.
	Relative int `json:"relative,omitempty"`
}

// Prerelease describes a validated release payload before it is exposed
type Prerelease struct {
	// Product is the name of the product being released
	Product ReleaseProduct `json:"product"`
	// Architecture is the architecture for the product.
	// Defaults to amd64.
	Architecture ReleaseArchitecture `json:"architecture,omitempty"`
	// VersionBounds describe the allowable version bounds to search in
	VersionBounds VersionBounds `json:"version_bounds"`
}

// VersionBounds describe the upper and lower bounds on a version search
type VersionBounds struct {
	Lower string `json:"lower"`
	Upper string `json:"upper"`
}

func (b *VersionBounds) Query() string {
	return fmt.Sprintf(">%s <%s", b.Lower, b.Upper)
}

// ReleaseProduct describes the product being released
type ReleaseProduct string

const (
	ReleaseProductOCP ReleaseProduct = "ocp"
	ReleaseProductOKD ReleaseProduct = "okd"
)

// ReleaseArchitecture describes the architecture for the product
type ReleaseArchitecture string

const (
	ReleaseArchitectureAMD64   ReleaseArchitecture = "amd64"
	ReleaseArchitecturePPC64le ReleaseArchitecture = "ppc64le"
	ReleaseArchitectureS390x   ReleaseArchitecture = "s390x"
)

type ReleaseStream string

const (
	ReleaseStreamCI      ReleaseStream = "ci"
	ReleaseStreamNightly ReleaseStream = "nightly"
	ReleaseStreamOKD     ReleaseStream = "okd"
)

// Release describes a generally available release payload
type Release struct {
	// Version is the minor version to search for
	Version string `json:"version"`
	// Channel is the release channel to search in
	Channel ReleaseChannel `json:"channel"`
	// Architecture is the architecture for the release.
	// Defaults to amd64.
	Architecture ReleaseArchitecture `json:"architecture,omitempty"`
}

type ReleaseChannel string

const (
	ReleaseChannelStable    ReleaseChannel = "stable"
	ReleaseChannelFast      ReleaseChannel = "fast"
	ReleaseChannelCandidate ReleaseChannel = "candidate"
)

// BuildRootImageConfiguration holds the two ways of using a base image
// that the pipeline will caches on.
type BuildRootImageConfiguration struct {
	ImageStreamTagReference *ImageStreamTagReference          `json:"image_stream_tag,omitempty"`
	ProjectImageBuild       *ProjectDirectoryImageBuildInputs `json:"project_image,omitempty"`
}

// ImageStreamTagReference identifies an ImageStreamTag
type ImageStreamTagReference struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Tag       string `json:"tag"`

	// As is an optional string to use as the intermediate name for this reference.
	As string `json:"as,omitempty"`
}

// ReleaseTagConfiguration describes how a release is
// assembled from release artifacts. A release image stream is a
// single stream with multiple tags (openshift/origin-v3.9:control-plane),
// each tag being a unique and well defined name for a component.
type ReleaseTagConfiguration struct {
	// Namespace identifies the namespace from which
	// all release artifacts not built in the current
	// job are tagged from.
	Namespace string `json:"namespace"`

	// Name is the image stream name to use that contains all
	// component tags.
	Name string `json:"name"`

	// NamePrefix is prepended to the final output image name
	// if specified.
	NamePrefix string `json:"name_prefix,omitempty"`
}

// ReleaseConfiguration records a resolved release with its name.
// We always expect this step to be preempted with an env var
// that was set at startup. This will be cleaner when we refactor
// release dependencies.
type ReleaseConfiguration struct {
	Name string `json:"name"`
	UnresolvedRelease
}

// PromotionConfiguration describes where images created by this
// config should be published to. The release tag configuration
// defines the inputs, while this defines the outputs.
type PromotionConfiguration struct {
	// Namespace identifies the namespace to which the built
	// artifacts will be published to.
	Namespace string `json:"namespace"`

	// Name is an optional image stream name to use that
	// contains all component tags. If specified, tag is
	// ignored.
	Name string `json:"name,omitempty"`

	// Tag is the ImageStreamTag tagged in for each
	// build image's ImageStream.
	Tag string `json:"tag,omitempty"`

	// NamePrefix is prepended to the final output image name
	// if specified.
	NamePrefix string `json:"name_prefix,omitempty"`

	// ExcludedImages are image names that will not be promoted.
	// Exclusions are made before additional_images are included.
	// Use exclusions when you want to build images for testing
	// but not promote them afterwards.
	ExcludedImages []string `json:"excluded_images,omitempty"`

	// AdditionalImages is a mapping of images to promote. The
	// images will be taken from the pipeline image stream. The
	// key is the name to promote as and the value is the source
	// name. If you specify a tag that does not exist as the source
	// the destination tag will not be created.
	AdditionalImages map[string]string `json:"additional_images,omitempty"`

	// Disabled will no-op succeed instead of running the actual
	// promotion step. This is useful when two branches need to
	// promote to the same output imagestream on a cut-over but
	// never concurrently, and you want to have promotion config
	// in the ci-operator configuration files all the time.
	Disabled bool `json:"disabled,omitempty"`
}

// StepConfiguration holds one step configuration.
// Only one of the fields in this can be non-null.
type StepConfiguration struct {
	InputImageTagStepConfiguration              *InputImageTagStepConfiguration              `json:"input_image_tag_step,omitempty"`
	PipelineImageCacheStepConfiguration         *PipelineImageCacheStepConfiguration         `json:"pipeline_image_cache_step,omitempty"`
	SourceStepConfiguration                     *SourceStepConfiguration                     `json:"source_step,omitempty"`
	ProjectDirectoryImageBuildStepConfiguration *ProjectDirectoryImageBuildStepConfiguration `json:"project_directory_image_build_step,omitempty"`
	RPMImageInjectionStepConfiguration          *RPMImageInjectionStepConfiguration          `json:"rpm_image_injection_step,omitempty"`
	RPMServeStepConfiguration                   *RPMServeStepConfiguration                   `json:"rpm_serve_step,omitempty"`
	OutputImageTagStepConfiguration             *OutputImageTagStepConfiguration             `json:"output_image_tag_step,omitempty"`
	ReleaseImagesTagStepConfiguration           *ReleaseTagConfiguration                     `json:"release_images_tag_step,omitempty"`
	ResolvedReleaseImagesStepConfiguration      *ReleaseConfiguration                        `json:"resolved_release_images_step,omitempty"`
	TestStepConfiguration                       *TestStepConfiguration                       `json:"test_step,omitempty"`
	ProjectDirectoryImageBuildInputs            *ProjectDirectoryImageBuildInputs            `json:"project_directory_image_build_inputs,omitempty"`
}

// InputImageTagStepConfiguration describes a step that
// tags an externalImage image in to the build pipeline.
// if no explicit output tag is provided, the name
// of the image is used as the tag.
type InputImageTagStepConfiguration struct {
	BaseImage ImageStreamTagReference         `json:"base_image"`
	To        PipelineImageStreamTagReference `json:"to,omitempty"`
}

// OutputImageTagStepConfiguration describes a step that
// tags a pipeline image out from the build pipeline.
type OutputImageTagStepConfiguration struct {
	From PipelineImageStreamTagReference `json:"from"`
	To   ImageStreamTagReference         `json:"to"`

	// Optional means the output step is not built, published, or
	// promoted unless explicitly targeted. Use for builds which
	// are invoked only when testing certain parts of the repo.
	Optional bool `json:"optional"`
}

// PipelineImageCacheStepConfiguration describes a
// step that builds a container image to cache the
// output of commands.
type PipelineImageCacheStepConfiguration struct {
	From PipelineImageStreamTagReference `json:"from"`
	To   PipelineImageStreamTagReference `json:"to"`

	// Commands are the shell commands to run in
	// the repository root to create the cached
	// content.
	Commands string `json:"commands"`
}

// TestStepConfiguration describes a step that runs a
// command in one of the previously built images and then
// gathers artifacts from that step.
type TestStepConfiguration struct {
	// As is the name of the test.
	As string `json:"as"`
	// Commands are the shell commands to run in
	// the repository root to execute tests.
	Commands string `json:"commands,omitempty"`
	// ArtifactDir is an optional directory that contains the
	// artifacts to upload. If unset, this will default to
	// "/tmp/artifacts".
	ArtifactDir string `json:"artifact_dir,omitempty"`

	// Secret is an optional secret object which
	// will be mounted inside the test container.
	// You cannot set the Secret and Secrets attributes
	// at the same time.
	Secret *Secret `json:"secret,omitempty"`

	// Secrets is an optional array of secret objects
	// which will be mounted inside the test container.
	// You cannot set the Secret and Secrets attributes
	// at the same time.
	Secrets []*Secret `json:"secrets,omitempty"`

	// Cron is how often the test is expected to run outside
	// of pull request workflows. Setting this field will
	// create a periodic job instead of a presubmit
	Cron *string `json:"cron,omitempty"`

	// Only one of the following can be not-null.
	ContainerTestConfiguration                                *ContainerTestConfiguration                                `json:"container,omitempty"`
	MultiStageTestConfiguration                               *MultiStageTestConfiguration                               `json:"steps,omitempty"`
	MultiStageTestConfigurationLiteral                        *MultiStageTestConfigurationLiteral                        `json:"literal_steps,omitempty"`
	OpenshiftAnsibleClusterTestConfiguration                  *OpenshiftAnsibleClusterTestConfiguration                  `json:"openshift_ansible,omitempty"`
	OpenshiftAnsibleSrcClusterTestConfiguration               *OpenshiftAnsibleSrcClusterTestConfiguration               `json:"openshift_ansible_src,omitempty"`
	OpenshiftAnsibleCustomClusterTestConfiguration            *OpenshiftAnsibleCustomClusterTestConfiguration            `json:"openshift_ansible_custom,omitempty"`
	OpenshiftAnsible40ClusterTestConfiguration                *OpenshiftAnsible40ClusterTestConfiguration                `json:"openshift_ansible_40,omitempty"`
	OpenshiftAnsibleUpgradeClusterTestConfiguration           *OpenshiftAnsibleUpgradeClusterTestConfiguration           `json:"openshift_ansible_upgrade,omitempty"`
	OpenshiftInstallerClusterTestConfiguration                *OpenshiftInstallerClusterTestConfiguration                `json:"openshift_installer,omitempty"`
	OpenshiftInstallerSrcClusterTestConfiguration             *OpenshiftInstallerSrcClusterTestConfiguration             `json:"openshift_installer_src,omitempty"`
	OpenshiftInstallerUPIClusterTestConfiguration             *OpenshiftInstallerUPIClusterTestConfiguration             `json:"openshift_installer_upi,omitempty"`
	OpenshiftInstallerUPISrcClusterTestConfiguration          *OpenshiftInstallerUPISrcClusterTestConfiguration          `json:"openshift_installer_upi_src,omitempty"`
	OpenshiftInstallerConsoleClusterTestConfiguration         *OpenshiftInstallerConsoleClusterTestConfiguration         `json:"openshift_installer_console,omitempty"`
	OpenshiftInstallerRandomClusterTestConfiguration          *OpenshiftInstallerRandomClusterTestConfiguration          `json:"openshift_installer_random,omitempty"`
	OpenshiftInstallerCustomTestImageClusterTestConfiguration *OpenshiftInstallerCustomTestImageClusterTestConfiguration `json:"openshift_installer_custom_test_image,omitempty"`
}

// RegistryReferenceConfig is the struct that step references are unmarshalled into.
type RegistryReferenceConfig struct {
	// Reference is the top level field of a reference config.
	Reference RegistryReference `json:"ref,omitempty"`
}

// RegistryReference contains the LiteralTestStep of a reference as well as the documentation for the step.
type RegistryReference struct {
	// LiteralTestStep defines the full test step that can be run by the ci-operator.
	LiteralTestStep `json:",inline"`
	// Documentation describes what the step being referenced does.
	Documentation string `json:"documentation,omitempty"`
}

// RegistryChainConfig is the struct that chain references are unmarshalled into.
type RegistryChainConfig struct {
	// Chain is the top level field of a chain config.
	Chain RegistryChain `json:"chain,omitempty"`
}

// RegistryChain contains the array of steps, name, and documentation for a step chain.
type RegistryChain struct {
	// As defines the name of the chain. This is how the chain will be referenced from a job's config.
	As string `json:"as,omitempty"`
	// Steps contains the list of steps that comprise the chain. Steps will be run in the order they are defined.
	Steps []TestStep `json:"steps"`
	// Documentation describes what the chain does.
	Documentation string `json:"documentation,omitempty"`
	// Environment lists parameters that should be set by the test.
	Environment []StepParameter `json:"env,omitempty"`
}

// RegistryWorkflowConfig is the struct that workflow references are unmarshalled into.
type RegistryWorkflowConfig struct {
	// Workflow is the top level field of a workflow config.
	Workflow RegistryWorkflow `json:"workflow,omitempty"`
}

// RegistryWorkflow contains the MultiStageTestConfiguration, name, and documentation for a workflow.
type RegistryWorkflow struct {
	// As defines the name of the workflow. This is how the workflow will be referenced from a job's config.
	As string `json:"as,omitempty"`
	// Steps contains the MultiStageTestConfiguration that the workflow defines.
	Steps MultiStageTestConfiguration `json:"steps,omitempty"`
	// Documentation describes what the workflow does.
	Documentation string `json:"documentation,omitempty"`
}

// LiteralTestStep is the external representation of a test step allowing users
// to define new test steps. It gets converted to an internal LiteralTestStep
// struct that represents the full configuration that ci-operator can use.
type LiteralTestStep struct {
	// As is the name of the LiteralTestStep.
	As string `json:"as,omitempty"`
	// From is the container image that will be used for this step.
	From string `json:"from,omitempty"`
	// FromImage is a literal ImageStreamTag reference to use for this step.
	FromImage *ImageStreamTagReference `json:"from_image,omitempty"`
	// Commands is the command(s) that will be run inside the image.
	Commands string `json:"commands,omitempty"`
	// ArtifactDir is the directory from which artifacts will be extracted
	// when the command finishes. Defaults to "/tmp/artifacts"
	ArtifactDir string `json:"artifact_dir,omitempty"`
	// Resources defines the resource requirements for the step.
	Resources ResourceRequirements `json:"resources,omitempty"`
	// Credentials defines the credentials we'll mount into this step.
	Credentials []CredentialReference `json:"credentials,omitempty"`
	// Environment lists parameters that should be set by the test.
	Environment []StepParameter `json:"env,omitempty"`
}

// StepParameter is a variable set by the test, with an optional default.
type StepParameter struct {
	// Name of the environment variable.
	Name string `json:"name"`
	// Default if not set, optional, makes the parameter not required if set.
	Default string `json:"default,omitempty"`
	// Documentation is a textual description of the parameter.
	Documentation string `json:"documentation,omitempty"`
}

// CredentialReference defines a secret to mount into a step and where to mount it.
type CredentialReference struct {
	// Namespace is where the source secret exists.
	Namespace string `json:"namespace"`
	// Names is which source secret to mount.
	Name string `json:"name"`
	// MountPath is where the secret should be mounted.
	MountPath string `json:"mount_path"`
}

// FromImageTag returns the internal name for the image tag that will be used
// for this step, if one is configured.
func (s *LiteralTestStep) FromImageTag() (PipelineImageStreamTagReference, bool) {
	if s.FromImage == nil {
		return "", false
	}
	return PipelineImageStreamTagReference(fmt.Sprintf("%s-%s-%s", s.FromImage.Namespace, s.FromImage.Name, s.FromImage.Tag)), true
}

// TestStep is the struct that a user's configuration gets unmarshalled into.
// It can contain either a LiteralTestStep, Reference, or Chain. If more than one is filled in an
// the same time, config validation will fail.
type TestStep struct {
	// LiteralTestStep is a full test step definition.
	*LiteralTestStep `json:",inline,omitempty"`
	// Reference is the name of a step reference.
	Reference *string `json:"ref,omitempty"`
	// Chain is the name of a step chain reference.
	Chain *string `json:"chain,omitempty"`
}

// MultiStageTestConfiguration is a flexible configuration mode that allows tighter control over
// the multiple stages of end to end tests.
type MultiStageTestConfiguration struct {
	// ClusterProfile defines the profile/cloud provider for end-to-end test steps.
	ClusterProfile ClusterProfile `json:"cluster_profile,omitempty"`
	// Pre is the array of test steps run to set up the environment for the test.
	Pre []TestStep `json:"pre,omitempty"`
	// Test is the array of test steps that define the actual test.
	Test []TestStep `json:"test,omitempty"`
	// Post is the array of test steps run after the tests finish and teardown/deprovision resources.
	// Post steps always run, even if previous steps fail.
	Post []TestStep `json:"post,omitempty"`
	// Workflow is the name of the workflow to be used for this configuration. For fields defined in both
	// the config and the workflow, the fields from the config will override what is set in Workflow.
	Workflow *string `json:"workflow,omitempty"`
	// Environment has the values of parameters for the steps.
	Environment TestEnvironment `json:"env,omitempty"`
}

// MultiStageTestConfigurationLiteral is a form of the MultiStageTestConfiguration that does not include
// references. It is the type that MultiStageTestConfigurations are converted to when parsed by the
// ci-operator-configresolver.
type MultiStageTestConfigurationLiteral struct {
	// ClusterProfile defines the profile/cloud provider for end-to-end test steps.
	ClusterProfile ClusterProfile `json:"cluster_profile"`
	// Pre is the array of test steps run to set up the environment for the test.
	Pre []LiteralTestStep `json:"pre,omitempty"`
	// Test is the array of test steps that define the actual test.
	Test []LiteralTestStep `json:"test,omitempty"`
	// Post is the array of test steps run after the tests finish and teardown/deprovision resources.
	// Post steps always run, even if previous steps fail.
	Post []LiteralTestStep `json:"post,omitempty"`
	// Environment has the values of parameters for the steps.
	Environment TestEnvironment `json:"env,omitempty"`
}

// TestEnvironment has the values of parameters for multi-stage tests.
type TestEnvironment map[string]string

// Secret describes a secret to be mounted inside a test
// container.
type Secret struct {
	// Secret name, used inside test containers
	Name string `json:"name"`
	// Secret mount path. Defaults to /usr/test-secret
	MountPath string `json:"mount_path"`
}

// MemoryBackedVolume describes a tmpfs (memory backed volume)
// that will be mounted into a test container at /tmp/volume.
// Use with tests that need extremely fast disk, such as those
// that run an etcd server or other IO-intensive workload.
type MemoryBackedVolume struct {
	// Size is the requested size of the volume as a Kubernetes
	// quantity, i.e. "1Gi" or "500M"
	Size string `json:"size"`
}

// ContainerTestConfiguration describes a test that runs a
// command in one of the previously built images.
type ContainerTestConfiguration struct {
	// From is the image stream tag in the pipeline to run this
	// command in.
	From PipelineImageStreamTagReference `json:"from"`
	// MemoryBackedVolume mounts a volume of the specified size into
	// the container at /tmp/volume.
	MemoryBackedVolume *MemoryBackedVolume `json:"memory_backed_volume,omitempty"`
}

// ClusterProfile is the name of a set of input variables
// provided to the installer defining the target cloud,
// cluster topology, etc.
type ClusterProfile string

const (
	ClusterProfileAWS                ClusterProfile = "aws"
	ClusterProfileAWSAtomic          ClusterProfile = "aws-atomic"
	ClusterProfileAWSCentos          ClusterProfile = "aws-centos"
	ClusterProfileAWSCentos40        ClusterProfile = "aws-centos-40"
	ClusterProfileAWSGluster         ClusterProfile = "aws-gluster"
	ClusterProfileAzure4             ClusterProfile = "azure4"
	ClusterProfileGCP                ClusterProfile = "gcp"
	ClusterProfileGCP40              ClusterProfile = "gcp-40"
	ClusterProfileGCPHA              ClusterProfile = "gcp-ha"
	ClusterProfileGCPCRIO            ClusterProfile = "gcp-crio"
	ClusterProfileGCPLogging         ClusterProfile = "gcp-logging"
	ClusterProfileGCPLoggingJournald ClusterProfile = "gcp-logging-journald"
	ClusterProfileGCPLoggingJSONFile ClusterProfile = "gcp-logging-json-file"
	ClusterProfileGCPLoggingCRIO     ClusterProfile = "gcp-logging-crio"
	ClusterProfileLibvirtPpc64le     ClusterProfile = "libvirt-ppc64le"
	ClusterProfileLibvirtS390x       ClusterProfile = "libvirt-s390x"
	ClusterProfileOpenStack          ClusterProfile = "openstack"
	ClusterProfileOpenStackVexxhost  ClusterProfile = "openstack-vexxhost"
	ClusterProfileOpenStackPpc64le   ClusterProfile = "openstack-ppc64le"
	ClusterProfileOvirt              ClusterProfile = "ovirt"
	ClusterProfilePacket             ClusterProfile = "packet"
	ClusterProfileVSphere            ClusterProfile = "vsphere"
)

// ClusterProfiles are all valid cluster profiles
func ClusterProfiles() []ClusterProfile {
	return []ClusterProfile{
		ClusterProfileAWS,
		ClusterProfileAWSAtomic,
		ClusterProfileAWSCentos,
		ClusterProfileAWSCentos40,
		ClusterProfileAWSGluster,
		ClusterProfileAzure4,
		ClusterProfileGCP,
		ClusterProfileGCP40,
		ClusterProfileGCPHA,
		ClusterProfileGCPCRIO,
		ClusterProfileGCPLogging,
		ClusterProfileGCPLoggingJournald,
		ClusterProfileGCPLoggingJSONFile,
		ClusterProfileGCPLoggingCRIO,
		ClusterProfileLibvirtPpc64le,
		ClusterProfileLibvirtS390x,
		ClusterProfileOpenStack,
		ClusterProfileOpenStackVexxhost,
		ClusterProfileOpenStackPpc64le,
		ClusterProfileOvirt,
		ClusterProfilePacket,
		ClusterProfileVSphere,
	}
}

// ClusterType maps profiles to the type string used by tests.
func (p ClusterProfile) ClusterType() string {
	switch p {
	case
		ClusterProfileAWS,
		ClusterProfileAWSAtomic,
		ClusterProfileAWSCentos,
		ClusterProfileAWSCentos40,
		ClusterProfileAWSGluster:
		return "aws"
	case ClusterProfileAzure4:
		return "azure4"
	case
		ClusterProfileGCP,
		ClusterProfileGCP40,
		ClusterProfileGCPHA,
		ClusterProfileGCPCRIO,
		ClusterProfileGCPLogging,
		ClusterProfileGCPLoggingJournald,
		ClusterProfileGCPLoggingJSONFile,
		ClusterProfileGCPLoggingCRIO:
		return "gcp"
	case ClusterProfileLibvirtPpc64le:
		return "libvirt-ppc64le"
	case ClusterProfileLibvirtS390x:
		return "libvirt-s390x"
	case ClusterProfileOpenStack:
		return "openstack"
	case ClusterProfileOpenStackVexxhost:
		return "openstack-vexxhost"
	case ClusterProfileOpenStackPpc64le:
		return "openstack-ppc64le"
	case ClusterProfileVSphere:
		return "vsphere"
	case ClusterProfileOvirt:
		return "ovirt"
	case ClusterProfilePacket:
		return "packet"
	default:
		return ""
	}
}

// LeaseType maps profiles to the type string used in leases.
func (p ClusterProfile) LeaseType() string {
	switch p {
	case
		ClusterProfileAWS,
		ClusterProfileAWSAtomic,
		ClusterProfileAWSCentos,
		ClusterProfileAWSCentos40,
		ClusterProfileAWSGluster:
		return "aws-quota-slice"
	case ClusterProfileAzure4:
		return "azure4-quota-slice"
	case
		ClusterProfileGCP,
		ClusterProfileGCP40,
		ClusterProfileGCPHA,
		ClusterProfileGCPCRIO,
		ClusterProfileGCPLogging,
		ClusterProfileGCPLoggingJournald,
		ClusterProfileGCPLoggingJSONFile,
		ClusterProfileGCPLoggingCRIO:
		return "gcp-quota-slice"
	case ClusterProfileLibvirtPpc64le:
		return "libvirt-ppc64le-quota-slice"
	case ClusterProfileLibvirtS390x:
		return "libvirt-s390x-quota-slice"
	case ClusterProfileOpenStack:
		return "openstack-quota-slice"
	case ClusterProfileOpenStackVexxhost:
		return "openstack-vexxhost-quota-slice"
	case ClusterProfileOpenStackPpc64le:
		return "openstack-ppc64le-quota-slice"
	case ClusterProfileOvirt:
		return "ovirt-quota-slice"
	case ClusterProfilePacket:
		return "packet-quota-slice"
	case ClusterProfileVSphere:
		return "vsphere-quota-slice"
	default:
		return ""
	}
}

// LeaseTypeFromClusterType maps cluster types to lease types
func LeaseTypeFromClusterType(t string) (string, error) {
	switch t {
	case "aws", "azure4", "gcp", "libvirt-ppc64le", "libvirt-s390x", "openstack", "openstack-vexxhost", "openstack-ppc64le", "vsphere", "ovirt", "packet":
		return t + "-quota-slice", nil
	default:
		return "", fmt.Errorf("invalid cluster type %q", t)
	}
}

// ClusterTestConfiguration describes a test that provisions
// a cluster and runs a command in it.
type ClusterTestConfiguration struct {
	ClusterProfile ClusterProfile `json:"cluster_profile"`
}

// OpenshiftAnsibleClusterTestConfiguration describes a test
// that provisions a cluster using openshift-ansible and runs
// conformance tests.
type OpenshiftAnsibleClusterTestConfiguration struct {
	ClusterTestConfiguration `json:",inline"`
}

// OpenshiftAnsibleSrcClusterTestConfiguration describes a
// test that provisions a cluster using openshift-ansible and
// executes a command in the `src` image.
type OpenshiftAnsibleSrcClusterTestConfiguration struct {
	ClusterTestConfiguration `json:",inline"`
}

// OpenshiftAnsibleCustomClusterTestConfiguration describes a
// test that provisions a cluster using openshift-ansible's
// custom provisioner, and runs conformance tests.
type OpenshiftAnsibleCustomClusterTestConfiguration struct {
	ClusterTestConfiguration `json:",inline"`
}

// OpenshiftAnsible40ClusterTestConfiguration describes a
// test that provisions a cluster using new installer and openshift-ansible
type OpenshiftAnsible40ClusterTestConfiguration struct {
	ClusterTestConfiguration `json:",inline"`
}

// OpenshiftAnsibleUpgradeClusterTestConfiguration describes a
// test that provisions a cluster using openshift-ansible,
// upgrades it to the next version and runs conformance tests.
type OpenshiftAnsibleUpgradeClusterTestConfiguration struct {
	ClusterTestConfiguration `json:",inline"`
	PreviousVersion          string `json:"previous_version"`
	PreviousRPMDeps          string `json:"previous_rpm_deps"`
}

// OpenshiftInstallerClusterTestConfiguration describes a test
// that provisions a cluster using openshift-installer and runs
// conformance tests.
type OpenshiftInstallerClusterTestConfiguration struct {
	ClusterTestConfiguration `json:",inline"`
	// If upgrade is true, RELEASE_IMAGE_INITIAL will be used as
	// the initial payload and the installer image from that
	// will be upgraded. The `run-upgrade-tests` function will be
	// available for the commands.
	Upgrade bool `json:"upgrade,omitempty"`
}

// OpenshiftInstallerSrcClusterTestConfiguration describes a
// test that provisions a cluster using openshift-installer and
// executes a command in the `src` image.
type OpenshiftInstallerSrcClusterTestConfiguration struct {
	ClusterTestConfiguration `json:",inline"`
}

// OpenshiftInstallerConsoleClusterTestConfiguration describes a
// test that provisions a cluster using openshift-installer and
// executes a command in the `console-test` image.
type OpenshiftInstallerConsoleClusterTestConfiguration struct {
	ClusterTestConfiguration `json:",inline"`
}

// OpenshiftInstallerUPIClusterTestConfiguration describes a
// test that provisions machines using installer-upi image and
// installs the cluster using UPI flow.
type OpenshiftInstallerUPIClusterTestConfiguration struct {
	ClusterTestConfiguration `json:",inline"`
}

// OpenshiftInstallerUPISrcClusterTestConfiguration describes a
// test that provisions machines using installer-upi image and
// installs the cluster using UPI flow. Tests will be run
// akin to the OpenshiftInstallerSrcClusterTestConfiguration.
type OpenshiftInstallerUPISrcClusterTestConfiguration struct {
	ClusterTestConfiguration `json:",inline"`
}

// OpenshiftInstallerRandomClusterTestConfiguration describes a
// that provisions a cluster using openshift-installer in a provider
// chosen randomly and runs conformance tests.
type OpenshiftInstallerRandomClusterTestConfiguration struct{}

// OpenshiftInstallerCustomTestImageClusterTestConfiguration describes a
// test that provisions a cluster using openshift-installer and
// executes a command in the image specified by the job configuration.
type OpenshiftInstallerCustomTestImageClusterTestConfiguration struct {
	ClusterTestConfiguration `json:",inline"`
	// From defines the imagestreamtag that will be used to run the
	// provided test command.  e.g. stable:console-test
	From             string `json:"from"`
	EnableNestedVirt bool   `json:"enable_nested_virt,omitempty"`
	NestedVirtImage  string `json:"nested_virt_image,omitempty"`
}

// OpenshiftInstallerGCPNestedVirtCustomTestImageClusterTestConfiguration describes a
// test that provisions a gcp cluster using openshift-installer with nested virt enabled
// and executes a command in the image specified by the job configuration.
type OpenshiftInstallerGCPNestedVirtCustomTestImageClusterTestConfiguration struct {
	ClusterTestConfiguration `json:",inline"`
	// From defines the imagestreamtag that will be used to run the
	// provided test command.  e.g. stable:console-test
	From string `json:"from"`
}

// PipelineImageStreamTagReference is a tag on the
// ImageStream corresponding to the code under test.
// This tag will identify an image but not use any
// namespaces or prefixes, For instance, if for the
// image openshift/origin-pod, the tag would be `pod`.
type PipelineImageStreamTagReference string

const (
	PipelineImageStreamTagReferenceRoot         PipelineImageStreamTagReference = "root"
	PipelineImageStreamTagReferenceSource       PipelineImageStreamTagReference = "src"
	PipelineImageStreamTagReferenceBinaries     PipelineImageStreamTagReference = "bin"
	PipelineImageStreamTagReferenceTestBinaries PipelineImageStreamTagReference = "test-bin"
	PipelineImageStreamTagReferenceRPMs         PipelineImageStreamTagReference = "rpms"
)

// SourceStepConfiguration describes a step that
// clones the source repositories required for
// jobs. If no output tag is provided, the default
// of `src` is used.
type SourceStepConfiguration struct {
	From PipelineImageStreamTagReference `json:"from"`
	To   PipelineImageStreamTagReference `json:"to,omitempty"`

	// ClonerefsImage is the image where we get the clonerefs tool
	ClonerefsImage ImageStreamTagReference `json:"clonerefs_image"`
	// ClonerefsPath is the path in the above image where the
	// clonerefs tool is placed
	ClonerefsPath string `json:"clonerefs_path"`
}

// ProjectDirectoryImageBuildStepConfiguration describes an
// image build from a directory in a component project.
type ProjectDirectoryImageBuildStepConfiguration struct {
	From PipelineImageStreamTagReference `json:"from"`
	To   PipelineImageStreamTagReference `json:"to"`

	ProjectDirectoryImageBuildInputs `json:",inline"`

	// Optional means the build step is not built, published, or
	// promoted unless explicitly targeted. Use for builds which
	// are invoked only when testing certain parts of the repo.
	Optional bool `json:"optional,omitempty"`
}

// ProjectDirectoryImageBuildInputs holds inputs for an image build from the repo under test
type ProjectDirectoryImageBuildInputs struct {
	// ContextDir is the directory in the project
	// from which this build should be run.
	ContextDir string `json:"context_dir,omitempty"`

	// DockerfilePath is the path to a Dockerfile in the
	// project to run relative to the context_dir.
	DockerfilePath string `json:"dockerfile_path,omitempty"`

	// Inputs is a map of tag reference name to image input changes
	// that will populate the build context for the Dockerfile or
	// alter the input image for a multi-stage build.
	Inputs map[string]ImageBuildInputs `json:"inputs,omitempty"`
}

// ImageBuildInputs is a subset of the v1 OpenShift Build API object
// defining an input source.
type ImageBuildInputs struct {
	// Paths is a list of paths to copy out of this image and into the
	// context directory.
	Paths []ImageSourcePath `json:"paths"`
	// As is a list of multi-stage step names or image names that will
	// be replaced by the image reference from this step. For instance,
	// if the Dockerfile defines FROM nginx:latest AS base, specifying
	// either "nginx:latest" or "base" in this array will replace that
	// image with the pipeline input.
	As []string `json:"as,omitempty"`
}

// ImageSourcePath maps a path in the source image into a destination
// path in the context. See the v1 OpenShift Build API for more info.
type ImageSourcePath struct {
	// SourcePath is a file or directory in the source image to copy from.
	SourcePath string `json:"source_path"`
	// DestinationDir is the directory in the destination image to copy
	// to.
	DestinationDir string `json:"destination_dir"`
}

// RPMImageInjectionStepConfiguration describes a step
// that updates injects an RPM repo into an image. If no
// output tag is provided, the input tag is updated.
type RPMImageInjectionStepConfiguration struct {
	From PipelineImageStreamTagReference `json:"from"`
	To   PipelineImageStreamTagReference `json:"to,omitempty"`
}

// RPMServeStepConfiguration describes a step that launches
// a server from an image with RPMs and exposes it to the web.
type RPMServeStepConfiguration struct {
	From PipelineImageStreamTagReference `json:"from"`
}

const (
	// api.PipelineImageStream is the name of the
	// ImageStream used to hold images built
	// to cache build steps in the pipeline.
	PipelineImageStream = "pipeline"

	// DefaultRPMLocation is the default relative
	// directory for Origin-based projects to put
	// their built RPMs.
	DefaultRPMLocation = "_output/local/releases/rpms/"

	// RPMServeLocation is the location from which
	// we will serve RPMs after they are built.
	RPMServeLocation = "/srv/repo"

	// StableImageStream is the ImageStream used to hold
	// build outputs from the repository under test and
	// the associated images imported from integration streams
	StableImageStream = "stable"
	// LatestStableName is the name of the special latest
	// stable stream, images in this stream are held in
	// the StableImageStream. Images for other versions of
	// the stream are held in similarly-named streams.
	LatestStableName = "latest"
	// InitialStableName is the name of the special stable
	// stream we copy at import to keep for upgrade tests.
	// TODO(skuznets): remove these when they're not implicit
	InitialStableName = "initial"

	// ReleaseImageStream is the name of the ImageStream
	// used to hold built or imported release payload images
	ReleaseImageStream = "release"

	ComponentFormatReplacement = "${component}"
)
