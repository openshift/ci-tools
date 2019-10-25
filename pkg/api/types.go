package api

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
	Requests ResourceList `json:"requests"`
	Limits   ResourceList `json:"limits"`
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
}

// BuildRootImageConfiguration holds the two ways of using a base image
// that the pipeline will caches on.
type BuildRootImageConfiguration struct {
	ImageStreamTagReference *ImageStreamTagReference          `json:"image_stream_tag,omitempty"`
	ProjectImageBuild       *ProjectDirectoryImageBuildInputs `json:"project_image,omitempty"`
}

// ImageStreamTagReference identifies an ImageStreamTag
type ImageStreamTagReference struct {
	// Cluster is an optional cluster string (host, host:port, or
	// scheme://host:port) to connect to for this image stream. The
	// referenced cluster must support anonymous access to retrieve
	// image streams, image stream tags, and image stream images in
	// the provided namespace.
	Cluster   string `json:"cluster,omitempty"`
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
	// Cluster is an optional cluster string (host, host:port, or
	// scheme://host:port) to connect to for this image stream. The
	// referenced cluster must support anonymous access to retrieve
	// image streams, image stream tags, and image stream images in
	// the provided namespace.
	Cluster string `json:"cluster,omitempty"`

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

	// TagOverrides is map of ImageStream name to
	// tag, allowing for specific components in the
	// above namespace to be tagged in at a different
	// level than the rest.
	TagOverrides map[string]string `json:"tag_overrides,omitempty"`
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
	Name string `json:"name"`

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
	Commands string `json:"commands"`
	// ArtifactDir is an optional directory that contains the
	// artifacts to upload. If unset, this will default under
	// the repository root to _output/local/artifacts.
	ArtifactDir string `json:"artifact_dir,omitempty"`

	// Secret is an optional secret object which
	// will be mounted inside the test container.
	Secret *Secret `json:"secret,omitempty"`

	// Cron is how often the test is expected to run outside
	// of pull request workflows. Setting this field will
	// create a periodic job instead of a presubmit
	Cron *string `json:"cron,omitempty"`

	// PullRequestPolicy defines when this test is run as
	// part of pull requests. It is a validation error to
	// set this field when Cron is also set. Policies:
	//
	// * Always: this test is run on every pull request
	// * OnRequest: this test will not be run until a user
	//     explicitly requests it via /test NAME, at which
	//     point it will be required for merge.
	// * RunIfChanged: this test will be run if the regex
	//     in pull_request_run_if_changed matches a change
	//     in the PR.
	//
	// The default value is Always
	PullRequestPolicy string `json:"pull_request_policy"`
	// PullRequestRunIfChanged is an optional regex that
	// will run this test if a filename in the repository
	// matching the regex is changed. If this regex is
	// set the PullRequestPolicy must be RunIfChanged or
	// empty (in which case it is defaulted).
	PullRequestRunIfChanged string `json:"pull_request_run_if_changed"`

	// Only one of the following can be not-null.
	ContainerTestConfiguration                                *ContainerTestConfiguration                                `json:"container,omitempty"`
	MultiStageTestConfiguration                               *MultiStageTestConfiguration                               `json:"steps,omitempty"`
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
	// Commands is the command(s) that will be run inside the image.
	Commands string `json:"commands,omitempty"`
	// ArtifactDir is the directory from which artifacts will be extracted when the command finishes.
	ArtifactDir string `json:"artifact_dir,omitempty"`
	// Resources defines the resource requirements for the step.
	Resources ResourceRequirements `json:"resources,omitempty"`
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
	ClusterProfile ClusterProfile `json:"cluster_profile"`
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
}

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
	ClusterProfileOpenStack          ClusterProfile = "openstack"
	ClusterProfileVSphere            ClusterProfile = "vsphere"
)

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
		return "azure"
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
	case ClusterProfileOpenStack:
		return "openstack"
	case ClusterProfileVSphere:
		return "vsphere"
	default:
		return ""
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

	StableImageStream = "stable"

	ComponentFormatReplacement = "${component}"
)
