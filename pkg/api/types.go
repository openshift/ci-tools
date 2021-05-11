package api

import (
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/util/sets"
	prowv1 "k8s.io/test-infra/prow/apis/prowjobs/v1"
	"k8s.io/test-infra/prow/repoowners"
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

	// Operator describes the operator bundle(s) that is built by the project
	Operator *OperatorStepConfiguration `json:"operator,omitempty"`

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
func (config ReleaseBuildConfiguration) BuildsImage(name string) bool {
	for _, i := range config.Images {
		if string(i.To) == name {
			return true
		}
	}
	return false
}

// IsBaseImage checks if `name` will be a tag in the pipeline image stream
// by virtue of being imported as a base image
func (config ReleaseBuildConfiguration) IsBaseImage(name string) bool {
	for i := range config.BaseImages {
		if i == name {
			return true
		}
	}
	for i := range config.BaseRPMImages {
		if i == name {
			return true
		}
	}
	return false
}

// IsPipelineImage checks if `name` will be a tag in the pipeline image stream.
func (config ReleaseBuildConfiguration) IsPipelineImage(name string) bool {
	if config.IsBaseImage(name) {
		return true
	}
	switch name {
	case string(PipelineImageStreamTagReferenceRoot),
		string(PipelineImageStreamTagReferenceSource),
		string(PipelineImageStreamTagReferenceBinaries),
		string(PipelineImageStreamTagReferenceTestBinaries),
		string(PipelineImageStreamTagReferenceRPMs),
		string(PipelineImageStreamTagReferenceBundleSource):
		return true
	}
	if IsIndexImage(name) {
		return true
	}
	return config.IsBundleImage(name)
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
	// Requests are resource requests applied to an individual step in the job.
	// These are directly used in creating the Pods that execute the Job.
	Requests ResourceList `json:"requests,omitempty"`
	// Limits are resource limits applied to an individual step in the job.
	// These are directly used in creating the Pods that execute the Job.
	Limits ResourceList `json:"limits,omitempty"`
}

// ResourceList is a map of string resource names and resource
// quantities, as defined on Kubernetes objects. Common resources
// to request or limit are `cpu` and `memory`. For `cpu`, values
// are provided in vCPUs - for instance, `2` or `200m`. For
// `memory`, values are provided in bytes - for instance, `20Mi`
// or `3Gi`.
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

	// CIOperatorInrepoConfigFileName is the name of the file that contains the build root images
	// pullspec.
	CIOperatorInrepoConfigFileName = ".ci-operator.yaml"
)

type CIOperatorInrepoConfig struct {
	BuildRootImage ImageStreamTagReference `json:"build_root_image"`
}

// BuildRootImageConfiguration holds the two ways of using a base image
// that the pipeline will caches on.
type BuildRootImageConfiguration struct {
	ImageStreamTagReference *ImageStreamTagReference          `json:"image_stream_tag,omitempty"`
	ProjectImageBuild       *ProjectDirectoryImageBuildInputs `json:"project_image,omitempty"`
	// If the BuildRoot images pullspec should be read from a file in the repository (BuildRootImageFileName).
	FromRepository bool `json:"from_repository,omitempty"`

	// UseBuildCache enables the import and use of the prior `bin` image
	// as a build cache, if the underlying build root has not changed since
	// the previous cache was published.
	UseBuildCache bool `json:"use_build_cache,omitempty"`
}

// ImageStreamTagReference identifies an ImageStreamTag
type ImageStreamTagReference struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Tag       string `json:"tag"`

	// As is an optional string to use as the intermediate name for this reference.
	As string `json:"as,omitempty"`
}

func (i *ImageStreamTagReference) ISTagName() string {
	return fmt.Sprintf("%s/%s:%s", i.Namespace, i.Name, i.Tag)
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
}

