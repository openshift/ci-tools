package webreg

const ciOperatorReferenceYaml = "# The list of base images describe" +
	"# which images are going to be necessary outside" +
	"# of the pipeline. The key will be the alias that other" +
	"# steps use to refer to this image." +
	"base_images:" +
	"    \"\":" +
	"        # As is an optional string to use as the intermediate name for this reference." +
	"        as: ' '" +
	"        name: ' '" +
	"        namespace: ' '" +
	"        tag: ' '" +
	"# BaseRPMImages is a list of the images and their aliases that will" +
	"# have RPM repositories injected into them for downstream" +
	"# image builds that require built project RPMs." +
	"base_rpm_images:" +
	"    \"\":" +
	"        # As is an optional string to use as the intermediate name for this reference." +
	"        as: ' '" +
	"        name: ' '" +
	"        namespace: ' '" +
	"        tag: ' '" +
	"# BinaryBuildCommands will create a \"bin\" image based on \"src\" that" +
	"# contains the output of this command. This allows reuse of binary artifacts" +
	"# across other steps. If empty, no \"bin\" image will be created." +
	"binary_build_commands: ' '" +
	"# BuildRootImage supports two ways to get the image that" +
	"# the pipeline will caches on. The one way is to take the reference" +
	"# from an image stream, and the other from a dockerfile." +
	"build_root:" +
	"    image_stream_tag:" +
	"        # As is an optional string to use as the intermediate name for this reference." +
	"        as: ' '" +
	"        name: ' '" +
	"        namespace: ' '" +
	"        tag: ' '" +
	"    project_image:" +
	"        # ContextDir is the directory in the project" +
	"        # from which this build should be run." +
	"        context_dir: ' '" +
	"        # DockerfilePath is the path to a Dockerfile in the" +
	"        # project to run relative to the context_dir." +
	"        dockerfile_path: ' '" +
	"        # Inputs is a map of tag reference name to image input changes" +
	"        # that will populate the build context for the Dockerfile or" +
	"        # alter the input image for a multi-stage build." +
	"        inputs:" +
	"            \"\":" +
	"                # As is a list of multi-stage step names or image names that will" +
	"                # be replaced by the image reference from this step. For instance," +
	"                # if the Dockerfile defines FROM nginx:latest AS base, specifying" +
	"                # either \"nginx:latest\" or \"base\" in this array will replace that" +
	"                # image with the pipeline input." +
	"                as:" +
	"                  - \"\"" +
	"                # Paths is a list of paths to copy out of this image and into the" +
	"                # context directory." +
	"                paths:" +
	"                  - # DestinationDir is the directory in the destination image to copy" +
	"                    # to." +
	"                    destination_dir: ' '" +
	"                    # SourcePath is a file or directory in the source image to copy from." +
	"                    source_path: ' '" +
	"# CanonicalGoRepository is a directory path that represents" +
	"# the desired location of the contents of this repository in" +
	"# Go. If specified the location of the repository we are" +
	"# cloning from is ignored." +
	"canonical_go_repository: \"\"" +
	"# Images describes the images that are built" +
	"# baseImage the project as part of the release" +
	"# process. The name of each image is its \"to\" value" +
	"# and can be used to build only a specific image." +
	"images:" +
	"  - # ContextDir is the directory in the project" +
	"    # from which this build should be run." +
	"    context_dir: ' '" +
	"    # DockerfilePath is the path to a Dockerfile in the" +
	"    # project to run relative to the context_dir." +
	"    dockerfile_path: ' '" +
	"    from: ' '" +
	"    # Inputs is a map of tag reference name to image input changes" +
	"    # that will populate the build context for the Dockerfile or" +
	"    # alter the input image for a multi-stage build." +
	"    inputs:" +
	"        \"\":" +
	"            # As is a list of multi-stage step names or image names that will" +
	"            # be replaced by the image reference from this step. For instance," +
	"            # if the Dockerfile defines FROM nginx:latest AS base, specifying" +
	"            # either \"nginx:latest\" or \"base\" in this array will replace that" +
	"            # image with the pipeline input." +
	"            as:" +
	"              - \"\"" +
	"            # Paths is a list of paths to copy out of this image and into the" +
	"            # context directory." +
	"            paths:" +
	"              - # DestinationDir is the directory in the destination image to copy" +
	"                # to." +
	"                destination_dir: ' '" +
	"                # SourcePath is a file or directory in the source image to copy from." +
	"                source_path: ' '" +
	"    to: ' '" +
	"# Operator describes the operator bundle(s) that is built by the project" +
	"operator:" +
	"    # Bundles define a dockerfile and build context to build a bundle" +
	"    bundles:" +
	"      - context_dir: ' '" +
	"        dockerfile_path: ' '" +
	"    # Substitutions describes the pullspecs in the operator manifests that must be subsituted" +
	"    # with the pull specs of the images in the CI registry" +
	"    substitutions:" +
	"      - # PullSpec is the pullspec that needs to be replaced" +
	"        pullspec: ' '" +
	"        # With is the string that the PullSpec is being replaced by" +
	"        with: ' '" +
	"# PromotionConfiguration determines how images are promoted" +
	"# by this command. It is ignored unless promotion has specifically" +
	"# been requested. Promotion is performed after all other steps" +
	"# have been completed so that tests can be run prior to promotion." +
	"# If no promotion is defined, it is defaulted from the ReleaseTagConfiguration." +
	"promotion:" +
	"    # AdditionalImages is a mapping of images to promote. The" +
	"    # images will be taken from the pipeline image stream. The" +
	"    # key is the name to promote as and the value is the source" +
	"    # name. If you specify a tag that does not exist as the source" +
	"    # the destination tag will not be created." +
	"    additional_images:" +
	"        \"\": \"\"" +
	"    # ExcludedImages are image names that will not be promoted." +
	"    # Exclusions are made before additional_images are included." +
	"    # Use exclusions when you want to build images for testing" +
	"    # but not promote them afterwards." +
	"    excluded_images:" +
	"      - \"\"" +
	"    # Name is an optional image stream name to use that" +
	"    # contains all component tags. If specified, tag is" +
	"    # ignored." +
	"    name: ' '" +
	"    # NamePrefix is prepended to the final output image name" +
	"    # if specified." +
	"    name_prefix: ' '" +
	"    # Namespace identifies the namespace to which the built" +
	"    # artifacts will be published to." +
	"    namespace: ' '" +
	"    # Tag is the ImageStreamTag tagged in for each" +
	"    # build image's ImageStream." +
	"    tag: ' '" +
	"# RawSteps are literal Steps that should be" +
	"# included in the final pipeline." +
	"raw_steps:" +
	"  - bundle_source_step:" +
	"        # Substitutions contains pullspecs that need to be replaced by images" +
	"        # in the CI cluster for operator bundle images" +
	"        substitutions:" +
	"          - # PullSpec is the pullspec that needs to be replaced" +
	"            pullspec: ' '" +
	"            # With is the string that the PullSpec is being replaced by" +
	"            with: ' '" +
	"    index_generator_step:" +
	"        # OperatorIndex is a list of the names of the bundle images that the" +
	"        # index will contain in its database." +
	"        operator_index:" +
	"          - \"\"" +
	"        to: ' '" +
	"    input_image_tag_step:" +
	"        base_image:" +
	"            # As is an optional string to use as the intermediate name for this reference." +
	"            as: ' '" +
	"            name: ' '" +
	"            namespace: ' '" +
	"            tag: ' '" +
	"        to: ' '" +
	"    output_image_tag_step:" +
	"        from: ' '" +
	"        # Optional means the output step is not built, published, or" +
	"        # promoted unless explicitly targeted. Use for builds which" +
	"        # are invoked only when testing certain parts of the repo." +
	"        optional: false" +
	"        to:" +
	"            # As is an optional string to use as the intermediate name for this reference." +
	"            as: ' '" +
	"            name: ' '" +
	"            namespace: ' '" +
	"            tag: ' '" +
	"    pipeline_image_cache_step:" +
	"        # Commands are the shell commands to run in" +
	"        # the repository root to create the cached" +
	"        # content." +
	"        commands: ' '" +
	"        from: ' '" +
	"        to: ' '" +
	"    project_directory_image_build_inputs:" +
	"        # ContextDir is the directory in the project" +
	"        # from which this build should be run." +
	"        context_dir: ' '" +
	"        # DockerfilePath is the path to a Dockerfile in the" +
	"        # project to run relative to the context_dir." +
	"        dockerfile_path: ' '" +
	"        # Inputs is a map of tag reference name to image input changes" +
	"        # that will populate the build context for the Dockerfile or" +
	"        # alter the input image for a multi-stage build." +
	"        inputs:" +
	"            \"\":" +
	"                # As is a list of multi-stage step names or image names that will" +
	"                # be replaced by the image reference from this step. For instance," +
	"                # if the Dockerfile defines FROM nginx:latest AS base, specifying" +
	"                # either \"nginx:latest\" or \"base\" in this array will replace that" +
	"                # image with the pipeline input." +
	"                as:" +
	"                  - \"\"" +
	"                # Paths is a list of paths to copy out of this image and into the" +
	"                # context directory." +
	"                paths:" +
	"                  - # DestinationDir is the directory in the destination image to copy" +
	"                    # to." +
	"                    destination_dir: ' '" +
	"                    # SourcePath is a file or directory in the source image to copy from." +
	"                    source_path: ' '" +
	"    project_directory_image_build_step:" +
	"        # ContextDir is the directory in the project" +
	"        # from which this build should be run." +
	"        context_dir: ' '" +
	"        # DockerfilePath is the path to a Dockerfile in the" +
	"        # project to run relative to the context_dir." +
	"        dockerfile_path: ' '" +
	"        from: ' '" +
	"        # Inputs is a map of tag reference name to image input changes" +
	"        # that will populate the build context for the Dockerfile or" +
	"        # alter the input image for a multi-stage build." +
	"        inputs:" +
	"            \"\":" +
	"                # As is a list of multi-stage step names or image names that will" +
	"                # be replaced by the image reference from this step. For instance," +
	"                # if the Dockerfile defines FROM nginx:latest AS base, specifying" +
	"                # either \"nginx:latest\" or \"base\" in this array will replace that" +
	"                # image with the pipeline input." +
	"                as:" +
	"                  - \"\"" +
	"                # Paths is a list of paths to copy out of this image and into the" +
	"                # context directory." +
	"                paths:" +
	"                  - # DestinationDir is the directory in the destination image to copy" +
	"                    # to." +
	"                    destination_dir: ' '" +
	"                    # SourcePath is a file or directory in the source image to copy from." +
	"                    source_path: ' '" +
	"        to: ' '" +
	"    release_images_tag_step:" +
	"        # Name is the image stream name to use that contains all" +
	"        # component tags." +
	"        name: ' '" +
	"        # NamePrefix is prepended to the final output image name" +
	"        # if specified." +
	"        name_prefix: ' '" +
	"        # Namespace identifies the namespace from which" +
	"        # all release artifacts not built in the current" +
	"        # job are tagged from." +
	"        namespace: ' '" +
	"    resolved_release_images_step:" +
	"        candidate:" +
	"            architecture: ' '" +
	"            product: ' '" +
	"            stream: ' '" +
	"            version: ' '" +
	"        name: ' '" +
	"        prerelease:" +
	"            architecture: ' '" +
	"            product: ' '" +
	"            version_bounds:" +
	"                lower: ' '" +
	"                upper: ' '" +
	"        release:" +
	"            architecture: ' '" +
	"            channel: ' '" +
	"            version: ' '" +
	"    rpm_image_injection_step:" +
	"        from: ' '" +
	"        to: ' '" +
	"    rpm_serve_step:" +
	"        from: ' '" +
	"    source_step:" +
	"        # ClonerefsImage is the image where we get the clonerefs tool" +
	"        clonerefs_image:" +
	"            # As is an optional string to use as the intermediate name for this reference." +
	"            as: ' '" +
	"            name: ' '" +
	"            namespace: ' '" +
	"            tag: ' '" +
	"        # ClonerefsPath is the path in the above image where the" +
	"        # clonerefs tool is placed" +
	"        clonerefs_path: ' '" +
	"        from: ' '" +
	"        to: ' '" +
	"    test_step:" +
	"        # ArtifactDir is an optional directory that contains the" +
	"        # artifacts to upload. If unset, this will default to" +
	"        # \"/tmp/artifacts\"." +
	"        artifact_dir: ' '" +
	"        # As is the name of the test." +
	"        as: ' '" +
	"        # Commands are the shell commands to run in" +
	"        # the repository root to execute tests." +
	"        commands: ' '" +
	"        # Only one of the following can be not-null." +
	"        container:" +
	"            # From is the image stream tag in the pipeline to run this" +
	"            # command in." +
	"            from: ' '" +
	"            # MemoryBackedVolume mounts a volume of the specified size into" +
	"            # the container at /tmp/volume." +
	"            memory_backed_volume:" +
	"                # Size is the requested size of the volume as a Kubernetes" +
	"                # quantity, i.e. \"1Gi\" or \"500M\"" +
	"                size: ' '" +
	"        # Cron is how often the test is expected to run outside" +
	"        # of pull request workflows. Setting this field will" +
	"        # create a periodic job instead of a presubmit" +
	"        cron: \"\"" +
	"        literal_steps:" +
	"            # AllowSkipOnSuccess defines if any steps can be skipped when" +
	"            # all previous `pre` and `test` steps were successful. The given step must explicitly" +
	"            # ask for being skipped by setting the OptionalOnSuccess flag to true." +
	"            allow_skip_on_success: false" +
	"            # ClusterProfile defines the profile/cloud provider for end-to-end test steps." +
	"            cluster_profile: ' '" +
	"            # Dependencies holds override values for dependency parameters." +
	"            dependencies:" +
	"                \"\": \"\"" +
	"            # Environment has the values of parameters for the steps." +
	"            env:" +
	"                \"\": \"\"" +
	"            # Post is the array of test steps run after the tests finish and teardown/deprovision resources." +
	"            # Post steps always run, even if previous steps fail." +
	"            post:" +
	"              - # ActiveDeadlineSeconds is passed directly through to the step's Pod." +
	"                active_deadline_seconds: 0" +
	"                # ArtifactDir is the directory from which artifacts will be extracted" +
	"                # when the command finishes. Defaults to \"/tmp/artifacts\"" +
	"                artifact_dir: ' '" +
	"                # As is the name of the LiteralTestStep." +
	"                as: ' '" +
	"                # Cli is the (optional) name of the release from which the `oc` binary" +
	"                # will be injected into this step." +
	"                cli: ' '" +
	"                # Commands is the command(s) that will be run inside the image." +
	"                commands: ' '" +
	"                # Credentials defines the credentials we'll mount into this step." +
	"                credentials:" +
	"                  - # MountPath is where the secret should be mounted." +
	"                    mount_path: ' '" +
	"                    # Names is which source secret to mount." +
	"                    name: ' '" +
	"                    # Namespace is where the source secret exists." +
	"                    namespace: ' '" +
	"                # Dependencies lists images which must be available before the test runs" +
	"                # and the environment variables which are used to expose their pull specs." +
	"                dependencies:" +
	"                  - # Env is the environment variable that the image's pull spec is exposed with" +
	"                    env: ' '" +
	"                    # Name is the tag or stream:tag that this dependency references" +
	"                    name: ' '" +
	"                # Environment lists parameters that should be set by the test." +
	"                env:" +
	"                  - # Default if not set, optional, makes the parameter not required if set." +
	"                    default: \"\"" +
	"                    # Documentation is a textual description of the parameter." +
	"                    documentation: ' '" +
	"                    # Name of the environment variable." +
	"                    name: ' '" +
	"                # From is the container image that will be used for this step." +
	"                from: ' '" +
	"                # FromImage is a literal ImageStreamTag reference to use for this step." +
	"                from_image:" +
	"                    # As is an optional string to use as the intermediate name for this reference." +
	"                    as: ' '" +
	"                    name: ' '" +
	"                    namespace: ' '" +
	"                    tag: ' '" +
	"                # OptionalOnSuccess defines if this step should be skipped as long" +
	"                # as all `pre` and `test` steps were successful and AllowSkipOnSuccess" +
	"                # flag is set to true in MultiStageTestConfiguration. This option is" +
	"                # applicable to `post` steps." +
	"                optional_on_success: false" +
	"                # Resources defines the resource requirements for the step." +
	"                resources:" +
	"                    limits:" +
	"                        \"\": \"\"" +
	"                    requests:" +
	"                        \"\": \"\"" +
	"                # TerminationGracePeriodSeconds is passed directly through to the step's Pod." +
	"                termination_grace_period_seconds: 0" +
	"            # Pre is the array of test steps run to set up the environment for the test." +
	"            pre:" +
	"              - # ActiveDeadlineSeconds is passed directly through to the step's Pod." +
	"                active_deadline_seconds: 0" +
	"                # ArtifactDir is the directory from which artifacts will be extracted" +
	"                # when the command finishes. Defaults to \"/tmp/artifacts\"" +
	"                artifact_dir: ' '" +
	"                # As is the name of the LiteralTestStep." +
	"                as: ' '" +
	"                # Cli is the (optional) name of the release from which the `oc` binary" +
	"                # will be injected into this step." +
	"                cli: ' '" +
	"                # Commands is the command(s) that will be run inside the image." +
	"                commands: ' '" +
	"                # Credentials defines the credentials we'll mount into this step." +
	"                credentials:" +
	"                  - # MountPath is where the secret should be mounted." +
	"                    mount_path: ' '" +
	"                    # Names is which source secret to mount." +
	"                    name: ' '" +
	"                    # Namespace is where the source secret exists." +
	"                    namespace: ' '" +
	"                # Dependencies lists images which must be available before the test runs" +
	"                # and the environment variables which are used to expose their pull specs." +
	"                dependencies:" +
	"                  - # Env is the environment variable that the image's pull spec is exposed with" +
	"                    env: ' '" +
	"                    # Name is the tag or stream:tag that this dependency references" +
	"                    name: ' '" +
	"                # Environment lists parameters that should be set by the test." +
	"                env:" +
	"                  - # Default if not set, optional, makes the parameter not required if set." +
	"                    default: \"\"" +
	"                    # Documentation is a textual description of the parameter." +
	"                    documentation: ' '" +
	"                    # Name of the environment variable." +
	"                    name: ' '" +
	"                # From is the container image that will be used for this step." +
	"                from: ' '" +
	"                # FromImage is a literal ImageStreamTag reference to use for this step." +
	"                from_image:" +
	"                    # As is an optional string to use as the intermediate name for this reference." +
	"                    as: ' '" +
	"                    name: ' '" +
	"                    namespace: ' '" +
	"                    tag: ' '" +
	"                # OptionalOnSuccess defines if this step should be skipped as long" +
	"                # as all `pre` and `test` steps were successful and AllowSkipOnSuccess" +
	"                # flag is set to true in MultiStageTestConfiguration. This option is" +
	"                # applicable to `post` steps." +
	"                optional_on_success: false" +
	"                # Resources defines the resource requirements for the step." +
	"                resources:" +
	"                    limits:" +
	"                        \"\": \"\"" +
	"                    requests:" +
	"                        \"\": \"\"" +
	"                # TerminationGracePeriodSeconds is passed directly through to the step's Pod." +
	"                termination_grace_period_seconds: 0" +
	"            # Test is the array of test steps that define the actual test." +
	"            test:" +
	"              - # ActiveDeadlineSeconds is passed directly through to the step's Pod." +
	"                active_deadline_seconds: 0" +
	"                # ArtifactDir is the directory from which artifacts will be extracted" +
	"                # when the command finishes. Defaults to \"/tmp/artifacts\"" +
	"                artifact_dir: ' '" +
	"                # As is the name of the LiteralTestStep." +
	"                as: ' '" +
	"                # Cli is the (optional) name of the release from which the `oc` binary" +
	"                # will be injected into this step." +
	"                cli: ' '" +
	"                # Commands is the command(s) that will be run inside the image." +
	"                commands: ' '" +
	"                # Credentials defines the credentials we'll mount into this step." +
	"                credentials:" +
	"                  - # MountPath is where the secret should be mounted." +
	"                    mount_path: ' '" +
	"                    # Names is which source secret to mount." +
	"                    name: ' '" +
	"                    # Namespace is where the source secret exists." +
	"                    namespace: ' '" +
	"                # Dependencies lists images which must be available before the test runs" +
	"                # and the environment variables which are used to expose their pull specs." +
	"                dependencies:" +
	"                  - # Env is the environment variable that the image's pull spec is exposed with" +
	"                    env: ' '" +
	"                    # Name is the tag or stream:tag that this dependency references" +
	"                    name: ' '" +
	"                # Environment lists parameters that should be set by the test." +
	"                env:" +
	"                  - # Default if not set, optional, makes the parameter not required if set." +
	"                    default: \"\"" +
	"                    # Documentation is a textual description of the parameter." +
	"                    documentation: ' '" +
	"                    # Name of the environment variable." +
	"                    name: ' '" +
	"                # From is the container image that will be used for this step." +
	"                from: ' '" +
	"                # FromImage is a literal ImageStreamTag reference to use for this step." +
	"                from_image:" +
	"                    # As is an optional string to use as the intermediate name for this reference." +
	"                    as: ' '" +
	"                    name: ' '" +
	"                    namespace: ' '" +
	"                    tag: ' '" +
	"                # OptionalOnSuccess defines if this step should be skipped as long" +
	"                # as all `pre` and `test` steps were successful and AllowSkipOnSuccess" +
	"                # flag is set to true in MultiStageTestConfiguration. This option is" +
	"                # applicable to `post` steps." +
	"                optional_on_success: false" +
	"                # Resources defines the resource requirements for the step." +
	"                resources:" +
	"                    limits:" +
	"                        \"\": \"\"" +
	"                    requests:" +
	"                        \"\": \"\"" +
	"                # TerminationGracePeriodSeconds is passed directly through to the step's Pod." +
	"                termination_grace_period_seconds: 0" +
	"        openshift_ansible:" +
	"            cluster_profile: ' '" +
	"        openshift_ansible_40:" +
	"            cluster_profile: ' '" +
	"        openshift_ansible_custom:" +
	"            cluster_profile: ' '" +
	"        openshift_ansible_src:" +
	"            cluster_profile: ' '" +
	"        openshift_ansible_upgrade:" +
	"            cluster_profile: ' '" +
	"            previous_rpm_deps: ' '" +
	"            previous_version: ' '" +
	"        openshift_installer:" +
	"            cluster_profile: ' '" +
	"        openshift_installer_console:" +
	"            cluster_profile: ' '" +
	"        openshift_installer_custom_test_image:" +
	"            cluster_profile: ' '" +
	"            # From defines the imagestreamtag that will be used to run the" +
	"            # provided test command. e.g. stable:console-test" +
	"            from: ' '" +
	"        openshift_installer_src:" +
	"            cluster_profile: ' '" +
	"        openshift_installer_upi:" +
	"            cluster_profile: ' '" +
	"        openshift_installer_upi_src:" +
	"            cluster_profile: ' '" +
	"        # Secret is an optional secret object which" +
	"        # will be mounted inside the test container." +
	"        # You cannot set the Secret and Secrets attributes" +
	"        # at the same time." +
	"        secret:" +
	"            # Secret mount path. Defaults to /usr/test-secret" +
	"            mount_path: ' '" +
	"            # Secret name, used inside test containers" +
	"            name: ' '" +
	"        # Secrets is an optional array of secret objects" +
	"        # which will be mounted inside the test container." +
	"        # You cannot set the Secret and Secrets attributes" +
	"        # at the same time." +
	"        secrets:" +
	"          - # Secret mount path. Defaults to /usr/test-secret" +
	"            mount_path: ' '" +
	"            # Secret name, used inside test containers" +
	"            name: ' '" +
	"        steps:" +
	"            # AllowSkipOnSuccess defines if any steps can be skipped when" +
	"            # all previous `pre` and `test` steps were successful. The given step must explicitly" +
	"            # ask for being skipped by setting the OptionalOnSuccess flag to true." +
	"            allow_skip_on_success: false" +
	"            # ClusterProfile defines the profile/cloud provider for end-to-end test steps." +
	"            cluster_profile: ' '" +
	"            # Dependencies holds override values for dependency parameters." +
	"            dependencies:" +
	"                \"\": \"\"" +
	"            # Environment has the values of parameters for the steps." +
	"            env:" +
	"                \"\": \"\"" +
	"            # Post is the array of test steps run after the tests finish and teardown/deprovision resources." +
	"            # Post steps always run, even if previous steps fail. However, they have an option to skip" +
	"            # execution if previous Pre and Test steps passed." +
	"            post:" +
	"              # LiteralTestStep is a full test step definition." +
	"              - active_deadline_seconds: 0" +
	"                artifact_dir: ' '" +
	"                as: ' '" +
	"                # Chain is the name of a step chain reference." +
	"                chain: \"\"" +
	"                # Cli is the (optional) name of the release from which the `oc` binary" +
	"                # will be injected into this step." +
	"                cli: ' '" +
	"                commands: ' '" +
	"                credentials:" +
	"                  # LiteralTestStep is a full test step definition." +
	"                  - mount_path: ' '" +
	"                    name: ' '" +
	"                    namespace: ' '" +
	"                dependencies:" +
	"                  # LiteralTestStep is a full test step definition." +
	"                  - env: ' '" +
	"                    name: ' '" +
	"                env:" +
	"                  # LiteralTestStep is a full test step definition." +
	"                  - default: \"\"" +
	"                    documentation: ' '" +
	"                    name: ' '" +
	"                from: ' '" +
	"                from_image:" +
	"                    # LiteralTestStep is a full test step definition." +
	"                    as: ' '" +
	"                    name: ' '" +
	"                    namespace: ' '" +
	"                    tag: ' '" +
	"                optional_on_success: false" +
	"                # Reference is the name of a step reference." +
	"                ref: \"\"" +
	"                # Resources defines the resource requirements for the step." +
	"                resources:" +
	"                    # LiteralTestStep is a full test step definition." +
	"                    limits:" +
	"                        # LiteralTestStep is a full test step definition." +
	"                        \"\": \"\"" +
	"                    requests:" +
	"                        # LiteralTestStep is a full test step definition." +
	"                        \"\": \"\"" +
	"                termination_grace_period_seconds: 0" +
	"            # Pre is the array of test steps run to set up the environment for the test." +
	"            pre:" +
	"              # LiteralTestStep is a full test step definition." +
	"              - active_deadline_seconds: 0" +
	"                artifact_dir: ' '" +
	"                as: ' '" +
	"                # Chain is the name of a step chain reference." +
	"                chain: \"\"" +
	"                # Cli is the (optional) name of the release from which the `oc` binary" +
	"                # will be injected into this step." +
	"                cli: ' '" +
	"                commands: ' '" +
	"                credentials:" +
	"                  # LiteralTestStep is a full test step definition." +
	"                  - mount_path: ' '" +
	"                    name: ' '" +
	"                    namespace: ' '" +
	"                dependencies:" +
	"                  # LiteralTestStep is a full test step definition." +
	"                  - env: ' '" +
	"                    name: ' '" +
	"                env:" +
	"                  # LiteralTestStep is a full test step definition." +
	"                  - default: \"\"" +
	"                    documentation: ' '" +
	"                    name: ' '" +
	"                from: ' '" +
	"                from_image:" +
	"                    # LiteralTestStep is a full test step definition." +
	"                    as: ' '" +
	"                    name: ' '" +
	"                    namespace: ' '" +
	"                    tag: ' '" +
	"                optional_on_success: false" +
	"                # Reference is the name of a step reference." +
	"                ref: \"\"" +
	"                # Resources defines the resource requirements for the step." +
	"                resources:" +
	"                    # LiteralTestStep is a full test step definition." +
	"                    limits:" +
	"                        # LiteralTestStep is a full test step definition." +
	"                        \"\": \"\"" +
	"                    requests:" +
	"                        # LiteralTestStep is a full test step definition." +
	"                        \"\": \"\"" +
	"                termination_grace_period_seconds: 0" +
	"            # Test is the array of test steps that define the actual test." +
	"            test:" +
	"              # LiteralTestStep is a full test step definition." +
	"              - active_deadline_seconds: 0" +
	"                artifact_dir: ' '" +
	"                as: ' '" +
	"                # Chain is the name of a step chain reference." +
	"                chain: \"\"" +
	"                # Cli is the (optional) name of the release from which the `oc` binary" +
	"                # will be injected into this step." +
	"                cli: ' '" +
	"                commands: ' '" +
	"                credentials:" +
	"                  # LiteralTestStep is a full test step definition." +
	"                  - mount_path: ' '" +
	"                    name: ' '" +
	"                    namespace: ' '" +
	"                dependencies:" +
	"                  # LiteralTestStep is a full test step definition." +
	"                  - env: ' '" +
	"                    name: ' '" +
	"                env:" +
	"                  # LiteralTestStep is a full test step definition." +
	"                  - default: \"\"" +
	"                    documentation: ' '" +
	"                    name: ' '" +
	"                from: ' '" +
	"                from_image:" +
	"                    # LiteralTestStep is a full test step definition." +
	"                    as: ' '" +
	"                    name: ' '" +
	"                    namespace: ' '" +
	"                    tag: ' '" +
	"                optional_on_success: false" +
	"                # Reference is the name of a step reference." +
	"                ref: \"\"" +
	"                # Resources defines the resource requirements for the step." +
	"                resources:" +
	"                    # LiteralTestStep is a full test step definition." +
	"                    limits:" +
	"                        # LiteralTestStep is a full test step definition." +
	"                        \"\": \"\"" +
	"                    requests:" +
	"                        # LiteralTestStep is a full test step definition." +
	"                        \"\": \"\"" +
	"                termination_grace_period_seconds: 0" +
	"            # Workflow is the name of the workflow to be used for this configuration. For fields defined in both" +
	"            # the config and the workflow, the fields from the config will override what is set in Workflow." +
	"            workflow: \"\"" +
	"# Releases maps semantic release payload identifiers" +
	"# to the names that they will be exposed under. For" +
	"# instance, an 'initial' name will be exposed as" +
	"# $RELEASE_IMAGE_INITIAL. The 'latest' key is special" +
	"# and cannot co-exist with 'tag_specification', as" +
	"# they result in the same output." +
	"releases:" +
	"    \"\":" +
	"        # Candidate describes a candidate release payload" +
	"        candidate:" +
	"            # Architecture is the architecture for the product." +
	"            # Defaults to amd64." +
	"            architecture: ' '" +
	"            # Product is the name of the product being released" +
	"            product: ' '" +
	"            # ReleaseStream is the stream from which we pick the latest candidate" +
	"            stream: ' '" +
	"            # Version is the minor version to search for" +
	"            version: ' '" +
	"        # Prerelease describes a yet-to-be released payload" +
	"        prerelease:" +
	"            # Architecture is the architecture for the product." +
	"            # Defaults to amd64." +
	"            architecture: ' '" +
	"            # Product is the name of the product being released" +
	"            product: ' '" +
	"            # VersionBounds describe the allowable version bounds to search in" +
	"            version_bounds:" +
	"                lower: ' '" +
	"                upper: ' '" +
	"        # Release describes a released payload" +
	"        release:" +
	"            # Architecture is the architecture for the release." +
	"            # Defaults to amd64." +
	"            architecture: ' '" +
	"            # Channel is the release channel to search in" +
	"            channel: ' '" +
	"            # Version is the minor version to search for" +
	"            version: ' '" +
	"# Resources is a set of resource requests or limits over the" +
	"# input types. The special name '*' may be used to set default" +
	"# requests and limits." +
	"resources:" +
	"    \"\":" +
	"        limits:" +
	"            \"\": \"\"" +
	"        requests:" +
	"            \"\": \"\"" +
	"# RpmBuildCommands will create an \"rpms\" image from \"bin\" (or \"src\", if no" +
	"# binary build commands were specified) that contains the output of this" +
	"# command. The created RPMs will then be served via HTTP to the \"base\" image" +
	"# via an injected rpm.repo in the standard location at /etc/yum.repos.d." +
	"rpm_build_commands: ' '" +
	"# RpmBuildLocation is where RPms are deposited after being built. If" +
	"# unset, this will default under the repository root to" +
	"# _output/local/releases/rpms/." +
	"rpm_build_location: ' '" +
	"# ReleaseTagConfiguration determines how the" +
	"# full release is assembled." +
	"tag_specification:" +
	"    # Name is the image stream name to use that contains all" +
	"    # component tags." +
	"    name: ' '" +
	"    # NamePrefix is prepended to the final output image name" +
	"    # if specified." +
	"    name_prefix: ' '" +
	"    # Namespace identifies the namespace from which" +
	"    # all release artifacts not built in the current" +
	"    # job are tagged from." +
	"    namespace: ' '" +
	"# TestBinaryBuildCommands will create a \"test-bin\" image based on \"src\" that" +
	"# contains the output of this command. This allows reuse of binary artifacts" +
	"# across other steps. If empty, no \"test-bin\" image will be created." +
	"test_binary_build_commands: ' '" +
	"# Tests describes the tests to run inside of built images." +
	"# The images launched as pods but have no explicit access to" +
	"# the cluster they are running on." +
	"tests:" +
	"  - # ArtifactDir is an optional directory that contains the" +
	"    # artifacts to upload. If unset, this will default to" +
	"    # \"/tmp/artifacts\"." +
	"    artifact_dir: ' '" +
	"    # As is the name of the test." +
	"    as: ' '" +
	"    # Commands are the shell commands to run in" +
	"    # the repository root to execute tests." +
	"    commands: ' '" +
	"    # Only one of the following can be not-null." +
	"    container:" +
	"        # From is the image stream tag in the pipeline to run this" +
	"        # command in." +
	"        from: ' '" +
	"        # MemoryBackedVolume mounts a volume of the specified size into" +
	"        # the container at /tmp/volume." +
	"        memory_backed_volume:" +
	"            # Size is the requested size of the volume as a Kubernetes" +
	"            # quantity, i.e. \"1Gi\" or \"500M\"" +
	"            size: ' '" +
	"    # Cron is how often the test is expected to run outside" +
	"    # of pull request workflows. Setting this field will" +
	"    # create a periodic job instead of a presubmit" +
	"    cron: \"\"" +
	"    literal_steps:" +
	"        # AllowSkipOnSuccess defines if any steps can be skipped when" +
	"        # all previous `pre` and `test` steps were successful. The given step must explicitly" +
	"        # ask for being skipped by setting the OptionalOnSuccess flag to true." +
	"        allow_skip_on_success: false" +
	"        # ClusterProfile defines the profile/cloud provider for end-to-end test steps." +
	"        cluster_profile: ' '" +
	"        # Dependencies holds override values for dependency parameters." +
	"        dependencies:" +
	"            \"\": \"\"" +
	"        # Environment has the values of parameters for the steps." +
	"        env:" +
	"            \"\": \"\"" +
	"        # Post is the array of test steps run after the tests finish and teardown/deprovision resources." +
	"        # Post steps always run, even if previous steps fail." +
	"        post:" +
	"          - # ActiveDeadlineSeconds is passed directly through to the step's Pod." +
	"            active_deadline_seconds: 0" +
	"            # ArtifactDir is the directory from which artifacts will be extracted" +
	"            # when the command finishes. Defaults to \"/tmp/artifacts\"" +
	"            artifact_dir: ' '" +
	"            # As is the name of the LiteralTestStep." +
	"            as: ' '" +
	"            # Cli is the (optional) name of the release from which the `oc` binary" +
	"            # will be injected into this step." +
	"            cli: ' '" +
	"            # Commands is the command(s) that will be run inside the image." +
	"            commands: ' '" +
	"            # Credentials defines the credentials we'll mount into this step." +
	"            credentials:" +
	"              - # MountPath is where the secret should be mounted." +
	"                mount_path: ' '" +
	"                # Names is which source secret to mount." +
	"                name: ' '" +
	"                # Namespace is where the source secret exists." +
	"                namespace: ' '" +
	"            # Dependencies lists images which must be available before the test runs" +
	"            # and the environment variables which are used to expose their pull specs." +
	"            dependencies:" +
	"              - # Env is the environment variable that the image's pull spec is exposed with" +
	"                env: ' '" +
	"                # Name is the tag or stream:tag that this dependency references" +
	"                name: ' '" +
	"            # Environment lists parameters that should be set by the test." +
	"            env:" +
	"              - # Default if not set, optional, makes the parameter not required if set." +
	"                default: \"\"" +
	"                # Documentation is a textual description of the parameter." +
	"                documentation: ' '" +
	"                # Name of the environment variable." +
	"                name: ' '" +
	"            # From is the container image that will be used for this step." +
	"            from: ' '" +
	"            # FromImage is a literal ImageStreamTag reference to use for this step." +
	"            from_image:" +
	"                # As is an optional string to use as the intermediate name for this reference." +
	"                as: ' '" +
	"                name: ' '" +
	"                namespace: ' '" +
	"                tag: ' '" +
	"            # OptionalOnSuccess defines if this step should be skipped as long" +
	"            # as all `pre` and `test` steps were successful and AllowSkipOnSuccess" +
	"            # flag is set to true in MultiStageTestConfiguration. This option is" +
	"            # applicable to `post` steps." +
	"            optional_on_success: false" +
	"            # Resources defines the resource requirements for the step." +
	"            resources:" +
	"                limits:" +
	"                    \"\": \"\"" +
	"                requests:" +
	"                    \"\": \"\"" +
	"            # TerminationGracePeriodSeconds is passed directly through to the step's Pod." +
	"            termination_grace_period_seconds: 0" +
	"        # Pre is the array of test steps run to set up the environment for the test." +
	"        pre:" +
	"          - # ActiveDeadlineSeconds is passed directly through to the step's Pod." +
	"            active_deadline_seconds: 0" +
	"            # ArtifactDir is the directory from which artifacts will be extracted" +
	"            # when the command finishes. Defaults to \"/tmp/artifacts\"" +
	"            artifact_dir: ' '" +
	"            # As is the name of the LiteralTestStep." +
	"            as: ' '" +
	"            # Cli is the (optional) name of the release from which the `oc` binary" +
	"            # will be injected into this step." +
	"            cli: ' '" +
	"            # Commands is the command(s) that will be run inside the image." +
	"            commands: ' '" +
	"            # Credentials defines the credentials we'll mount into this step." +
	"            credentials:" +
	"              - # MountPath is where the secret should be mounted." +
	"                mount_path: ' '" +
	"                # Names is which source secret to mount." +
	"                name: ' '" +
	"                # Namespace is where the source secret exists." +
	"                namespace: ' '" +
	"            # Dependencies lists images which must be available before the test runs" +
	"            # and the environment variables which are used to expose their pull specs." +
	"            dependencies:" +
	"              - # Env is the environment variable that the image's pull spec is exposed with" +
	"                env: ' '" +
	"                # Name is the tag or stream:tag that this dependency references" +
	"                name: ' '" +
	"            # Environment lists parameters that should be set by the test." +
	"            env:" +
	"              - # Default if not set, optional, makes the parameter not required if set." +
	"                default: \"\"" +
	"                # Documentation is a textual description of the parameter." +
	"                documentation: ' '" +
	"                # Name of the environment variable." +
	"                name: ' '" +
	"            # From is the container image that will be used for this step." +
	"            from: ' '" +
	"            # FromImage is a literal ImageStreamTag reference to use for this step." +
	"            from_image:" +
	"                # As is an optional string to use as the intermediate name for this reference." +
	"                as: ' '" +
	"                name: ' '" +
	"                namespace: ' '" +
	"                tag: ' '" +
	"            # OptionalOnSuccess defines if this step should be skipped as long" +
	"            # as all `pre` and `test` steps were successful and AllowSkipOnSuccess" +
	"            # flag is set to true in MultiStageTestConfiguration. This option is" +
	"            # applicable to `post` steps." +
	"            optional_on_success: false" +
	"            # Resources defines the resource requirements for the step." +
	"            resources:" +
	"                limits:" +
	"                    \"\": \"\"" +
	"                requests:" +
	"                    \"\": \"\"" +
	"            # TerminationGracePeriodSeconds is passed directly through to the step's Pod." +
	"            termination_grace_period_seconds: 0" +
	"        # Test is the array of test steps that define the actual test." +
	"        test:" +
	"          - # ActiveDeadlineSeconds is passed directly through to the step's Pod." +
	"            active_deadline_seconds: 0" +
	"            # ArtifactDir is the directory from which artifacts will be extracted" +
	"            # when the command finishes. Defaults to \"/tmp/artifacts\"" +
	"            artifact_dir: ' '" +
	"            # As is the name of the LiteralTestStep." +
	"            as: ' '" +
	"            # Cli is the (optional) name of the release from which the `oc` binary" +
	"            # will be injected into this step." +
	"            cli: ' '" +
	"            # Commands is the command(s) that will be run inside the image." +
	"            commands: ' '" +
	"            # Credentials defines the credentials we'll mount into this step." +
	"            credentials:" +
	"              - # MountPath is where the secret should be mounted." +
	"                mount_path: ' '" +
	"                # Names is which source secret to mount." +
	"                name: ' '" +
	"                # Namespace is where the source secret exists." +
	"                namespace: ' '" +
	"            # Dependencies lists images which must be available before the test runs" +
	"            # and the environment variables which are used to expose their pull specs." +
	"            dependencies:" +
	"              - # Env is the environment variable that the image's pull spec is exposed with" +
	"                env: ' '" +
	"                # Name is the tag or stream:tag that this dependency references" +
	"                name: ' '" +
	"            # Environment lists parameters that should be set by the test." +
	"            env:" +
	"              - # Default if not set, optional, makes the parameter not required if set." +
	"                default: \"\"" +
	"                # Documentation is a textual description of the parameter." +
	"                documentation: ' '" +
	"                # Name of the environment variable." +
	"                name: ' '" +
	"            # From is the container image that will be used for this step." +
	"            from: ' '" +
	"            # FromImage is a literal ImageStreamTag reference to use for this step." +
	"            from_image:" +
	"                # As is an optional string to use as the intermediate name for this reference." +
	"                as: ' '" +
	"                name: ' '" +
	"                namespace: ' '" +
	"                tag: ' '" +
	"            # OptionalOnSuccess defines if this step should be skipped as long" +
	"            # as all `pre` and `test` steps were successful and AllowSkipOnSuccess" +
	"            # flag is set to true in MultiStageTestConfiguration. This option is" +
	"            # applicable to `post` steps." +
	"            optional_on_success: false" +
	"            # Resources defines the resource requirements for the step." +
	"            resources:" +
	"                limits:" +
	"                    \"\": \"\"" +
	"                requests:" +
	"                    \"\": \"\"" +
	"            # TerminationGracePeriodSeconds is passed directly through to the step's Pod." +
	"            termination_grace_period_seconds: 0" +
	"    openshift_ansible:" +
	"        cluster_profile: ' '" +
	"    openshift_ansible_40:" +
	"        cluster_profile: ' '" +
	"    openshift_ansible_custom:" +
	"        cluster_profile: ' '" +
	"    openshift_ansible_src:" +
	"        cluster_profile: ' '" +
	"    openshift_ansible_upgrade:" +
	"        cluster_profile: ' '" +
	"        previous_rpm_deps: ' '" +
	"        previous_version: ' '" +
	"    openshift_installer:" +
	"        cluster_profile: ' '" +
	"    openshift_installer_console:" +
	"        cluster_profile: ' '" +
	"    openshift_installer_custom_test_image:" +
	"        cluster_profile: ' '" +
	"        # From defines the imagestreamtag that will be used to run the" +
	"        # provided test command. e.g. stable:console-test" +
	"        from: ' '" +
	"    openshift_installer_src:" +
	"        cluster_profile: ' '" +
	"    openshift_installer_upi:" +
	"        cluster_profile: ' '" +
	"    openshift_installer_upi_src:" +
	"        cluster_profile: ' '" +
	"    # Secret is an optional secret object which" +
	"    # will be mounted inside the test container." +
	"    # You cannot set the Secret and Secrets attributes" +
	"    # at the same time." +
	"    secret:" +
	"        # Secret mount path. Defaults to /usr/test-secret" +
	"        mount_path: ' '" +
	"        # Secret name, used inside test containers" +
	"        name: ' '" +
	"    # Secrets is an optional array of secret objects" +
	"    # which will be mounted inside the test container." +
	"    # You cannot set the Secret and Secrets attributes" +
	"    # at the same time." +
	"    secrets:" +
	"      - # Secret mount path. Defaults to /usr/test-secret" +
	"        mount_path: ' '" +
	"        # Secret name, used inside test containers" +
	"        name: ' '" +
	"    steps:" +
	"        # AllowSkipOnSuccess defines if any steps can be skipped when" +
	"        # all previous `pre` and `test` steps were successful. The given step must explicitly" +
	"        # ask for being skipped by setting the OptionalOnSuccess flag to true." +
	"        allow_skip_on_success: false" +
	"        # ClusterProfile defines the profile/cloud provider for end-to-end test steps." +
	"        cluster_profile: ' '" +
	"        # Dependencies holds override values for dependency parameters." +
	"        dependencies:" +
	"            \"\": \"\"" +
	"        # Environment has the values of parameters for the steps." +
	"        env:" +
	"            \"\": \"\"" +
	"        # Post is the array of test steps run after the tests finish and teardown/deprovision resources." +
	"        # Post steps always run, even if previous steps fail. However, they have an option to skip" +
	"        # execution if previous Pre and Test steps passed." +
	"        post:" +
	"          # LiteralTestStep is a full test step definition." +
	"          - active_deadline_seconds: 0" +
	"            artifact_dir: ' '" +
	"            as: ' '" +
	"            # Chain is the name of a step chain reference." +
	"            chain: \"\"" +
	"            # Cli is the (optional) name of the release from which the `oc` binary" +
	"            # will be injected into this step." +
	"            cli: ' '" +
	"            commands: ' '" +
	"            credentials:" +
	"              # LiteralTestStep is a full test step definition." +
	"              - mount_path: ' '" +
	"                name: ' '" +
	"                namespace: ' '" +
	"            dependencies:" +
	"              # LiteralTestStep is a full test step definition." +
	"              - env: ' '" +
	"                name: ' '" +
	"            env:" +
	"              # LiteralTestStep is a full test step definition." +
	"              - default: \"\"" +
	"                documentation: ' '" +
	"                name: ' '" +
	"            from: ' '" +
	"            from_image:" +
	"                # LiteralTestStep is a full test step definition." +
	"                as: ' '" +
	"                name: ' '" +
	"                namespace: ' '" +
	"                tag: ' '" +
	"            optional_on_success: false" +
	"            # Reference is the name of a step reference." +
	"            ref: \"\"" +
	"            # Resources defines the resource requirements for the step." +
	"            resources:" +
	"                # LiteralTestStep is a full test step definition." +
	"                limits:" +
	"                    # LiteralTestStep is a full test step definition." +
	"                    \"\": \"\"" +
	"                requests:" +
	"                    # LiteralTestStep is a full test step definition." +
	"                    \"\": \"\"" +
	"            termination_grace_period_seconds: 0" +
	"        # Pre is the array of test steps run to set up the environment for the test." +
	"        pre:" +
	"          # LiteralTestStep is a full test step definition." +
	"          - active_deadline_seconds: 0" +
	"            artifact_dir: ' '" +
	"            as: ' '" +
	"            # Chain is the name of a step chain reference." +
	"            chain: \"\"" +
	"            # Cli is the (optional) name of the release from which the `oc` binary" +
	"            # will be injected into this step." +
	"            cli: ' '" +
	"            commands: ' '" +
	"            credentials:" +
	"              # LiteralTestStep is a full test step definition." +
	"              - mount_path: ' '" +
	"                name: ' '" +
	"                namespace: ' '" +
	"            dependencies:" +
	"              # LiteralTestStep is a full test step definition." +
	"              - env: ' '" +
	"                name: ' '" +
	"            env:" +
	"              # LiteralTestStep is a full test step definition." +
	"              - default: \"\"" +
	"                documentation: ' '" +
	"                name: ' '" +
	"            from: ' '" +
	"            from_image:" +
	"                # LiteralTestStep is a full test step definition." +
	"                as: ' '" +
	"                name: ' '" +
	"                namespace: ' '" +
	"                tag: ' '" +
	"            optional_on_success: false" +
	"            # Reference is the name of a step reference." +
	"            ref: \"\"" +
	"            # Resources defines the resource requirements for the step." +
	"            resources:" +
	"                # LiteralTestStep is a full test step definition." +
	"                limits:" +
	"                    # LiteralTestStep is a full test step definition." +
	"                    \"\": \"\"" +
	"                requests:" +
	"                    # LiteralTestStep is a full test step definition." +
	"                    \"\": \"\"" +
	"            termination_grace_period_seconds: 0" +
	"        # Test is the array of test steps that define the actual test." +
	"        test:" +
	"          # LiteralTestStep is a full test step definition." +
	"          - active_deadline_seconds: 0" +
	"            artifact_dir: ' '" +
	"            as: ' '" +
	"            # Chain is the name of a step chain reference." +
	"            chain: \"\"" +
	"            # Cli is the (optional) name of the release from which the `oc` binary" +
	"            # will be injected into this step." +
	"            cli: ' '" +
	"            commands: ' '" +
	"            credentials:" +
	"              # LiteralTestStep is a full test step definition." +
	"              - mount_path: ' '" +
	"                name: ' '" +
	"                namespace: ' '" +
	"            dependencies:" +
	"              # LiteralTestStep is a full test step definition." +
	"              - env: ' '" +
	"                name: ' '" +
	"            env:" +
	"              # LiteralTestStep is a full test step definition." +
	"              - default: \"\"" +
	"                documentation: ' '" +
	"                name: ' '" +
	"            from: ' '" +
	"            from_image:" +
	"                # LiteralTestStep is a full test step definition." +
	"                as: ' '" +
	"                name: ' '" +
	"                namespace: ' '" +
	"                tag: ' '" +
	"            optional_on_success: false" +
	"            # Reference is the name of a step reference." +
	"            ref: \"\"" +
	"            # Resources defines the resource requirements for the step." +
	"            resources:" +
	"                # LiteralTestStep is a full test step definition." +
	"                limits:" +
	"                    # LiteralTestStep is a full test step definition." +
	"                    \"\": \"\"" +
	"                requests:" +
	"                    # LiteralTestStep is a full test step definition." +
	"                    \"\": \"\"" +
	"            termination_grace_period_seconds: 0" +
	"        # Workflow is the name of the workflow to be used for this configuration. For fields defined in both" +
	"        # the config and the workflow, the fields from the config will override what is set in Workflow." +
	"        workflow: \"\"" +
	"zz_generated_metadata:" +
	"    branch: ' '" +
	"    org: ' '" +
	"    repo: ' '" +
	"    variant: ' '" +
	""
