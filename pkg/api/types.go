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
	// TestBaseImage is the image we base our pipeline
	// image caches on. It should contain all build-time
	// dependencies for the project. It is valid to set
	// only the tag and allow for the other fields to be
	// defaulted.
	TestBaseImage *ImageStreamTagReference `json:"test_base_image,omitempty"`

	// The following commands describe how binaries,
	// test binaries and RPMs are built baseImage
	// source in the repo under test. If a command is
	// omitted by the user, the resulting image is
	// not built.
	BinaryBuildCommands     string `json:"binary_build_commands,omitempty"`
	TestBinaryBuildCommands string `json:"test_binary_build_commands,omitempty"`
	RpmBuildCommands        string `json:"rpm_build_commands,omitempty"`

	// RpmBuildLocation is where RPms are deposited
	// after being built. If unset, this will default
	// under the repository root to
	// _output/local/releases/rpms/.
	RpmBuildLocation string `json:"rpm_build_location,omitempty"`

	// The following lists of base images describe
	// which images are going to be necessary outside
	// of the pipeline. RPM repositories will be
	// injected into the baseRPMImages for downstream
	// image builds that require built project RPMs.
	BaseImages    []ImageStreamTagReference `json:"base_images,omitempty"`
	BaseRPMImages []ImageStreamTagReference `json:"base_rpm_images,omitempty"`

	// Images describes the images that are built
	// baseImage the project as part of the release
	// process
	Images []ProjectDirectoryImageBuildStepConfiguration `json:"images,omitempty"`

	// ReleaseTagConfiguration determines how the
	// full release is assembled.
	ReleaseTagConfiguration *ReleaseTagConfiguration `json:"tag_specification,omitempty"`

	// RawSteps are literal Steps that should be
	// included in the final pipeline.
	RawSteps []StepConfiguration `json:"raw_steps,omitempty"`
}

// ImageStreamTagReference identifies an ImageStreamTag
type ImageStreamTagReference struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Tag       string `json:"tag"`
}

// ReleaseTagConfiguration describes how a release is
// assembled from release arifacts.
type ReleaseTagConfiguration struct {
	// Namespace identifies the namespace from which
	// all release artifacts not built in the current
	// job are tagged from.
	Namespace string `json:"namespace"`

	// Tag is the ImageStreamTag tagged in for each
	// ImageStream in the above Namespace.
	Tag string `json:"tag"`

	// TagOverrides is map of ImageStream name to
	// tag, allowing for specific components in the
	// above namespace to be tagged in at a different
	// level than the rest.
	TagOverrides map[string]string `json:"tag_overrides"`
}

// StepConfiguration holds one step configuration.
// Only one of the fields in this can be non-null.
type StepConfiguration struct {
	ImageTagStepConfiguration                   *ImageTagStepConfiguration                   `json:"image_tag_step,omitempty"`
	PipelineImageCacheStepConfiguration         *PipelineImageCacheStepConfiguration         `json:"pipeline_image_cache_step,omitempty"`
	SourceStepConfiguration                     *SourceStepConfiguration                     `json:"source_step,omitempty"`
	ProjectDirectoryImageBuildStepConfiguration *ProjectDirectoryImageBuildStepConfiguration `json:"project_directory_image_build_step,omitempty"`
	RPMImageInjectionStepConfiguration          *RPMImageInjectionStepConfiguration          `json:"rpm_image_injection_step,omitempty"`
	RPMServeStepConfiguration                   *RPMServeStepConfiguration                   `json:"rpm_serve_step,omitempty"`
}

// ImageTagStepConfiguration describes a step that
// tags an externalImage image in to the build pipeline.
// if no explicit output tag is provided, the name
// of the image is used as the tag.
type ImageTagStepConfiguration struct {
	BaseImage ImageStreamTagReference         `json:"base_image"`
	To        PipelineImageStreamTagReference `json:"to,omitempty"`
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

// PipelineImageStreamTagReference is a tag on the
// ImageStream corresponding to the code under test.
// This tag will identify an image but not use any
// namespaces or prefixes, For instance, if for the
// image openshift/origin-pod, the tag would be `pod`.
type PipelineImageStreamTagReference string

const (
	PipelineImageStreamTagReferenceBase         PipelineImageStreamTagReference = "base"
	PipelineImageStreamTagReferenceSource                                       = "src"
	PipelineImageStreamTagReferenceBinaries                                     = "bin"
	PipelineImageStreamTagReferenceTestBinaries                                 = "test-bin"
	PipelineImageStreamTagReferenceRPMs                                         = "rpms"
)

// SourceStepConfiguration describes a step that
// clones the source repositories required for
// jobs. If no output tag is provided, the default
// of `src` is used.
type SourceStepConfiguration struct {
	From PipelineImageStreamTagReference `json:"from"`
	To   PipelineImageStreamTagReference `json:"to,omitempty"`
}

// ProjectDirectoryImageBuildStepConfiguration describes an
// image build from a directory in a component project.
type ProjectDirectoryImageBuildStepConfiguration struct {
	From PipelineImageStreamTagReference `json:"from"`
	To   PipelineImageStreamTagReference `json:"to"`

	// ContextDir is the directory in the project
	// from which this build should be run.
	ContextDir string `json:"context_dir"`
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