// ReleaseConfiguration records a resolved release with its name.
// We always expect this step to be preempted with an env var
// that was set at startup. This will be cleaner when we refactor
// release dependencies.
type ReleaseConfiguration struct {
	Name              string `json:"name"`
	UnresolvedRelease `json:",inline"`
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

	// RegistryOverride is an override for the registry domain to
	// which we will mirror images. This is an advanced option and
	// should *not* be used in common test workflows. The CI chat
	// bot uses this option to facilitate image sharing.
	RegistryOverride string `json:"registry_override,omitempty"`

	// DisableBuildCache stops us from uploading the build cache.
	// This is useful (only) for CI chat bot invocations where
	// promotion does not imply output artifacts are being created
	// for posterity.
	DisableBuildCache bool `json:"disable_build_cache,omitempty"`
}

// StepConfiguration holds one step configuration.
// Only one of the fields in this can be non-null.
type StepConfiguration struct {
	InputImageTagStepConfiguration              *InputImageTagStepConfiguration              `json:"input_image_tag_step,omitempty"`
	PipelineImageCacheStepConfiguration         *PipelineImageCacheStepConfiguration         `json:"pipeline_image_cache_step,omitempty"`
	SourceStepConfiguration                     *SourceStepConfiguration                     `json:"source_step,omitempty"`
	BundleSourceStepConfiguration               *BundleSourceStepConfiguration               `json:"bundle_source_step,omitempty"`
	IndexGeneratorStepConfiguration             *IndexGeneratorStepConfiguration             `json:"index_generator_step,omitempty"`
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
	InputImage `json:",inline"`
	Sources    []ImageStreamSource `json:"-"`
}

func (config InputImageTagStepConfiguration) Matches(other InputImage) bool {
	return config.InputImage == other
}

func (config InputImageTagStepConfiguration) FormattedSources() string {
	var formattedSources []string
	tests := sets.String{}
	for _, source := range config.Sources {
		switch source.SourceType {
		case ImageStreamSourceTest:
			tests.Insert(source.Name)
		default:
			formattedSources = append(formattedSources, string(source.SourceType))
		}
	}

	if len(tests) > 0 {
		formattedSources = append(formattedSources, fmt.Sprintf("test steps: %s", strings.Join(tests.List(), ",")))

	}

	return strings.Join(formattedSources, ",")

}

func (config *InputImageTagStepConfiguration) AddSources(sources ...ImageStreamSource) {
	config.Sources = append(config.Sources, sources...)
}

type InputImage struct {
	BaseImage ImageStreamTagReference         `json:"base_image"`
	To        PipelineImageStreamTagReference `json:"to,omitempty"`
}

type ImageStreamSourceType string

const (
	ImageStreamSourceRoot    ImageStreamSourceType = "root"
	ImageStreamSourceBase    ImageStreamSourceType = "base_image"
	ImageStreamSourceBaseRpm ImageStreamSourceType = "base_rpm_image"
	ImageStreamSourceTest    ImageStreamSourceType = "test step"
)

type ImageStreamSource struct {
	SourceType ImageStreamSourceType
	Name       string
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

// Cluster is the name of a cluster in CI build farm.
type Cluster string

const (
	ClusterAPPCI   Cluster = "app.ci"
	ClusterBuild01 Cluster = "build01"
	ClusterBuild02 Cluster = "build02"
	ClusterVSphere Cluster = "vsphere"
)

// TestStepConfiguration describes a step that runs a
// command in one of the previously built images and then
// gathers artifacts from that step.
type TestStepConfiguration struct {
	// As is the name of the test.
	As string `json:"as"`
	// Commands are the shell commands to run in
	// the repository root to execute tests.
	Commands string `json:"commands,omitempty"`

	// Cluster specifies the name of the cluster where the test runs.
	Cluster Cluster `json:"cluster,omitempty"`

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

	// Interval is how frequently the test should be run based
	// on the last time the test ran. Setting this field will
	// create a periodic job instead of a presubmit
	Interval *string `json:"interval,omitempty"`

	// ReleaseController configures prowgen to create a periodic that
	// does not get run by prow and instead is run by release-controller.
	// The job must be configured as a verification or periodic job in a
	// release-controller config file when this field is set to `true`.
	ReleaseController bool `json:"release_controller,omitempty"`

	// Postsubmit configures prowgen to generate the job as a postsubmit rather than a presubmit
	Postsubmit bool `json:"postsubmit,omitempty"`

	// ClusterClaim claims an OpenShift cluster and exposes environment variable ${KUBECONFIG} to the test container
	ClusterClaim *ClusterClaim `json:"cluster_claim,omitempty"`

	// Only one of the following can be not-null.
	ContainerTestConfiguration                                *ContainerTestConfiguration                                `json:"container,omitempty"`
	MultiStageTestConfiguration                               *MultiStageTestConfiguration                               `json:"steps,omitempty"`
	MultiStageTestConfigurationLiteral                        *MultiStageTestConfigurationLiteral                        `json:"literal_steps,omitempty"`
	OpenshiftAnsibleClusterTestConfiguration                  *OpenshiftAnsibleClusterTestConfiguration                  `json:"openshift_ansible,omitempty"`
	OpenshiftAnsibleSrcClusterTestConfiguration               *OpenshiftAnsibleSrcClusterTestConfiguration               `json:"openshift_ansible_src,omitempty"`
	OpenshiftAnsibleCustomClusterTestConfiguration            *OpenshiftAnsibleCustomClusterTestConfiguration            `json:"openshift_ansible_custom,omitempty"`
	OpenshiftInstallerClusterTestConfiguration                *OpenshiftInstallerClusterTestConfiguration                `json:"openshift_installer,omitempty"`
	OpenshiftInstallerUPIClusterTestConfiguration             *OpenshiftInstallerUPIClusterTestConfiguration             `json:"openshift_installer_upi,omitempty"`
	OpenshiftInstallerUPISrcClusterTestConfiguration          *OpenshiftInstallerUPISrcClusterTestConfiguration          `json:"openshift_installer_upi_src,omitempty"`
	OpenshiftInstallerCustomTestImageClusterTestConfiguration *OpenshiftInstallerCustomTestImageClusterTestConfiguration `json:"openshift_installer_custom_test_image,omitempty"`
}

// Cloud is the name of a cloud provider, e.g., aws cluster topology, etc.
type Cloud string

const (
	CloudAWS Cloud = "aws"
)

// ClusterClaim claims an OpenShift cluster for the job.
type ClusterClaim struct {
	// Product is the name of the product being released.
	// Defaults to ocp.
	Product ReleaseProduct `json:"product,omitempty"`
	// Version is the version of the product
	Version string `json:"version"`
	// Architecture is the architecture for the product.
	// Defaults to amd64.
	Architecture ReleaseArchitecture `json:"architecture,omitempty"`
	// Cloud is the cloud where the product is installed, e.g., aws.
	Cloud Cloud `json:"cloud"`
	// Owner is the owner of cloud account used to install the product, e.g., dpp.
	Owner string `json:"owner"`
	// Timeout is how long ci-operator will wait for the cluster to be ready.
	// Defaults to 1h.
	Timeout *prowv1.Duration `json:"timeout,omitempty"`
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
	// Leases lists resources that should be acquired for the test.
	Leases []StepLease `json:"leases,omitempty"`
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

// RegistryObserverConfig is the struct that observer configs are unmarshalled into
type RegistryObserverConfig struct {
	// Observer is the top level field of an observer config
	Observer RegistryObserver `json:"observer,omitempty"`
}

// RegistryObserver contains the configuration and documentation for an observer
type RegistryObserver struct {
	// Observer defines the observer pod
	Observer `json:",inline"`
	// Documentation describes what the observer being configured does.
	Documentation string `json:"documentation,omitempty"`
}

// RegistryMetadata maps the registry info for each step in the registry by filename
type RegistryMetadata map[string]RegistryInfo

// RegistryInfo contains metadata about a registry component that is useful for the web UI of the step registry
type RegistryInfo struct {
	// Path is the path of the directoryfor the registry component relative to the registry's base directory
	Path string `json:"path,omitempty"`
	// Owners is the OWNERS config for the registry component
	Owners repoowners.Config `json:"owners,omitempty"`
}

// Observer is the configuration for an observer Pod that will run in parallel
// with a multi-stage test job.
type Observer struct {
	// Name is the name of this observer
	Name string `json:"name"`
	// From is the container image that will be used for this observer.
	From string `json:"from,omitempty"`
	// FromImage is a literal ImageStreamTag reference to use for this observer.
	FromImage *ImageStreamTagReference `json:"from_image,omitempty"`
	// Commands is the command(s) that will be run inside the image.
	Commands string `json:"commands,omitempty"`
}

// Observers is a configuration for which observer pods should and should not
// be run during a job
type Observers struct {
	// Enable is a list of named observer that should be enabled
	Enable []string `json:"enable,omitempty"`
	// Disable is a list of named observers that should be disabled
	Disable []string `json:"disable,omitempty"`
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
	// Resources defines the resource requirements for the step.
	Resources ResourceRequirements `json:"resources"`
	// Timeout is how long the we will wait before aborting a job with SIGINT.
	Timeout *prowv1.Duration `json:"timeout,omitempty"`
	// GracePeriod is how long the we will wait after sending SIGINT to send
	// SIGKILL when aborting a Step.
	GracePeriod *prowv1.Duration `json:"grace_period,omitempty"`
	// Credentials defines the credentials we'll mount into this step.
	Credentials []CredentialReference `json:"credentials,omitempty"`
	// Environment lists parameters that should be set by the test.
	Environment []StepParameter `json:"env,omitempty"`
	// Dependencies lists images which must be available before the test runs
	// and the environment variables which are used to expose their pull specs.
	Dependencies []StepDependency `json:"dependencies,omitempty"`
	// DnsConfig for step's Pod.
	DNSConfig *StepDNSConfig `json:"dnsConfig,omitempty"`
	// Leases lists resources that should be acquired for the test.
	Leases []StepLease `json:"leases,omitempty"`
	// OptionalOnSuccess defines if this step should be skipped as long
	// as all `pre` and `test` steps were successful and AllowSkipOnSuccess
	// flag is set to true in MultiStageTestConfiguration. This option is
	// applicable to `post` steps.
	OptionalOnSuccess *bool `json:"optional_on_success,omitempty"`
	// BestEffort defines if this step should cause the job to fail when the
	// step fails. This only applies when AllowBestEffortPostSteps flag is set
	// to true in MultiStageTestConfiguration. This option is applicable to
	// `post` steps.
	BestEffort *bool `json:"best_effort,omitempty"`
	// Cli is the (optional) name of the release from which the `oc` binary
	// will be injected into this step.
	Cli string `json:"cli,omitempty"`
	// Observers are the observers that should be running
	Observers []string `json:"observers,omitempty"`
	// RunAsScript defines if this step should be executed as a script mounted
	// in the test container instead of being executed directly via bash
	RunAsScript *bool `json:"run_as_script,omitempty"`
}

// StepParameter is a variable set by the test, with an optional default.
type StepParameter struct {
	// Name of the environment variable.
	Name string `json:"name"`
	// Default if not set, optional, makes the parameter not required if set.
	Default *string `json:"default,omitempty"`
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

// StepDependency defines a dependency on an image and the environment variable
// used to expose the image's pull spec to the step.
type StepDependency struct {
	// Name is the tag or stream:tag that this dependency references
	Name string `json:"name"`
	// Env is the environment variable that the image's pull spec is exposed with
	Env string `json:"env"`
}

// StepDNSConfig defines a resource that needs to be acquired prior to execution.
// Used to expose to the step via the specificed search list
type StepDNSConfig struct {
	// Nameservers is a list of IP addresses that will be used as DNS servers for the Pod
	Nameservers []string `json:"nameservers,omitempty"`
	// Searches is a list of DNS search domains for host-name lookup
	Searches []string `json:"searches,omitempty"`
}

// StepLease defines a resource that needs to be acquired prior to execution.
// The resource name will be exposed to the step via the specificed environment
// variable.
type StepLease struct {
	// ResourceType is the type of resource that will be leased.
	ResourceType string `json:"resource_type"`
	// Env is the environment variable that will contain the resource name.
	Env string `json:"env"`
	// Count is the number of resources to acquire (optional, defaults to 1).
	Count uint `json:"count,omitempty"`
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
	// Post steps always run, even if previous steps fail. However, they have an option to skip
	// execution if previous Pre and Test steps passed.
	Post []TestStep `json:"post,omitempty"`
	// Workflow is the name of the workflow to be used for this configuration. For fields defined in both
	// the config and the workflow, the fields from the config will override what is set in Workflow.
	Workflow *string `json:"workflow,omitempty"`
	// Environment has the values of parameters for the steps.
	Environment TestEnvironment `json:"env,omitempty"`
	// Dependencies holds override values for dependency parameters.
	Dependencies TestDependencies `json:"dependencies,omitempty"`
	// DnsConfig for step's Pod.
	DNSConfig *StepDNSConfig `json:"dnsConfig,omitempty"`
	// Leases lists resources that should be acquired for the test.
	Leases []StepLease `json:"leases,omitempty"`
	// AllowSkipOnSuccess defines if any steps can be skipped when
	// all previous `pre` and `test` steps were successful. The given step must explicitly
	// ask for being skipped by setting the OptionalOnSuccess flag to true.
	AllowSkipOnSuccess *bool `json:"allow_skip_on_success,omitempty"`
	// AllowBestEffortPostSteps defines if any `post` steps can be ignored when
	// they fail. The given step must explicitly ask for being ignored by setting
	// the OptionalOnSuccess flag to true.
	AllowBestEffortPostSteps *bool `json:"allow_best_effort_post_steps,omitempty"`
	// Observers are the observers that should be running
	Observers *Observers `json:"observers,omitempty"`
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
	// Dependencies holds override values for dependency parameters.
	Dependencies TestDependencies `json:"dependencies,omitempty"`
	// DnsConfig for step's Pod.
	DNSConfig *StepDNSConfig `json:"dnsConfig,omitempty"`
	// Leases lists resources that should be acquired for the test.
	Leases []StepLease `json:"leases,omitempty"`
	// AllowSkipOnSuccess defines if any steps can be skipped when
	// all previous `pre` and `test` steps were successful. The given step must explicitly
	// ask for being skipped by setting the OptionalOnSuccess flag to true.
	AllowSkipOnSuccess *bool `json:"allow_skip_on_success,omitempty"`
	// AllowBestEffortPostSteps defines if any `post` steps can be ignored when
	// they fail. The given step must explicitly ask for being ignored by setting
	// the OptionalOnSuccess flag to true.
	AllowBestEffortPostSteps *bool `json:"allow_best_effort_post_steps,omitempty"`
	// Observers are the observers that need to be run
	Observers []Observer `json:"observers,omitempty"`
}

// TestEnvironment has the values of parameters for multi-stage tests.
type TestEnvironment map[string]string

// TestDependencies has the values of dependency overrides for multi-stage tests.
type TestDependencies map[string]string

// Secret describes a secret to be mounted inside a test
// container.
type Secret struct {
	// Secret name, used inside test containers
	Name string `json:"name"`
	// Secret mount path. Defaults to /usr/test-secrets for first
	// secret. /usr/test-secrets-2 for second, and so on.
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
	ClusterProfileAzure              ClusterProfile = "azure"
	ClusterProfileAzure4             ClusterProfile = "azure4"
	ClusterProfileAzureArc           ClusterProfile = "azure-arc"
	ClusterProfileGCP                ClusterProfile = "gcp"
	ClusterProfileGCP40              ClusterProfile = "gcp-40"
	ClusterProfileGCPHA              ClusterProfile = "gcp-ha"
	ClusterProfileGCPCRIO            ClusterProfile = "gcp-crio"
	ClusterProfileGCPLogging         ClusterProfile = "gcp-logging"
	ClusterProfileGCPLoggingJournald ClusterProfile = "gcp-logging-journald"
	ClusterProfileGCPLoggingJSONFile ClusterProfile = "gcp-logging-json-file"
	ClusterProfileGCPLoggingCRIO     ClusterProfile = "gcp-logging-crio"
	ClusterProfileGCP2               ClusterProfile = "gcp-openshift-gce-devel-ci-2"
	ClusterProfileLibvirtPpc64le     ClusterProfile = "libvirt-ppc64le"
	ClusterProfileLibvirtS390x       ClusterProfile = "libvirt-s390x"
	ClusterProfileOpenStack          ClusterProfile = "openstack"
	ClusterProfileOpenStackOsuosl    ClusterProfile = "openstack-osuosl"
	ClusterProfileOpenStackVexxhost  ClusterProfile = "openstack-vexxhost"
	ClusterProfileOpenStackPpc64le   ClusterProfile = "openstack-ppc64le"
	ClusterProfileOvirt              ClusterProfile = "ovirt"
	ClusterProfilePacket             ClusterProfile = "packet"
	ClusterProfileVSphere            ClusterProfile = "vsphere"
	ClusterProfileKubevirt           ClusterProfile = "kubevirt"
	ClusterProfileAWSCPaaS           ClusterProfile = "aws-cpaas"
	ClusterProfileOSDEphemeral       ClusterProfile = "osd-ephemeral"
	ClusterProfileAWS2               ClusterProfile = "aws-2"
	ClusterProfileHyperShift         ClusterProfile = "hypershift"
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
		ClusterProfileAzureArc,
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
		ClusterProfileOpenStackOsuosl,
		ClusterProfileOpenStackVexxhost,
		ClusterProfileOpenStackPpc64le,
		ClusterProfileOvirt,
		ClusterProfilePacket,
		ClusterProfileVSphere,
		ClusterProfileKubevirt,
		ClusterProfileAWSCPaaS,
		ClusterProfileOSDEphemeral,
		ClusterProfileAWS2,
		ClusterProfileGCP2,
		ClusterProfileHyperShift,
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
		ClusterProfileAWSGluster,
		ClusterProfileAWSCPaaS,
		ClusterProfileAWS2:
		return "aws"
	case
		ClusterProfileAzure4,
		ClusterProfileAzureArc:
		return "azure4"
	case
		ClusterProfileGCP,
		ClusterProfileGCP40,
		ClusterProfileGCPHA,
		ClusterProfileGCPCRIO,
		ClusterProfileGCPLogging,
		ClusterProfileGCPLoggingJournald,
		ClusterProfileGCPLoggingJSONFile,
		ClusterProfileGCPLoggingCRIO,
		ClusterProfileGCP2:
		return "gcp"
	case ClusterProfileLibvirtPpc64le:
		return "libvirt-ppc64le"
	case ClusterProfileLibvirtS390x:
		return "libvirt-s390x"
	case ClusterProfileOpenStack:
		return "openstack"
	case ClusterProfileOpenStackOsuosl:
		return "openstack-osuosl"
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
	case ClusterProfileKubevirt:
		return "kubevirt"
	case ClusterProfileOSDEphemeral:
		return "osd-ephemeral"
	case ClusterProfileHyperShift:
		return "hypershift"
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
	case ClusterProfileAzureArc:
		return "azure-arc-quota-slice"
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
	case ClusterProfileGCP2:
		return "gcp-openshift-gce-devel-ci-2-quota-slice"
	case ClusterProfileLibvirtPpc64le:
		return "libvirt-ppc64le-quota-slice"
	case ClusterProfileLibvirtS390x:
		return "libvirt-s390x-quota-slice"
	case ClusterProfileOpenStack:
		return "openstack-quota-slice"
	case ClusterProfileOpenStackOsuosl:
		return "openstack-osuosl-quota-slice"
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
	case ClusterProfileKubevirt:
		return "kubevirt-quota-slice"
	case ClusterProfileAWSCPaaS:
		return "aws-cpaas-quota-slice"
	case ClusterProfileOSDEphemeral:
		return "osd-ephemeral-quota-slice"
	case ClusterProfileAWS2:
		return "aws-2-quota-slice"
	case ClusterProfileHyperShift:
		return "hypershift-quota-slice"
	default:
		return ""
	}
}

// LeaseTypeFromClusterType maps cluster types to lease types
func LeaseTypeFromClusterType(t string) (string, error) {
	switch t {
	case "aws", "azure4", "azure-arc", "gcp", "libvirt-ppc64le", "libvirt-s390x", "openstack", "openstack-osuosl", "openstack-vexxhost", "openstack-ppc64le", "vsphere", "ovirt", "packet", "kubevirt", "aws-cpaas", "osd-ephemeral":
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

// OperatorStepConfiguration describes the locations of operator bundle information,
// bundle build dockerfiles, and images the operator(s) depends on that must
// be substituted to run in a CI test cluster
type OperatorStepConfiguration struct {
	// Bundles define a dockerfile and build context to build a bundle
	Bundles []Bundle `json:"bundles,omitempty"`

	// Substitutions describes the pullspecs in the operator manifests that must be subsituted
	// with the pull specs of the images in the CI registry
	Substitutions []PullSpecSubstitution `json:"substitutions,omitempty"`
}

// IndexUpdate specifies the update mode for an operator being added to an index
type IndexUpdate string

const (
	IndexUpdateSemver          = "semver"
	IndexUpdateReplaces        = "replaces"
	IndexUpdateSemverSkippatch = "semver-skippatch"
)

// Bundle contains the data needed to build a bundle from the bundle source image and update an index to include the new bundle
type Bundle struct {
	// As defines the name for this bundle. If not set, a name will be automatically generated for the bundle.
	As string `json:"as,omitempty"`
	// DockerfilePath defines where the dockerfile for build the bundle exists relative to the contextdir
	DockerfilePath string `json:"dockerfile_path,omitempty"`
	// ContextDir defines the source directory to build the bundle from relative to the repository root
	ContextDir string `json:"context_dir,omitempty"`
	// BaseIndex defines what index image to use as a base when adding the bundle to an index
	BaseIndex string `json:"base_index,omitempty"`
	// UpdateGraph defines the update mode to use when adding the bundle to the base index.
	// Can be: semver (default), semver-skippatch, or replaces
	UpdateGraph IndexUpdate `json:"update_graph,omitempty"`
}

// IndexGeneratorStepConfiguration describes a step that creates an index database and
// Dockerfile to build an operator index that uses the generated database based on
// bundle names provided in OperatorIndex
type IndexGeneratorStepConfiguration struct {
	To PipelineImageStreamTagReference `json:"to,omitempty"`

	// OperatorIndex is a list of the names of the bundle images that the
	// index will contain in its database.
	OperatorIndex []string `json:"operator_index,omitempty"`

	// BaseIndex is the index image to add the bundle(s) to. If unset, a new index is created
	BaseIndex string `json:"base_index,omitempty"`

	// UpdateGraph defines the mode to us when updating the index graph
	UpdateGraph IndexUpdate `json:"update_graph,omitempty"`
}

// PipelineImageStreamTagReferenceIndexImageGenerator is the name of the index image generator built by ci-operator
const PipelineImageStreamTagReferenceIndexImageGenerator PipelineImageStreamTagReference = "ci-index-gen"

// PipelineImageStreamTagReferenceIndexImage is the name of the index image built by ci-operator
const PipelineImageStreamTagReferenceIndexImage PipelineImageStreamTagReference = "ci-index"

func IsIndexImage(imageName string) bool {
	return strings.HasPrefix(imageName, string(PipelineImageStreamTagReferenceIndexImage))
}

func IndexName(bundleName string) string {
	return fmt.Sprintf("%s-%s", PipelineImageStreamTagReferenceIndexImage, bundleName)
}

func IndexGeneratorName(indexName PipelineImageStreamTagReference) PipelineImageStreamTagReference {
	return PipelineImageStreamTagReference(fmt.Sprintf("%s-gen", indexName))
}

// BundleSourceStepConfiguration describes a step that performs a set of
// substitutions on all yaml files in the `src` image so that the
// pullspecs in the operator manifests point to images inside the CI registry.
// It is intended to be used as the source image for bundle image builds.
type BundleSourceStepConfiguration struct {
	// Substitutions contains pullspecs that need to be replaced by images
	// in the CI cluster for operator bundle images
	Substitutions []PullSpecSubstitution `json:"substitutions,omitempty"`
}

// PipelineImageStreamTagReferenceBundleSourceName is the name of the bundle source image built by the CI
const PipelineImageStreamTagReferenceBundleSource PipelineImageStreamTagReference = "src-bundle"

// BundlePrefix is the prefix used by ci-operator for bundle images without an explicitly configured name
const BundlePrefix = "ci-bundle"

func (config ReleaseBuildConfiguration) IsBundleImage(imageName string) bool {
	if config.Operator == nil {
		return false
	}
	if strings.HasPrefix(imageName, BundlePrefix) {
		return true
	}
	for _, bundle := range config.Operator.Bundles {
		if bundle.As != "" && imageName == bundle.As {
			return true
		}
	}
	return false
}

func BundleName(index int) string {
	return fmt.Sprintf("%s%d", BundlePrefix, index)
}

// ProjectDirectoryImageBuildStepConfiguration describes an
// image build from a directory in a component project.
type ProjectDirectoryImageBuildStepConfiguration struct {
	From PipelineImageStreamTagReference `json:"from,omitempty"`
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

	// DockerfileLiteral can be used to  provide an inline Dockerfile.
	// Mutually exclusive with DockerfilePath.
	DockerfileLiteral *string `json:"dockerfile_literal,omitempty"`

	// Inputs is a map of tag reference name to image input changes
	// that will populate the build context for the Dockerfile or
	// alter the input image for a multi-stage build.
	Inputs map[string]ImageBuildInputs `json:"inputs,omitempty"`

	// BuildArgs contains build arguments that will be resolved in the Dockerfile.
	// See https://docs.docker.com/engine/reference/builder/#/arg for more details.
	BuildArgs []BuildArg `json:"build_args,omitempty"`
}

type BuildArg struct {
	// Name of the build arg.
	Name string `json:"name,omitempty"`

	// Value of the build arg. Cannot be set if ValueFrom is set.
	Value string `json:"value,omitempty"`

	// ValueFrom specifies the value of the build Arg is taken from a key of a secret. Cannot be set if Value is set.
	ValueFrom *SecretKeySelector `json:"secret_key_ref,omitempty"`
}

// SecretKeySelector selects a key of a Secret.
type SecretKeySelector struct {
	// Namespace is the namespace of the secret
	Namespace string `json:"namespace"`
	// Name is the name of the secret
	Name string `json:"name"`
	// Name is the key of the secret to take the value from
	Key string `json:"key"`
}

// NamespacedName represents the namespaced name
func (arg *SecretKeySelector) NamespacedName() (string, error) {
	if arg.Namespace == "" {
		return "", fmt.Errorf("namespace must be set")
	}
	if arg.Name == "" {
		return "", fmt.Errorf("name must be set")
	}
	if arg.Key == "" {
		return "", fmt.Errorf("key must be set")
	}
	return fmt.Sprintf("%s-%s", arg.Namespace, arg.Name), nil
}

// PullSpecSubstitution contains a name of a pullspec that needs to
// be substituted with the name of a different pullspec. This is used
// for generated operator bundle images.
type PullSpecSubstitution struct {
	// PullSpec is the pullspec that needs to be replaced
	PullSpec string `json:"pullspec,omitempty"`
	// With is the string that the PullSpec is being replaced by
	With string `json:"with,omitempty"`
}

// ImageBuildInputs is a subset of the v1 OpenShift Build API object
// defining an input source.
type ImageBuildInputs struct {
	// Paths is a list of paths to copy out of this image and into the
	// context directory.
	Paths []ImageSourcePath `json:"paths,omitempty"`
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
	// LatestReleaseName is the name of the special latest
	// stable stream, images in this stream are held in
	// the StableImageStream. Images for other versions of
	// the stream are held in similarly-named streams.
	LatestReleaseName = "latest"
	// LatestReleaseName is the name of the special stable
	// stream we copy at import to keep for upgrade tests.
	// TODO(skuznets): remove these when they're not implicit
	InitialReleaseName = "initial"

	// ReleaseImageStream is the name of the ImageStream
	// used to hold built or imported release payload images
	ReleaseImageStream = "release"

	ComponentFormatReplacement = "${component}"
)
