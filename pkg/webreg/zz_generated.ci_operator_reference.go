package webreg

const ciOperatorReferenceYaml = "# The list of base images describe\n" +
	"# which images are going to be necessary outside\n" +
	"# of the pipeline. The key will be the alias that other\n" +
	"# steps use to refer to this image.\n" +
	"base_images:\n" +
	"    \"\":\n" +
	"        # As is an optional string to use as the intermediate name for this reference.\n" +
	"        as: ' '\n" +
	"        name: ' '\n" +
	"        namespace: ' '\n" +
	"        tag: ' '\n" +
	"# BaseRPMImages is a list of the images and their aliases that will\n" +
	"# have RPM repositories injected into them for downstream\n" +
	"# image builds that require built project RPMs.\n" +
	"base_rpm_images:\n" +
	"    \"\":\n" +
	"        # As is an optional string to use as the intermediate name for this reference.\n" +
	"        as: ' '\n" +
	"        name: ' '\n" +
	"        namespace: ' '\n" +
	"        tag: ' '\n" +
	"# BinaryBuildCommands will create a \"bin\" image based on \"src\" that\n" +
	"# contains the output of this command. This allows reuse of binary artifacts\n" +
	"# across other steps. If empty, no \"bin\" image will be created.\n" +
	"binary_build_commands: ' '\n" +
	"# BuildRootImage supports two ways to get the image that\n" +
	"# the pipeline will caches on. The one way is to take the reference\n" +
	"# from an image stream, and the other from a dockerfile.\n" +
	"build_root:\n" +
	"    # If the BuildRoot images pullspec should be read from a file in the repository (BuildRootImageFileName).\n" +
	"    from_repository: true\n" +
	"    image_stream_tag:\n" +
	"        # As is an optional string to use as the intermediate name for this reference.\n" +
	"        as: ' '\n" +
	"        name: ' '\n" +
	"        namespace: ' '\n" +
	"        tag: ' '\n" +
	"    project_image:\n" +
	"        # BuildArgs contains build arguments that will be resolved in the Dockerfile.\n" +
	"        # See https://docs.docker.com/engine/reference/builder/#/arg for more details.\n" +
	"        build_args:\n" +
	"            - # Name of the build arg.\n" +
	"              name: ' '\n" +
	"              # Value of the build arg.\n" +
	"              value: ' '\n" +
	"        # ContextDir is the directory in the project\n" +
	"        # from which this build should be run.\n" +
	"        context_dir: ' '\n" +
	"        # DockerfileLiteral can be used to provide an inline Dockerfile.\n" +
	"        # Mutually exclusive with DockerfilePath.\n" +
	"        dockerfile_literal: \"\"\n" +
	"        # DockerfilePath is the path to a Dockerfile in the\n" +
	"        # project to run relative to the context_dir.\n" +
	"        dockerfile_path: ' '\n" +
	"        # Inputs is a map of tag reference name to image input changes\n" +
	"        # that will populate the build context for the Dockerfile or\n" +
	"        # alter the input image for a multi-stage build.\n" +
	"        inputs:\n" +
	"            \"\":\n" +
	"                # As is a list of multi-stage step names or image names that will\n" +
	"                # be replaced by the image reference from this step. For instance,\n" +
	"                # if the Dockerfile defines FROM nginx:latest AS base, specifying\n" +
	"                # either \"nginx:latest\" or \"base\" in this array will replace that\n" +
	"                # image with the pipeline input.\n" +
	"                as:\n" +
	"                    - \"\"\n" +
	"                # Paths is a list of paths to copy out of this image and into the\n" +
	"                # context directory.\n" +
	"                paths:\n" +
	"                    - # DestinationDir is the directory in the destination image to copy\n" +
	"                      # to.\n" +
	"                      destination_dir: ' '\n" +
	"                      # SourcePath is a file or directory in the source image to copy from.\n" +
	"                      source_path: ' '\n" +
	"    # UseBuildCache enables the import and use of the prior `bin` image\n" +
	"    # as a build cache, if the underlying build root has not changed since\n" +
	"    # the previous cache was published.\n" +
	"    use_build_cache: true\n" +
	"# CanonicalGoRepository is a directory path that represents\n" +
	"# the desired location of the contents of this repository in\n" +
	"# Go. If specified the location of the repository we are\n" +
	"# cloning from is ignored.\n" +
	"canonical_go_repository: \"\"\n" +
	"# Images describes the images that are built\n" +
	"# baseImage the project as part of the release\n" +
	"# process. The name of each image is its \"to\" value\n" +
	"# and can be used to build only a specific image.\n" +
	"images:\n" +
	"    - # BuildArgs contains build arguments that will be resolved in the Dockerfile.\n" +
	"      # See https://docs.docker.com/engine/reference/builder/#/arg for more details.\n" +
	"      build_args:\n" +
	"        - # Name of the build arg.\n" +
	"          name: ' '\n" +
	"          # Value of the build arg.\n" +
	"          value: ' '\n" +
	"      # ContextDir is the directory in the project\n" +
	"      # from which this build should be run.\n" +
	"      context_dir: ' '\n" +
	"      # DockerfileLiteral can be used to provide an inline Dockerfile.\n" +
	"      # Mutually exclusive with DockerfilePath.\n" +
	"      dockerfile_literal: \"\"\n" +
	"      # DockerfilePath is the path to a Dockerfile in the\n" +
	"      # project to run relative to the context_dir.\n" +
	"      dockerfile_path: ' '\n" +
	"      from: ' '\n" +
	"      # Inputs is a map of tag reference name to image input changes\n" +
	"      # that will populate the build context for the Dockerfile or\n" +
	"      # alter the input image for a multi-stage build.\n" +
	"      inputs:\n" +
	"        \"\":\n" +
	"            # As is a list of multi-stage step names or image names that will\n" +
	"            # be replaced by the image reference from this step. For instance,\n" +
	"            # if the Dockerfile defines FROM nginx:latest AS base, specifying\n" +
	"            # either \"nginx:latest\" or \"base\" in this array will replace that\n" +
	"            # image with the pipeline input.\n" +
	"            as:\n" +
	"                - \"\"\n" +
	"            # Paths is a list of paths to copy out of this image and into the\n" +
	"            # context directory.\n" +
	"            paths:\n" +
	"                - # DestinationDir is the directory in the destination image to copy\n" +
	"                  # to.\n" +
	"                  destination_dir: ' '\n" +
	"                  # SourcePath is a file or directory in the source image to copy from.\n" +
	"                  source_path: ' '\n" +
	"      # Optional means the build step is not built, published, or\n" +
	"      # promoted unless explicitly targeted. Use for builds which\n" +
	"      # are invoked only when testing certain parts of the repo.\n" +
	"      optional: true\n" +
	"      to: ' '\n" +
	"# Operator describes the operator bundle(s) that is built by the project\n" +
	"operator:\n" +
	"    # Bundles define a dockerfile and build context to build a bundle\n" +
	"    bundles:\n" +
	"        - # As defines the name for this bundle. If not set, a name will be automatically generated for the bundle.\n" +
	"          as: ' '\n" +
	"          # BaseIndex defines what index image to use as a base when adding the bundle to an index\n" +
	"          base_index: ' '\n" +
	"          # ContextDir defines the source directory to build the bundle from relative to the repository root\n" +
	"          context_dir: ' '\n" +
	"          # DockerfilePath defines where the dockerfile for build the bundle exists relative to the contextdir\n" +
	"          dockerfile_path: ' '\n" +
	"          # UpdateGraph defines the update mode to use when adding the bundle to the base index.\n" +
	"          # Can be: semver (default), semver-skippatch, or replaces\n" +
	"          update_graph: ' '\n" +
	"    # Substitutions describes the pullspecs in the operator manifests that must be subsituted\n" +
	"    # with the pull specs of the images in the CI registry\n" +
	"    substitutions:\n" +
	"        - # PullSpec is the pullspec that needs to be replaced\n" +
	"          pullspec: ' '\n" +
	"          # With is the string that the PullSpec is being replaced by\n" +
	"          with: ' '\n" +
	"# PromotionConfiguration determines how images are promoted\n" +
	"# by this command. It is ignored unless promotion has specifically\n" +
	"# been requested. Promotion is performed after all other steps\n" +
	"# have been completed so that tests can be run prior to promotion.\n" +
	"# If no promotion is defined, it is defaulted from the ReleaseTagConfiguration.\n" +
	"promotion:\n" +
	"    # AdditionalImages is a mapping of images to promote. The\n" +
	"    # images will be taken from the pipeline image stream. The\n" +
	"    # key is the name to promote as and the value is the source\n" +
	"    # name. If you specify a tag that does not exist as the source\n" +
	"    # the destination tag will not be created.\n" +
	"    additional_images:\n" +
	"        \"\": \"\"\n" +
	"    # DisableBuildCache stops us from uploading the build cache.\n" +
	"    # This is useful (only) for CI chat bot invocations where\n" +
	"    # promotion does not imply output artifacts are being created\n" +
	"    # for posterity.\n" +
	"    disable_build_cache: true\n" +
	"    # Disabled will no-op succeed instead of running the actual\n" +
	"    # promotion step. This is useful when two branches need to\n" +
	"    # promote to the same output imagestream on a cut-over but\n" +
	"    # never concurrently, and you want to have promotion config\n" +
	"    # in the ci-operator configuration files all the time.\n" +
	"    disabled: true\n" +
	"    # ExcludedImages are image names that will not be promoted.\n" +
	"    # Exclusions are made before additional_images are included.\n" +
	"    # Use exclusions when you want to build images for testing\n" +
	"    # but not promote them afterwards.\n" +
	"    excluded_images:\n" +
	"        - \"\"\n" +
	"    # Name is an optional image stream name to use that\n" +
	"    # contains all component tags. If specified, tag is\n" +
	"    # ignored.\n" +
	"    name: ' '\n" +
	"    # Namespace identifies the namespace to which the built\n" +
	"    # artifacts will be published to.\n" +
	"    namespace: ' '\n" +
	"    # RegistryOverride is an override for the registry domain to\n" +
	"    # which we will mirror images. This is an advanced option and\n" +
	"    # should *not* be used in common test workflows. The CI chat\n" +
	"    # bot uses this option to facilitate image sharing.\n" +
	"    registry_override: ' '\n" +
	"    # Tag is the ImageStreamTag tagged in for each\n" +
	"    # build image's ImageStream.\n" +
	"    tag: ' '\n" +
	"# RawSteps are literal Steps that should be\n" +
	"# included in the final pipeline.\n" +
	"raw_steps:\n" +
	"    - bundle_source_step:\n" +
	"        # Substitutions contains pullspecs that need to be replaced by images\n" +
	"        # in the CI cluster for operator bundle images\n" +
	"        substitutions:\n" +
	"            - # PullSpec is the pullspec that needs to be replaced\n" +
	"              pullspec: ' '\n" +
	"              # With is the string that the PullSpec is being replaced by\n" +
	"              with: ' '\n" +
	"      index_generator_step:\n" +
	"        # BaseIndex is the index image to add the bundle(s) to. If unset, a new index is created\n" +
	"        base_index: ' '\n" +
	"        # OperatorIndex is a list of the names of the bundle images that the\n" +
	"        # index will contain in its database.\n" +
	"        operator_index:\n" +
	"            - \"\"\n" +
	"        to: ' '\n" +
	"        # UpdateGraph defines the mode to us when updating the index graph\n" +
	"        update_graph: ' '\n" +
	"      input_image_tag_step:\n" +
	"        base_image:\n" +
	"            # As is an optional string to use as the intermediate name for this reference.\n" +
	"            as: ' '\n" +
	"            name: ' '\n" +
	"            namespace: ' '\n" +
	"            tag: ' '\n" +
	"        to: ' '\n" +
	"      output_image_tag_step:\n" +
	"        from: ' '\n" +
	"        # Optional means the output step is not built, published, or\n" +
	"        # promoted unless explicitly targeted. Use for builds which\n" +
	"        # are invoked only when testing certain parts of the repo.\n" +
	"        optional: false\n" +
	"        to:\n" +
	"            # As is an optional string to use as the intermediate name for this reference.\n" +
	"            as: ' '\n" +
	"            name: ' '\n" +
	"            namespace: ' '\n" +
	"            tag: ' '\n" +
	"      pipeline_image_cache_step:\n" +
	"        # Commands are the shell commands to run in\n" +
	"        # the repository root to create the cached\n" +
	"        # content.\n" +
	"        commands: ' '\n" +
	"        from: ' '\n" +
	"        to: ' '\n" +
	"      project_directory_image_build_inputs:\n" +
	"        # BuildArgs contains build arguments that will be resolved in the Dockerfile.\n" +
	"        # See https://docs.docker.com/engine/reference/builder/#/arg for more details.\n" +
	"        build_args:\n" +
	"            - # Name of the build arg.\n" +
	"              name: ' '\n" +
	"              # Value of the build arg.\n" +
	"              value: ' '\n" +
	"        # ContextDir is the directory in the project\n" +
	"        # from which this build should be run.\n" +
	"        context_dir: ' '\n" +
	"        # DockerfileLiteral can be used to provide an inline Dockerfile.\n" +
	"        # Mutually exclusive with DockerfilePath.\n" +
	"        dockerfile_literal: \"\"\n" +
	"        # DockerfilePath is the path to a Dockerfile in the\n" +
	"        # project to run relative to the context_dir.\n" +
	"        dockerfile_path: ' '\n" +
	"        # Inputs is a map of tag reference name to image input changes\n" +
	"        # that will populate the build context for the Dockerfile or\n" +
	"        # alter the input image for a multi-stage build.\n" +
	"        inputs:\n" +
	"            \"\":\n" +
	"                # As is a list of multi-stage step names or image names that will\n" +
	"                # be replaced by the image reference from this step. For instance,\n" +
	"                # if the Dockerfile defines FROM nginx:latest AS base, specifying\n" +
	"                # either \"nginx:latest\" or \"base\" in this array will replace that\n" +
	"                # image with the pipeline input.\n" +
	"                as:\n" +
	"                    - \"\"\n" +
	"                # Paths is a list of paths to copy out of this image and into the\n" +
	"                # context directory.\n" +
	"                paths:\n" +
	"                    - # DestinationDir is the directory in the destination image to copy\n" +
	"                      # to.\n" +
	"                      destination_dir: ' '\n" +
	"                      # SourcePath is a file or directory in the source image to copy from.\n" +
	"                      source_path: ' '\n" +
	"      project_directory_image_build_step:\n" +
	"        # BuildArgs contains build arguments that will be resolved in the Dockerfile.\n" +
	"        # See https://docs.docker.com/engine/reference/builder/#/arg for more details.\n" +
	"        build_args:\n" +
	"            - # Name of the build arg.\n" +
	"              name: ' '\n" +
	"              # Value of the build arg.\n" +
	"              value: ' '\n" +
	"        # ContextDir is the directory in the project\n" +
	"        # from which this build should be run.\n" +
	"        context_dir: ' '\n" +
	"        # DockerfileLiteral can be used to provide an inline Dockerfile.\n" +
	"        # Mutually exclusive with DockerfilePath.\n" +
	"        dockerfile_literal: \"\"\n" +
	"        # DockerfilePath is the path to a Dockerfile in the\n" +
	"        # project to run relative to the context_dir.\n" +
	"        dockerfile_path: ' '\n" +
	"        from: ' '\n" +
	"        # Inputs is a map of tag reference name to image input changes\n" +
	"        # that will populate the build context for the Dockerfile or\n" +
	"        # alter the input image for a multi-stage build.\n" +
	"        inputs:\n" +
	"            \"\":\n" +
	"                # As is a list of multi-stage step names or image names that will\n" +
	"                # be replaced by the image reference from this step. For instance,\n" +
	"                # if the Dockerfile defines FROM nginx:latest AS base, specifying\n" +
	"                # either \"nginx:latest\" or \"base\" in this array will replace that\n" +
	"                # image with the pipeline input.\n" +
	"                as:\n" +
	"                    - \"\"\n" +
	"                # Paths is a list of paths to copy out of this image and into the\n" +
	"                # context directory.\n" +
	"                paths:\n" +
	"                    - # DestinationDir is the directory in the destination image to copy\n" +
	"                      # to.\n" +
	"                      destination_dir: ' '\n" +
	"                      # SourcePath is a file or directory in the source image to copy from.\n" +
	"                      source_path: ' '\n" +
	"        # Optional means the build step is not built, published, or\n" +
	"        # promoted unless explicitly targeted. Use for builds which\n" +
	"        # are invoked only when testing certain parts of the repo.\n" +
	"        optional: true\n" +
	"        to: ' '\n" +
	"      release_images_tag_step:\n" +
	"        # IncludeBuiltImages determines if the release we assemble will include\n" +
	"        # images built during the test itself.\n" +
	"        include_built_images: true\n" +
	"        # Name is the image stream name to use that contains all\n" +
	"        # component tags.\n" +
	"        name: ' '\n" +
	"        # Namespace identifies the namespace from which\n" +
	"        # all release artifacts not built in the current\n" +
	"        # job are tagged from.\n" +
	"        namespace: ' '\n" +
	"      resolved_release_images_step:\n" +
	"        # Candidate describes a candidate release payload\n" +
	"        candidate:\n" +
	"            # Architecture is the architecture for the product.\n" +
	"            # Defaults to amd64.\n" +
	"            architecture: ' '\n" +
	"            # Product is the name of the product being released\n" +
	"            product: ' '\n" +
	"            # ReleaseStream is the stream from which we pick the latest candidate\n" +
	"            stream: ' '\n" +
	"            # Version is the minor version to search for\n" +
	"            version: ' '\n" +
	"        # Integration describes an integration stream which we can create a payload out of\n" +
	"        integration:\n" +
	"            # IncludeBuiltImages determines if the release we assemble will include\n" +
	"            # images built during the test itself.\n" +
	"            include_built_images: true\n" +
	"            # Name is the name of the ImageStream\n" +
	"            name: ' '\n" +
	"            # Namespace is the namespace in which the integration stream lives.\n" +
	"            namespace: ' '\n" +
	"        name: ' '\n" +
	"        # Prerelease describes a yet-to-be released payload\n" +
	"        prerelease:\n" +
	"            # Architecture is the architecture for the product.\n" +
	"            # Defaults to amd64.\n" +
	"            architecture: ' '\n" +
	"            # Product is the name of the product being released\n" +
	"            product: ' '\n" +
	"            # VersionBounds describe the allowable version bounds to search in\n" +
	"            version_bounds:\n" +
	"                lower: ' '\n" +
	"                upper: ' '\n" +
	"        # Release describes a released payload\n" +
	"        release:\n" +
	"            # Architecture is the architecture for the release.\n" +
	"            # Defaults to amd64.\n" +
	"            architecture: ' '\n" +
	"            # Channel is the release channel to search in\n" +
	"            channel: ' '\n" +
	"            # Version is the minor version to search for\n" +
	"            version: ' '\n" +
	"      rpm_image_injection_step:\n" +
	"        from: ' '\n" +
	"        to: ' '\n" +
	"      rpm_serve_step:\n" +
	"        from: ' '\n" +
	"      source_step:\n" +
	"        # ClonerefsImage is the image where we get the clonerefs tool\n" +
	"        clonerefs_image:\n" +
	"            # As is an optional string to use as the intermediate name for this reference.\n" +
	"            as: ' '\n" +
	"            name: ' '\n" +
	"            namespace: ' '\n" +
	"            tag: ' '\n" +
	"        # ClonerefsPath is the path in the above image where the\n" +
	"        # clonerefs tool is placed\n" +
	"        clonerefs_path: ' '\n" +
	"        from: ' '\n" +
	"        to: ' '\n" +
	"      test_step:\n" +
	"        # As is the name of the test.\n" +
	"        as: ' '\n" +
	"        # Cluster specifies the name of the cluster where the test runs.\n" +
	"        cluster: ' '\n" +
	"        # ClusterClaim claims an OpenShift cluster and exposes environment variable ${KUBECONFIG} to the test container\n" +
	"        cluster_claim:\n" +
	"            # Architecture is the architecture for the product.\n" +
	"            # Defaults to amd64.\n" +
	"            architecture: ' '\n" +
	"            # As is the name to use when importing the cluster claim release payload.\n" +
	"            # If unset, claim release will be imported as `latest`.\n" +
	"            as: ' '\n" +
	"            # Cloud is the cloud where the product is installed, e.g., aws.\n" +
	"            cloud: ' '\n" +
	"            # Labels is the labels to select the cluster pools\n" +
	"            labels:\n" +
	"                \"\": \"\"\n" +
	"            # Owner is the owner of cloud account used to install the product, e.g., dpp.\n" +
	"            owner: ' '\n" +
	"            # Product is the name of the product being released.\n" +
	"            # Defaults to ocp.\n" +
	"            product: ' '\n" +
	"            # Timeout is how long ci-operator will wait for the cluster to be ready.\n" +
	"            # Defaults to 1h.\n" +
	"            timeout: 0s\n" +
	"            # Version is the version of the product\n" +
	"            version: ' '\n" +
	"        # Commands are the shell commands to run in\n" +
	"        # the repository root to execute tests.\n" +
	"        commands: ' '\n" +
	"        # Only one of the following can be not-null.\n" +
	"        container:\n" +
	"            # If the step should clone the source code prior to running the command.\n" +
	"            # Defaults to `true` for `base_images`, `false` otherwise.\n" +
	"            clone: false\n" +
	"            # From is the image stream tag in the pipeline to run this\n" +
	"            # command in.\n" +
	"            from: ' '\n" +
	"            # MemoryBackedVolume mounts a volume of the specified size into\n" +
	"            # the container at /tmp/volume.\n" +
	"            memory_backed_volume:\n" +
	"                # Size is the requested size of the volume as a Kubernetes\n" +
	"                # quantity, i.e. \"1Gi\" or \"500M\"\n" +
	"                size: ' '\n" +
	"        # Cron is how often the test is expected to run outside\n" +
	"        # of pull request workflows. Setting this field will\n" +
	"        # create a periodic job instead of a presubmit\n" +
	"        cron: \"\"\n" +
	"        # Interval is how frequently the test should be run based\n" +
	"        # on the last time the test ran. Setting this field will\n" +
	"        # create a periodic job instead of a presubmit\n" +
	"        interval: \"\"\n" +
	"        literal_steps:\n" +
	"            # AllowBestEffortPostSteps defines if any `post` steps can be ignored when\n" +
	"            # they fail. The given step must explicitly ask for being ignored by setting\n" +
	"            # the OptionalOnSuccess flag to true.\n" +
	"            allow_best_effort_post_steps: false\n" +
	"            # AllowSkipOnSuccess defines if any steps can be skipped when\n" +
	"            # all previous `pre` and `test` steps were successful. The given step must explicitly\n" +
	"            # ask for being skipped by setting the OptionalOnSuccess flag to true.\n" +
	"            allow_skip_on_success: false\n" +
	"            # ClusterProfile defines the profile/cloud provider for end-to-end test steps.\n" +
	"            cluster_profile: ' '\n" +
	"            # Dependencies holds override values for dependency parameters.\n" +
	"            dependencies:\n" +
	"                \"\": \"\"\n" +
	"            # DependencyOverrides allows a step to override a dependency with a fully-qualified pullspec. This will probably only ever\n" +
	"            # be used with rehearsals. Otherwise, the overrides should be passed in as parameters to ci-operator.\n" +
	"            dependency_overrides:\n" +
	"                \"\": \"\"\n" +
	"            # DnsConfig for step's Pod.\n" +
	"            dnsConfig:\n" +
	"                # Nameservers is a list of IP addresses that will be used as DNS servers for the Pod\n" +
	"                nameservers:\n" +
	"                    - \"\"\n" +
	"                # Searches is a list of DNS search domains for host-name lookup\n" +
	"                searches:\n" +
	"                    - \"\"\n" +
	"            # Environment has the values of parameters for the steps.\n" +
	"            env:\n" +
	"                \"\": \"\"\n" +
	"            # Leases lists resources that should be acquired for the test.\n" +
	"            leases:\n" +
	"                - # Env is the environment variable that will contain the resource name.\n" +
	"                  env: ' '\n" +
	"                  # ResourceType is the type of resource that will be leased.\n" +
	"                  resource_type: ' '\n" +
	"            # Observers are the observers that need to be run\n" +
	"            observers:\n" +
	"                - # Commands is the command(s) that will be run inside the image.\n" +
	"                  commands: ' '\n" +
	"                  # From is the container image that will be used for this observer.\n" +
	"                  from: ' '\n" +
	"                  # FromImage is a literal ImageStreamTag reference to use for this observer.\n" +
	"                  from_image:\n" +
	"                    # As is an optional string to use as the intermediate name for this reference.\n" +
	"                    as: ' '\n" +
	"                    name: ' '\n" +
	"                    namespace: ' '\n" +
	"                    tag: ' '\n" +
	"                  # Name is the name of this observer\n" +
	"                  name: ' '\n" +
	"                  # Resources defines the resource requirements for the step.\n" +
	"                  resources:\n" +
	"                    # Limits are resource limits applied to an individual step in the job.\n" +
	"                    # These are directly used in creating the Pods that execute the Job.\n" +
	"                    limits:\n" +
	"                        \"\": \"\"\n" +
	"                    # Requests are resource requests applied to an individual step in the job.\n" +
	"                    # These are directly used in creating the Pods that execute the Job.\n" +
	"                    requests:\n" +
	"                        \"\": \"\"\n" +
	"            # Post is the array of test steps run after the tests finish and teardown/deprovision resources.\n" +
	"            # Post steps always run, even if previous steps fail.\n" +
	"            post:\n" +
	"                - # As is the name of the LiteralTestStep.\n" +
	"                  as: ' '\n" +
	"                  # BestEffort defines if this step should cause the job to fail when the\n" +
	"                  # step fails. This only applies when AllowBestEffortPostSteps flag is set\n" +
	"                  # to true in MultiStageTestConfiguration. This option is applicable to\n" +
	"                  # `post` steps.\n" +
	"                  best_effort: false\n" +
	"                  # Cli is the (optional) name of the release from which the `oc` binary\n" +
	"                  # will be injected into this step.\n" +
	"                  cli: ' '\n" +
	"                  # Commands is the command(s) that will be run inside the image.\n" +
	"                  commands: ' '\n" +
	"                  # Credentials defines the credentials we'll mount into this step.\n" +
	"                  credentials:\n" +
	"                    - # MountPath is where the secret should be mounted.\n" +
	"                      mount_path: ' '\n" +
	"                      # Names is which source secret to mount.\n" +
	"                      name: ' '\n" +
	"                      # Namespace is where the source secret exists.\n" +
	"                      namespace: ' '\n" +
	"                  # Dependencies lists images which must be available before the test runs\n" +
	"                  # and the environment variables which are used to expose their pull specs.\n" +
	"                  dependencies:\n" +
	"                    - # Env is the environment variable that the image's pull spec is exposed with\n" +
	"                      env: ' '\n" +
	"                      # Name is the tag or stream:tag that this dependency references\n" +
	"                      name: ' '\n" +
	"                  # DnsConfig for step's Pod.\n" +
	"                  dnsConfig:\n" +
	"                    # Nameservers is a list of IP addresses that will be used as DNS servers for the Pod\n" +
	"                    nameservers:\n" +
	"                        - \"\"\n" +
	"                    # Searches is a list of DNS search domains for host-name lookup\n" +
	"                    searches:\n" +
	"                        - \"\"\n" +
	"                  # Environment lists parameters that should be set by the test.\n" +
	"                  env:\n" +
	"                    - # Default if not set, optional, makes the parameter not required if set.\n" +
	"                      default: \"\"\n" +
	"                      # Documentation is a textual description of the parameter.\n" +
	"                      documentation: ' '\n" +
	"                      # Name of the environment variable.\n" +
	"                      name: ' '\n" +
	"                  # From is the container image that will be used for this step.\n" +
	"                  from: ' '\n" +
	"                  # FromImage is a literal ImageStreamTag reference to use for this step.\n" +
	"                  from_image:\n" +
	"                    # As is an optional string to use as the intermediate name for this reference.\n" +
	"                    as: ' '\n" +
	"                    name: ' '\n" +
	"                    namespace: ' '\n" +
	"                    tag: ' '\n" +
	"                  # GracePeriod is how long the we will wait after sending SIGINT to send\n" +
	"                  # SIGKILL when aborting a Step.\n" +
	"                  grace_period: 0s\n" +
	"                  # Leases lists resources that should be acquired for the test.\n" +
	"                  leases:\n" +
	"                    - # Env is the environment variable that will contain the resource name.\n" +
	"                      env: ' '\n" +
	"                      # ResourceType is the type of resource that will be leased.\n" +
	"                      resource_type: ' '\n" +
	"                  # Observers are the observers that should be running\n" +
	"                  observers:\n" +
	"                    - \"\"\n" +
	"                  # OptionalOnSuccess defines if this step should be skipped as long\n" +
	"                  # as all `pre` and `test` steps were successful and AllowSkipOnSuccess\n" +
	"                  # flag is set to true in MultiStageTestConfiguration. This option is\n" +
	"                  # applicable to `post` steps.\n" +
	"                  optional_on_success: false\n" +
	"                  # Resources defines the resource requirements for the step.\n" +
	"                  resources:\n" +
	"                    # Limits are resource limits applied to an individual step in the job.\n" +
	"                    # These are directly used in creating the Pods that execute the Job.\n" +
	"                    limits:\n" +
	"                        \"\": \"\"\n" +
	"                    # Requests are resource requests applied to an individual step in the job.\n" +
	"                    # These are directly used in creating the Pods that execute the Job.\n" +
	"                    requests:\n" +
	"                        \"\": \"\"\n" +
	"                  # RunAsScript defines if this step should be executed as a script mounted\n" +
	"                  # in the test container instead of being executed directly via bash\n" +
	"                  run_as_script: false\n" +
	"                  # Timeout is how long the we will wait before aborting a job with SIGINT.\n" +
	"                  timeout: 0s\n" +
	"            # Pre is the array of test steps run to set up the environment for the test.\n" +
	"            pre:\n" +
	"                - # As is the name of the LiteralTestStep.\n" +
	"                  as: ' '\n" +
	"                  # BestEffort defines if this step should cause the job to fail when the\n" +
	"                  # step fails. This only applies when AllowBestEffortPostSteps flag is set\n" +
	"                  # to true in MultiStageTestConfiguration. This option is applicable to\n" +
	"                  # `post` steps.\n" +
	"                  best_effort: false\n" +
	"                  # Cli is the (optional) name of the release from which the `oc` binary\n" +
	"                  # will be injected into this step.\n" +
	"                  cli: ' '\n" +
	"                  # Commands is the command(s) that will be run inside the image.\n" +
	"                  commands: ' '\n" +
	"                  # Credentials defines the credentials we'll mount into this step.\n" +
	"                  credentials:\n" +
	"                    - # MountPath is where the secret should be mounted.\n" +
	"                      mount_path: ' '\n" +
	"                      # Names is which source secret to mount.\n" +
	"                      name: ' '\n" +
	"                      # Namespace is where the source secret exists.\n" +
	"                      namespace: ' '\n" +
	"                  # Dependencies lists images which must be available before the test runs\n" +
	"                  # and the environment variables which are used to expose their pull specs.\n" +
	"                  dependencies:\n" +
	"                    - # Env is the environment variable that the image's pull spec is exposed with\n" +
	"                      env: ' '\n" +
	"                      # Name is the tag or stream:tag that this dependency references\n" +
	"                      name: ' '\n" +
	"                  # DnsConfig for step's Pod.\n" +
	"                  dnsConfig:\n" +
	"                    # Nameservers is a list of IP addresses that will be used as DNS servers for the Pod\n" +
	"                    nameservers:\n" +
	"                        - \"\"\n" +
	"                    # Searches is a list of DNS search domains for host-name lookup\n" +
	"                    searches:\n" +
	"                        - \"\"\n" +
	"                  # Environment lists parameters that should be set by the test.\n" +
	"                  env:\n" +
	"                    - # Default if not set, optional, makes the parameter not required if set.\n" +
	"                      default: \"\"\n" +
	"                      # Documentation is a textual description of the parameter.\n" +
	"                      documentation: ' '\n" +
	"                      # Name of the environment variable.\n" +
	"                      name: ' '\n" +
	"                  # From is the container image that will be used for this step.\n" +
	"                  from: ' '\n" +
	"                  # FromImage is a literal ImageStreamTag reference to use for this step.\n" +
	"                  from_image:\n" +
	"                    # As is an optional string to use as the intermediate name for this reference.\n" +
	"                    as: ' '\n" +
	"                    name: ' '\n" +
	"                    namespace: ' '\n" +
	"                    tag: ' '\n" +
	"                  # GracePeriod is how long the we will wait after sending SIGINT to send\n" +
	"                  # SIGKILL when aborting a Step.\n" +
	"                  grace_period: 0s\n" +
	"                  # Leases lists resources that should be acquired for the test.\n" +
	"                  leases:\n" +
	"                    - # Env is the environment variable that will contain the resource name.\n" +
	"                      env: ' '\n" +
	"                      # ResourceType is the type of resource that will be leased.\n" +
	"                      resource_type: ' '\n" +
	"                  # Observers are the observers that should be running\n" +
	"                  observers:\n" +
	"                    - \"\"\n" +
	"                  # OptionalOnSuccess defines if this step should be skipped as long\n" +
	"                  # as all `pre` and `test` steps were successful and AllowSkipOnSuccess\n" +
	"                  # flag is set to true in MultiStageTestConfiguration. This option is\n" +
	"                  # applicable to `post` steps.\n" +
	"                  optional_on_success: false\n" +
	"                  # Resources defines the resource requirements for the step.\n" +
	"                  resources:\n" +
	"                    # Limits are resource limits applied to an individual step in the job.\n" +
	"                    # These are directly used in creating the Pods that execute the Job.\n" +
	"                    limits:\n" +
	"                        \"\": \"\"\n" +
	"                    # Requests are resource requests applied to an individual step in the job.\n" +
	"                    # These are directly used in creating the Pods that execute the Job.\n" +
	"                    requests:\n" +
	"                        \"\": \"\"\n" +
	"                  # RunAsScript defines if this step should be executed as a script mounted\n" +
	"                  # in the test container instead of being executed directly via bash\n" +
	"                  run_as_script: false\n" +
	"                  # Timeout is how long the we will wait before aborting a job with SIGINT.\n" +
	"                  timeout: 0s\n" +
	"            # Test is the array of test steps that define the actual test.\n" +
	"            test:\n" +
	"                - # As is the name of the LiteralTestStep.\n" +
	"                  as: ' '\n" +
	"                  # BestEffort defines if this step should cause the job to fail when the\n" +
	"                  # step fails. This only applies when AllowBestEffortPostSteps flag is set\n" +
	"                  # to true in MultiStageTestConfiguration. This option is applicable to\n" +
	"                  # `post` steps.\n" +
	"                  best_effort: false\n" +
	"                  # Cli is the (optional) name of the release from which the `oc` binary\n" +
	"                  # will be injected into this step.\n" +
	"                  cli: ' '\n" +
	"                  # Commands is the command(s) that will be run inside the image.\n" +
	"                  commands: ' '\n" +
	"                  # Credentials defines the credentials we'll mount into this step.\n" +
	"                  credentials:\n" +
	"                    - # MountPath is where the secret should be mounted.\n" +
	"                      mount_path: ' '\n" +
	"                      # Names is which source secret to mount.\n" +
	"                      name: ' '\n" +
	"                      # Namespace is where the source secret exists.\n" +
	"                      namespace: ' '\n" +
	"                  # Dependencies lists images which must be available before the test runs\n" +
	"                  # and the environment variables which are used to expose their pull specs.\n" +
	"                  dependencies:\n" +
	"                    - # Env is the environment variable that the image's pull spec is exposed with\n" +
	"                      env: ' '\n" +
	"                      # Name is the tag or stream:tag that this dependency references\n" +
	"                      name: ' '\n" +
	"                  # DnsConfig for step's Pod.\n" +
	"                  dnsConfig:\n" +
	"                    # Nameservers is a list of IP addresses that will be used as DNS servers for the Pod\n" +
	"                    nameservers:\n" +
	"                        - \"\"\n" +
	"                    # Searches is a list of DNS search domains for host-name lookup\n" +
	"                    searches:\n" +
	"                        - \"\"\n" +
	"                  # Environment lists parameters that should be set by the test.\n" +
	"                  env:\n" +
	"                    - # Default if not set, optional, makes the parameter not required if set.\n" +
	"                      default: \"\"\n" +
	"                      # Documentation is a textual description of the parameter.\n" +
	"                      documentation: ' '\n" +
	"                      # Name of the environment variable.\n" +
	"                      name: ' '\n" +
	"                  # From is the container image that will be used for this step.\n" +
	"                  from: ' '\n" +
	"                  # FromImage is a literal ImageStreamTag reference to use for this step.\n" +
	"                  from_image:\n" +
	"                    # As is an optional string to use as the intermediate name for this reference.\n" +
	"                    as: ' '\n" +
	"                    name: ' '\n" +
	"                    namespace: ' '\n" +
	"                    tag: ' '\n" +
	"                  # GracePeriod is how long the we will wait after sending SIGINT to send\n" +
	"                  # SIGKILL when aborting a Step.\n" +
	"                  grace_period: 0s\n" +
	"                  # Leases lists resources that should be acquired for the test.\n" +
	"                  leases:\n" +
	"                    - # Env is the environment variable that will contain the resource name.\n" +
	"                      env: ' '\n" +
	"                      # ResourceType is the type of resource that will be leased.\n" +
	"                      resource_type: ' '\n" +
	"                  # Observers are the observers that should be running\n" +
	"                  observers:\n" +
	"                    - \"\"\n" +
	"                  # OptionalOnSuccess defines if this step should be skipped as long\n" +
	"                  # as all `pre` and `test` steps were successful and AllowSkipOnSuccess\n" +
	"                  # flag is set to true in MultiStageTestConfiguration. This option is\n" +
	"                  # applicable to `post` steps.\n" +
	"                  optional_on_success: false\n" +
	"                  # Resources defines the resource requirements for the step.\n" +
	"                  resources:\n" +
	"                    # Limits are resource limits applied to an individual step in the job.\n" +
	"                    # These are directly used in creating the Pods that execute the Job.\n" +
	"                    limits:\n" +
	"                        \"\": \"\"\n" +
	"                    # Requests are resource requests applied to an individual step in the job.\n" +
	"                    # These are directly used in creating the Pods that execute the Job.\n" +
	"                    requests:\n" +
	"                        \"\": \"\"\n" +
	"                  # RunAsScript defines if this step should be executed as a script mounted\n" +
	"                  # in the test container instead of being executed directly via bash\n" +
	"                  run_as_script: false\n" +
	"                  # Timeout is how long the we will wait before aborting a job with SIGINT.\n" +
	"                  timeout: 0s\n" +
	"            # Override job timeout\n" +
	"            timeout: 0s\n" +
	"        openshift_ansible:\n" +
	"            cluster_profile: ' '\n" +
	"        openshift_ansible_custom:\n" +
	"            cluster_profile: ' '\n" +
	"        openshift_ansible_src:\n" +
	"            cluster_profile: ' '\n" +
	"        openshift_installer:\n" +
	"            cluster_profile: ' '\n" +
	"            # If upgrade is true, RELEASE_IMAGE_INITIAL will be used as\n" +
	"            # the initial payload and the installer image from that\n" +
	"            # will be upgraded. The `run-upgrade-tests` function will be\n" +
	"            # available for the commands.\n" +
	"            upgrade: true\n" +
	"        openshift_installer_custom_test_image:\n" +
	"            cluster_profile: ' '\n" +
	"            # From defines the imagestreamtag that will be used to run the\n" +
	"            # provided test command. e.g. stable:console-test\n" +
	"            from: ' '\n" +
	"        openshift_installer_upi:\n" +
	"            cluster_profile: ' '\n" +
	"        openshift_installer_upi_src:\n" +
	"            cluster_profile: ' '\n" +
	"        # Optional indicates that the job's status context, that is generated from the corresponding test, should not be required for merge.\n" +
	"        optional: true\n" +
	"        # Postsubmit configures prowgen to generate the job as a postsubmit rather than a presubmit\n" +
	"        postsubmit: true\n" +
	"        # ReleaseController configures prowgen to create a periodic that\n" +
	"        # does not get run by prow and instead is run by release-controller.\n" +
	"        # The job must be configured as a verification or periodic job in a\n" +
	"        # release-controller config file when this field is set to `true`.\n" +
	"        release_controller: true\n" +
	"        # RunIfChanged is a regex that will result in the test only running if something that matches it was changed.\n" +
	"        run_if_changed: ' '\n" +
	"        # Secret is an optional secret object which\n" +
	"        # will be mounted inside the test container.\n" +
	"        # You cannot set the Secret and Secrets attributes\n" +
	"        # at the same time.\n" +
	"        secret:\n" +
	"            # Secret mount path. Defaults to /usr/test-secrets for first\n" +
	"            # secret. /usr/test-secrets-2 for second, and so on.\n" +
	"            mount_path: ' '\n" +
	"            # Secret name, used inside test containers\n" +
	"            name: ' '\n" +
	"        # Secrets is an optional array of secret objects\n" +
	"        # which will be mounted inside the test container.\n" +
	"        # You cannot set the Secret and Secrets attributes\n" +
	"        # at the same time.\n" +
	"        secrets:\n" +
	"            - # Secret mount path. Defaults to /usr/test-secrets for first\n" +
	"              # secret. /usr/test-secrets-2 for second, and so on.\n" +
	"              mount_path: ' '\n" +
	"              # Secret name, used inside test containers\n" +
	"              name: ' '\n" +
	"        # SkipIfOnlyChanged is a regex that will result in the test being skipped if all changed files match that regex.\n" +
	"        skip_if_only_changed: ' '\n" +
	"        steps:\n" +
	"            # AllowBestEffortPostSteps defines if any `post` steps can be ignored when\n" +
	"            # they fail. The given step must explicitly ask for being ignored by setting\n" +
	"            # the OptionalOnSuccess flag to true.\n" +
	"            allow_best_effort_post_steps: false\n" +
	"            # AllowSkipOnSuccess defines if any steps can be skipped when\n" +
	"            # all previous `pre` and `test` steps were successful. The given step must explicitly\n" +
	"            # ask for being skipped by setting the OptionalOnSuccess flag to true.\n" +
	"            allow_skip_on_success: false\n" +
	"            # ClusterProfile defines the profile/cloud provider for end-to-end test steps.\n" +
	"            cluster_profile: ' '\n" +
	"            # Dependencies holds override values for dependency parameters.\n" +
	"            dependencies:\n" +
	"                \"\": \"\"\n" +
	"            # DependencyOverrides allows a step to override a dependency with a fully-qualified pullspec. This will probably only ever\n" +
	"            # be used with rehearsals. Otherwise, the overrides should be passed in as parameters to ci-operator.\n" +
	"            dependency_overrides:\n" +
	"                \"\": \"\"\n" +
	"            # DnsConfig for step's Pod.\n" +
	"            dnsConfig:\n" +
	"                # Nameservers is a list of IP addresses that will be used as DNS servers for the Pod\n" +
	"                nameservers:\n" +
	"                    - \"\"\n" +
	"                # Searches is a list of DNS search domains for host-name lookup\n" +
	"                searches:\n" +
	"                    - \"\"\n" +
	"            # Environment has the values of parameters for the steps.\n" +
	"            env:\n" +
	"                \"\": \"\"\n" +
	"            # Leases lists resources that should be acquired for the test.\n" +
	"            leases:\n" +
	"                - # Env is the environment variable that will contain the resource name.\n" +
	"                  env: ' '\n" +
	"                  # ResourceType is the type of resource that will be leased.\n" +
	"                  resource_type: ' '\n" +
	"            # Observers are the observers that should be running\n" +
	"            observers:\n" +
	"                # Disable is a list of named observers that should be disabled\n" +
	"                disable:\n" +
	"                    - \"\"\n" +
	"                # Enable is a list of named observer that should be enabled\n" +
	"                enable:\n" +
	"                    - \"\"\n" +
	"            # Post is the array of test steps run after the tests finish and teardown/deprovision resources.\n" +
	"            # Post steps always run, even if previous steps fail. However, they have an option to skip\n" +
	"            # execution if previous Pre and Test steps passed.\n" +
	"            post:\n" +
	"                # LiteralTestStep is a full test step definition.\n" +
	"                - as: ' '\n" +
	"                  best_effort: false\n" +
	"                  # Chain is the name of a step chain reference.\n" +
	"                  chain: \"\"\n" +
	"                  # Cli is the (optional) name of the release from which the `oc` binary\n" +
	"                  # will be injected into this step.\n" +
	"                  cli: ' '\n" +
	"                  commands: ' '\n" +
	"                  credentials:\n" +
	"                    # LiteralTestStep is a full test step definition.\n" +
	"                    - mount_path: ' '\n" +
	"                      name: ' '\n" +
	"                      namespace: ' '\n" +
	"                  dependencies:\n" +
	"                    # LiteralTestStep is a full test step definition.\n" +
	"                    - env: ' '\n" +
	"                      name: ' '\n" +
	"                  dnsConfig:\n" +
	"                    # LiteralTestStep is a full test step definition.\n" +
	"                    nameservers:\n" +
	"                        # LiteralTestStep is a full test step definition.\n" +
	"                        - \"\"\n" +
	"                    searches:\n" +
	"                        # LiteralTestStep is a full test step definition.\n" +
	"                        - \"\"\n" +
	"                  env:\n" +
	"                    # LiteralTestStep is a full test step definition.\n" +
	"                    - default: \"\"\n" +
	"                      documentation: ' '\n" +
	"                      name: ' '\n" +
	"                  from: ' '\n" +
	"                  from_image:\n" +
	"                    # LiteralTestStep is a full test step definition.\n" +
	"                    as: ' '\n" +
	"                    name: ' '\n" +
	"                    namespace: ' '\n" +
	"                    tag: ' '\n" +
	"                  grace_period: 0s\n" +
	"                  leases:\n" +
	"                    # LiteralTestStep is a full test step definition.\n" +
	"                    - env: ' '\n" +
	"                      resource_type: ' '\n" +
	"                  observers:\n" +
	"                    # LiteralTestStep is a full test step definition.\n" +
	"                    - \"\"\n" +
	"                  optional_on_success: false\n" +
	"                  # Reference is the name of a step reference.\n" +
	"                  ref: \"\"\n" +
	"                  # Resources defines the resource requirements for the step.\n" +
	"                  resources:\n" +
	"                    # LiteralTestStep is a full test step definition.\n" +
	"                    limits:\n" +
	"                        # LiteralTestStep is a full test step definition.\n" +
	"                        \"\": \"\"\n" +
	"                    requests:\n" +
	"                        # LiteralTestStep is a full test step definition.\n" +
	"                        \"\": \"\"\n" +
	"                  run_as_script: false\n" +
	"                  timeout: 0s\n" +
	"            # Pre is the array of test steps run to set up the environment for the test.\n" +
	"            pre:\n" +
	"                # LiteralTestStep is a full test step definition.\n" +
	"                - as: ' '\n" +
	"                  best_effort: false\n" +
	"                  # Chain is the name of a step chain reference.\n" +
	"                  chain: \"\"\n" +
	"                  # Cli is the (optional) name of the release from which the `oc` binary\n" +
	"                  # will be injected into this step.\n" +
	"                  cli: ' '\n" +
	"                  commands: ' '\n" +
	"                  credentials:\n" +
	"                    # LiteralTestStep is a full test step definition.\n" +
	"                    - mount_path: ' '\n" +
	"                      name: ' '\n" +
	"                      namespace: ' '\n" +
	"                  dependencies:\n" +
	"                    # LiteralTestStep is a full test step definition.\n" +
	"                    - env: ' '\n" +
	"                      name: ' '\n" +
	"                  dnsConfig:\n" +
	"                    # LiteralTestStep is a full test step definition.\n" +
	"                    nameservers:\n" +
	"                        # LiteralTestStep is a full test step definition.\n" +
	"                        - \"\"\n" +
	"                    searches:\n" +
	"                        # LiteralTestStep is a full test step definition.\n" +
	"                        - \"\"\n" +
	"                  env:\n" +
	"                    # LiteralTestStep is a full test step definition.\n" +
	"                    - default: \"\"\n" +
	"                      documentation: ' '\n" +
	"                      name: ' '\n" +
	"                  from: ' '\n" +
	"                  from_image:\n" +
	"                    # LiteralTestStep is a full test step definition.\n" +
	"                    as: ' '\n" +
	"                    name: ' '\n" +
	"                    namespace: ' '\n" +
	"                    tag: ' '\n" +
	"                  grace_period: 0s\n" +
	"                  leases:\n" +
	"                    # LiteralTestStep is a full test step definition.\n" +
	"                    - env: ' '\n" +
	"                      resource_type: ' '\n" +
	"                  observers:\n" +
	"                    # LiteralTestStep is a full test step definition.\n" +
	"                    - \"\"\n" +
	"                  optional_on_success: false\n" +
	"                  # Reference is the name of a step reference.\n" +
	"                  ref: \"\"\n" +
	"                  # Resources defines the resource requirements for the step.\n" +
	"                  resources:\n" +
	"                    # LiteralTestStep is a full test step definition.\n" +
	"                    limits:\n" +
	"                        # LiteralTestStep is a full test step definition.\n" +
	"                        \"\": \"\"\n" +
	"                    requests:\n" +
	"                        # LiteralTestStep is a full test step definition.\n" +
	"                        \"\": \"\"\n" +
	"                  run_as_script: false\n" +
	"                  timeout: 0s\n" +
	"            # Test is the array of test steps that define the actual test.\n" +
	"            test:\n" +
	"                # LiteralTestStep is a full test step definition.\n" +
	"                - as: ' '\n" +
	"                  best_effort: false\n" +
	"                  # Chain is the name of a step chain reference.\n" +
	"                  chain: \"\"\n" +
	"                  # Cli is the (optional) name of the release from which the `oc` binary\n" +
	"                  # will be injected into this step.\n" +
	"                  cli: ' '\n" +
	"                  commands: ' '\n" +
	"                  credentials:\n" +
	"                    # LiteralTestStep is a full test step definition.\n" +
	"                    - mount_path: ' '\n" +
	"                      name: ' '\n" +
	"                      namespace: ' '\n" +
	"                  dependencies:\n" +
	"                    # LiteralTestStep is a full test step definition.\n" +
	"                    - env: ' '\n" +
	"                      name: ' '\n" +
	"                  dnsConfig:\n" +
	"                    # LiteralTestStep is a full test step definition.\n" +
	"                    nameservers:\n" +
	"                        # LiteralTestStep is a full test step definition.\n" +
	"                        - \"\"\n" +
	"                    searches:\n" +
	"                        # LiteralTestStep is a full test step definition.\n" +
	"                        - \"\"\n" +
	"                  env:\n" +
	"                    # LiteralTestStep is a full test step definition.\n" +
	"                    - default: \"\"\n" +
	"                      documentation: ' '\n" +
	"                      name: ' '\n" +
	"                  from: ' '\n" +
	"                  from_image:\n" +
	"                    # LiteralTestStep is a full test step definition.\n" +
	"                    as: ' '\n" +
	"                    name: ' '\n" +
	"                    namespace: ' '\n" +
	"                    tag: ' '\n" +
	"                  grace_period: 0s\n" +
	"                  leases:\n" +
	"                    # LiteralTestStep is a full test step definition.\n" +
	"                    - env: ' '\n" +
	"                      resource_type: ' '\n" +
	"                  observers:\n" +
	"                    # LiteralTestStep is a full test step definition.\n" +
	"                    - \"\"\n" +
	"                  optional_on_success: false\n" +
	"                  # Reference is the name of a step reference.\n" +
	"                  ref: \"\"\n" +
	"                  # Resources defines the resource requirements for the step.\n" +
	"                  resources:\n" +
	"                    # LiteralTestStep is a full test step definition.\n" +
	"                    limits:\n" +
	"                        # LiteralTestStep is a full test step definition.\n" +
	"                        \"\": \"\"\n" +
	"                    requests:\n" +
	"                        # LiteralTestStep is a full test step definition.\n" +
	"                        \"\": \"\"\n" +
	"                  run_as_script: false\n" +
	"                  timeout: 0s\n" +
	"            # Workflow is the name of the workflow to be used for this configuration. For fields defined in both\n" +
	"            # the config and the workflow, the fields from the config will override what is set in Workflow.\n" +
	"            workflow: \"\"\n" +
	"        # Timeout overrides maximum prowjob duration\n" +
	"        timeout: 0s\n" +
	"# Releases maps semantic release payload identifiers\n" +
	"# to the names that they will be exposed under. For\n" +
	"# instance, an 'initial' name will be exposed as\n" +
	"# $RELEASE_IMAGE_INITIAL. The 'latest' key is special\n" +
	"# and cannot co-exist with 'tag_specification', as\n" +
	"# they result in the same output.\n" +
	"releases:\n" +
	"    \"\":\n" +
	"        # Candidate describes a candidate release payload\n" +
	"        candidate:\n" +
	"            # Architecture is the architecture for the product.\n" +
	"            # Defaults to amd64.\n" +
	"            architecture: ' '\n" +
	"            # Product is the name of the product being released\n" +
	"            product: ' '\n" +
	"            # ReleaseStream is the stream from which we pick the latest candidate\n" +
	"            stream: ' '\n" +
	"            # Version is the minor version to search for\n" +
	"            version: ' '\n" +
	"        # Integration describes an integration stream which we can create a payload out of\n" +
	"        integration:\n" +
	"            # IncludeBuiltImages determines if the release we assemble will include\n" +
	"            # images built during the test itself.\n" +
	"            include_built_images: true\n" +
	"            # Name is the name of the ImageStream\n" +
	"            name: ' '\n" +
	"            # Namespace is the namespace in which the integration stream lives.\n" +
	"            namespace: ' '\n" +
	"        # Prerelease describes a yet-to-be released payload\n" +
	"        prerelease:\n" +
	"            # Architecture is the architecture for the product.\n" +
	"            # Defaults to amd64.\n" +
	"            architecture: ' '\n" +
	"            # Product is the name of the product being released\n" +
	"            product: ' '\n" +
	"            # VersionBounds describe the allowable version bounds to search in\n" +
	"            version_bounds:\n" +
	"                lower: ' '\n" +
	"                upper: ' '\n" +
	"        # Release describes a released payload\n" +
	"        release:\n" +
	"            # Architecture is the architecture for the release.\n" +
	"            # Defaults to amd64.\n" +
	"            architecture: ' '\n" +
	"            # Channel is the release channel to search in\n" +
	"            channel: ' '\n" +
	"            # Version is the minor version to search for\n" +
	"            version: ' '\n" +
	"# Resources is a set of resource requests or limits over the\n" +
	"# input types. The special name '*' may be used to set default\n" +
	"# requests and limits.\n" +
	"resources:\n" +
	"    \"\":\n" +
	"        limits:\n" +
	"            \"\": \"\"\n" +
	"        requests:\n" +
	"            \"\": \"\"\n" +
	"# RpmBuildCommands will create an \"rpms\" image from \"bin\" (or \"src\", if no\n" +
	"# binary build commands were specified) that contains the output of this\n" +
	"# command. The created RPMs will then be served via HTTP to the \"base\" image\n" +
	"# via an injected rpm.repo in the standard location at /etc/yum.repos.d.\n" +
	"rpm_build_commands: ' '\n" +
	"# RpmBuildLocation is where RPms are deposited after being built. If\n" +
	"# unset, this will default under the repository root to\n" +
	"# _output/local/releases/rpms/.\n" +
	"rpm_build_location: ' '\n" +
	"# ReleaseTagConfiguration determines how the\n" +
	"# full release is assembled.\n" +
	"tag_specification:\n" +
	"    # IncludeBuiltImages determines if the release we assemble will include\n" +
	"    # images built during the test itself.\n" +
	"    include_built_images: true\n" +
	"    # Name is the image stream name to use that contains all\n" +
	"    # component tags.\n" +
	"    name: ' '\n" +
	"    # Namespace identifies the namespace from which\n" +
	"    # all release artifacts not built in the current\n" +
	"    # job are tagged from.\n" +
	"    namespace: ' '\n" +
	"# TestBinaryBuildCommands will create a \"test-bin\" image based on \"src\" that\n" +
	"# contains the output of this command. This allows reuse of binary artifacts\n" +
	"# across other steps. If empty, no \"test-bin\" image will be created.\n" +
	"test_binary_build_commands: ' '\n" +
	"# Tests describes the tests to run inside of built images.\n" +
	"# The images launched as pods but have no explicit access to\n" +
	"# the cluster they are running on.\n" +
	"tests:\n" +
	"    - # As is the name of the test.\n" +
	"      as: ' '\n" +
	"      # Cluster specifies the name of the cluster where the test runs.\n" +
	"      cluster: ' '\n" +
	"      # ClusterClaim claims an OpenShift cluster and exposes environment variable ${KUBECONFIG} to the test container\n" +
	"      cluster_claim:\n" +
	"        # Architecture is the architecture for the product.\n" +
	"        # Defaults to amd64.\n" +
	"        architecture: ' '\n" +
	"        # As is the name to use when importing the cluster claim release payload.\n" +
	"        # If unset, claim release will be imported as `latest`.\n" +
	"        as: ' '\n" +
	"        # Cloud is the cloud where the product is installed, e.g., aws.\n" +
	"        cloud: ' '\n" +
	"        # Labels is the labels to select the cluster pools\n" +
	"        labels:\n" +
	"            \"\": \"\"\n" +
	"        # Owner is the owner of cloud account used to install the product, e.g., dpp.\n" +
	"        owner: ' '\n" +
	"        # Product is the name of the product being released.\n" +
	"        # Defaults to ocp.\n" +
	"        product: ' '\n" +
	"        # Timeout is how long ci-operator will wait for the cluster to be ready.\n" +
	"        # Defaults to 1h.\n" +
	"        timeout: 0s\n" +
	"        # Version is the version of the product\n" +
	"        version: ' '\n" +
	"      # Commands are the shell commands to run in\n" +
	"      # the repository root to execute tests.\n" +
	"      commands: ' '\n" +
	"      # Only one of the following can be not-null.\n" +
	"      container:\n" +
	"        # If the step should clone the source code prior to running the command.\n" +
	"        # Defaults to `true` for `base_images`, `false` otherwise.\n" +
	"        clone: false\n" +
	"        # From is the image stream tag in the pipeline to run this\n" +
	"        # command in.\n" +
	"        from: ' '\n" +
	"        # MemoryBackedVolume mounts a volume of the specified size into\n" +
	"        # the container at /tmp/volume.\n" +
	"        memory_backed_volume:\n" +
	"            # Size is the requested size of the volume as a Kubernetes\n" +
	"            # quantity, i.e. \"1Gi\" or \"500M\"\n" +
	"            size: ' '\n" +
	"      # Cron is how often the test is expected to run outside\n" +
	"      # of pull request workflows. Setting this field will\n" +
	"      # create a periodic job instead of a presubmit\n" +
	"      cron: \"\"\n" +
	"      # Interval is how frequently the test should be run based\n" +
	"      # on the last time the test ran. Setting this field will\n" +
	"      # create a periodic job instead of a presubmit\n" +
	"      interval: \"\"\n" +
	"      literal_steps:\n" +
	"        # AllowBestEffortPostSteps defines if any `post` steps can be ignored when\n" +
	"        # they fail. The given step must explicitly ask for being ignored by setting\n" +
	"        # the OptionalOnSuccess flag to true.\n" +
	"        allow_best_effort_post_steps: false\n" +
	"        # AllowSkipOnSuccess defines if any steps can be skipped when\n" +
	"        # all previous `pre` and `test` steps were successful. The given step must explicitly\n" +
	"        # ask for being skipped by setting the OptionalOnSuccess flag to true.\n" +
	"        allow_skip_on_success: false\n" +
	"        # ClusterProfile defines the profile/cloud provider for end-to-end test steps.\n" +
	"        cluster_profile: ' '\n" +
	"        # Dependencies holds override values for dependency parameters.\n" +
	"        dependencies:\n" +
	"            \"\": \"\"\n" +
	"        # DependencyOverrides allows a step to override a dependency with a fully-qualified pullspec. This will probably only ever\n" +
	"        # be used with rehearsals. Otherwise, the overrides should be passed in as parameters to ci-operator.\n" +
	"        dependency_overrides:\n" +
	"            \"\": \"\"\n" +
	"        # DnsConfig for step's Pod.\n" +
	"        dnsConfig:\n" +
	"            # Nameservers is a list of IP addresses that will be used as DNS servers for the Pod\n" +
	"            nameservers:\n" +
	"                - \"\"\n" +
	"            # Searches is a list of DNS search domains for host-name lookup\n" +
	"            searches:\n" +
	"                - \"\"\n" +
	"        # Environment has the values of parameters for the steps.\n" +
	"        env:\n" +
	"            \"\": \"\"\n" +
	"        # Leases lists resources that should be acquired for the test.\n" +
	"        leases:\n" +
	"            - # Env is the environment variable that will contain the resource name.\n" +
	"              env: ' '\n" +
	"              # ResourceType is the type of resource that will be leased.\n" +
	"              resource_type: ' '\n" +
	"        # Observers are the observers that need to be run\n" +
	"        observers:\n" +
	"            - # Commands is the command(s) that will be run inside the image.\n" +
	"              commands: ' '\n" +
	"              # From is the container image that will be used for this observer.\n" +
	"              from: ' '\n" +
	"              # FromImage is a literal ImageStreamTag reference to use for this observer.\n" +
	"              from_image:\n" +
	"                # As is an optional string to use as the intermediate name for this reference.\n" +
	"                as: ' '\n" +
	"                name: ' '\n" +
	"                namespace: ' '\n" +
	"                tag: ' '\n" +
	"              # Name is the name of this observer\n" +
	"              name: ' '\n" +
	"              # Resources defines the resource requirements for the step.\n" +
	"              resources:\n" +
	"                # Limits are resource limits applied to an individual step in the job.\n" +
	"                # These are directly used in creating the Pods that execute the Job.\n" +
	"                limits:\n" +
	"                    \"\": \"\"\n" +
	"                # Requests are resource requests applied to an individual step in the job.\n" +
	"                # These are directly used in creating the Pods that execute the Job.\n" +
	"                requests:\n" +
	"                    \"\": \"\"\n" +
	"        # Post is the array of test steps run after the tests finish and teardown/deprovision resources.\n" +
	"        # Post steps always run, even if previous steps fail.\n" +
	"        post:\n" +
	"            - # As is the name of the LiteralTestStep.\n" +
	"              as: ' '\n" +
	"              # BestEffort defines if this step should cause the job to fail when the\n" +
	"              # step fails. This only applies when AllowBestEffortPostSteps flag is set\n" +
	"              # to true in MultiStageTestConfiguration. This option is applicable to\n" +
	"              # `post` steps.\n" +
	"              best_effort: false\n" +
	"              # Cli is the (optional) name of the release from which the `oc` binary\n" +
	"              # will be injected into this step.\n" +
	"              cli: ' '\n" +
	"              # Commands is the command(s) that will be run inside the image.\n" +
	"              commands: ' '\n" +
	"              # Credentials defines the credentials we'll mount into this step.\n" +
	"              credentials:\n" +
	"                - # MountPath is where the secret should be mounted.\n" +
	"                  mount_path: ' '\n" +
	"                  # Names is which source secret to mount.\n" +
	"                  name: ' '\n" +
	"                  # Namespace is where the source secret exists.\n" +
	"                  namespace: ' '\n" +
	"              # Dependencies lists images which must be available before the test runs\n" +
	"              # and the environment variables which are used to expose their pull specs.\n" +
	"              dependencies:\n" +
	"                - # Env is the environment variable that the image's pull spec is exposed with\n" +
	"                  env: ' '\n" +
	"                  # Name is the tag or stream:tag that this dependency references\n" +
	"                  name: ' '\n" +
	"              # DnsConfig for step's Pod.\n" +
	"              dnsConfig:\n" +
	"                # Nameservers is a list of IP addresses that will be used as DNS servers for the Pod\n" +
	"                nameservers:\n" +
	"                    - \"\"\n" +
	"                # Searches is a list of DNS search domains for host-name lookup\n" +
	"                searches:\n" +
	"                    - \"\"\n" +
	"              # Environment lists parameters that should be set by the test.\n" +
	"              env:\n" +
	"                - # Default if not set, optional, makes the parameter not required if set.\n" +
	"                  default: \"\"\n" +
	"                  # Documentation is a textual description of the parameter.\n" +
	"                  documentation: ' '\n" +
	"                  # Name of the environment variable.\n" +
	"                  name: ' '\n" +
	"              # From is the container image that will be used for this step.\n" +
	"              from: ' '\n" +
	"              # FromImage is a literal ImageStreamTag reference to use for this step.\n" +
	"              from_image:\n" +
	"                # As is an optional string to use as the intermediate name for this reference.\n" +
	"                as: ' '\n" +
	"                name: ' '\n" +
	"                namespace: ' '\n" +
	"                tag: ' '\n" +
	"              # GracePeriod is how long the we will wait after sending SIGINT to send\n" +
	"              # SIGKILL when aborting a Step.\n" +
	"              grace_period: 0s\n" +
	"              # Leases lists resources that should be acquired for the test.\n" +
	"              leases:\n" +
	"                - # Env is the environment variable that will contain the resource name.\n" +
	"                  env: ' '\n" +
	"                  # ResourceType is the type of resource that will be leased.\n" +
	"                  resource_type: ' '\n" +
	"              # Observers are the observers that should be running\n" +
	"              observers:\n" +
	"                - \"\"\n" +
	"              # OptionalOnSuccess defines if this step should be skipped as long\n" +
	"              # as all `pre` and `test` steps were successful and AllowSkipOnSuccess\n" +
	"              # flag is set to true in MultiStageTestConfiguration. This option is\n" +
	"              # applicable to `post` steps.\n" +
	"              optional_on_success: false\n" +
	"              # Resources defines the resource requirements for the step.\n" +
	"              resources:\n" +
	"                # Limits are resource limits applied to an individual step in the job.\n" +
	"                # These are directly used in creating the Pods that execute the Job.\n" +
	"                limits:\n" +
	"                    \"\": \"\"\n" +
	"                # Requests are resource requests applied to an individual step in the job.\n" +
	"                # These are directly used in creating the Pods that execute the Job.\n" +
	"                requests:\n" +
	"                    \"\": \"\"\n" +
	"              # RunAsScript defines if this step should be executed as a script mounted\n" +
	"              # in the test container instead of being executed directly via bash\n" +
	"              run_as_script: false\n" +
	"              # Timeout is how long the we will wait before aborting a job with SIGINT.\n" +
	"              timeout: 0s\n" +
	"        # Pre is the array of test steps run to set up the environment for the test.\n" +
	"        pre:\n" +
	"            - # As is the name of the LiteralTestStep.\n" +
	"              as: ' '\n" +
	"              # BestEffort defines if this step should cause the job to fail when the\n" +
	"              # step fails. This only applies when AllowBestEffortPostSteps flag is set\n" +
	"              # to true in MultiStageTestConfiguration. This option is applicable to\n" +
	"              # `post` steps.\n" +
	"              best_effort: false\n" +
	"              # Cli is the (optional) name of the release from which the `oc` binary\n" +
	"              # will be injected into this step.\n" +
	"              cli: ' '\n" +
	"              # Commands is the command(s) that will be run inside the image.\n" +
	"              commands: ' '\n" +
	"              # Credentials defines the credentials we'll mount into this step.\n" +
	"              credentials:\n" +
	"                - # MountPath is where the secret should be mounted.\n" +
	"                  mount_path: ' '\n" +
	"                  # Names is which source secret to mount.\n" +
	"                  name: ' '\n" +
	"                  # Namespace is where the source secret exists.\n" +
	"                  namespace: ' '\n" +
	"              # Dependencies lists images which must be available before the test runs\n" +
	"              # and the environment variables which are used to expose their pull specs.\n" +
	"              dependencies:\n" +
	"                - # Env is the environment variable that the image's pull spec is exposed with\n" +
	"                  env: ' '\n" +
	"                  # Name is the tag or stream:tag that this dependency references\n" +
	"                  name: ' '\n" +
	"              # DnsConfig for step's Pod.\n" +
	"              dnsConfig:\n" +
	"                # Nameservers is a list of IP addresses that will be used as DNS servers for the Pod\n" +
	"                nameservers:\n" +
	"                    - \"\"\n" +
	"                # Searches is a list of DNS search domains for host-name lookup\n" +
	"                searches:\n" +
	"                    - \"\"\n" +
	"              # Environment lists parameters that should be set by the test.\n" +
	"              env:\n" +
	"                - # Default if not set, optional, makes the parameter not required if set.\n" +
	"                  default: \"\"\n" +
	"                  # Documentation is a textual description of the parameter.\n" +
	"                  documentation: ' '\n" +
	"                  # Name of the environment variable.\n" +
	"                  name: ' '\n" +
	"              # From is the container image that will be used for this step.\n" +
	"              from: ' '\n" +
	"              # FromImage is a literal ImageStreamTag reference to use for this step.\n" +
	"              from_image:\n" +
	"                # As is an optional string to use as the intermediate name for this reference.\n" +
	"                as: ' '\n" +
	"                name: ' '\n" +
	"                namespace: ' '\n" +
	"                tag: ' '\n" +
	"              # GracePeriod is how long the we will wait after sending SIGINT to send\n" +
	"              # SIGKILL when aborting a Step.\n" +
	"              grace_period: 0s\n" +
	"              # Leases lists resources that should be acquired for the test.\n" +
	"              leases:\n" +
	"                - # Env is the environment variable that will contain the resource name.\n" +
	"                  env: ' '\n" +
	"                  # ResourceType is the type of resource that will be leased.\n" +
	"                  resource_type: ' '\n" +
	"              # Observers are the observers that should be running\n" +
	"              observers:\n" +
	"                - \"\"\n" +
	"              # OptionalOnSuccess defines if this step should be skipped as long\n" +
	"              # as all `pre` and `test` steps were successful and AllowSkipOnSuccess\n" +
	"              # flag is set to true in MultiStageTestConfiguration. This option is\n" +
	"              # applicable to `post` steps.\n" +
	"              optional_on_success: false\n" +
	"              # Resources defines the resource requirements for the step.\n" +
	"              resources:\n" +
	"                # Limits are resource limits applied to an individual step in the job.\n" +
	"                # These are directly used in creating the Pods that execute the Job.\n" +
	"                limits:\n" +
	"                    \"\": \"\"\n" +
	"                # Requests are resource requests applied to an individual step in the job.\n" +
	"                # These are directly used in creating the Pods that execute the Job.\n" +
	"                requests:\n" +
	"                    \"\": \"\"\n" +
	"              # RunAsScript defines if this step should be executed as a script mounted\n" +
	"              # in the test container instead of being executed directly via bash\n" +
	"              run_as_script: false\n" +
	"              # Timeout is how long the we will wait before aborting a job with SIGINT.\n" +
	"              timeout: 0s\n" +
	"        # Test is the array of test steps that define the actual test.\n" +
	"        test:\n" +
	"            - # As is the name of the LiteralTestStep.\n" +
	"              as: ' '\n" +
	"              # BestEffort defines if this step should cause the job to fail when the\n" +
	"              # step fails. This only applies when AllowBestEffortPostSteps flag is set\n" +
	"              # to true in MultiStageTestConfiguration. This option is applicable to\n" +
	"              # `post` steps.\n" +
	"              best_effort: false\n" +
	"              # Cli is the (optional) name of the release from which the `oc` binary\n" +
	"              # will be injected into this step.\n" +
	"              cli: ' '\n" +
	"              # Commands is the command(s) that will be run inside the image.\n" +
	"              commands: ' '\n" +
	"              # Credentials defines the credentials we'll mount into this step.\n" +
	"              credentials:\n" +
	"                - # MountPath is where the secret should be mounted.\n" +
	"                  mount_path: ' '\n" +
	"                  # Names is which source secret to mount.\n" +
	"                  name: ' '\n" +
	"                  # Namespace is where the source secret exists.\n" +
	"                  namespace: ' '\n" +
	"              # Dependencies lists images which must be available before the test runs\n" +
	"              # and the environment variables which are used to expose their pull specs.\n" +
	"              dependencies:\n" +
	"                - # Env is the environment variable that the image's pull spec is exposed with\n" +
	"                  env: ' '\n" +
	"                  # Name is the tag or stream:tag that this dependency references\n" +
	"                  name: ' '\n" +
	"              # DnsConfig for step's Pod.\n" +
	"              dnsConfig:\n" +
	"                # Nameservers is a list of IP addresses that will be used as DNS servers for the Pod\n" +
	"                nameservers:\n" +
	"                    - \"\"\n" +
	"                # Searches is a list of DNS search domains for host-name lookup\n" +
	"                searches:\n" +
	"                    - \"\"\n" +
	"              # Environment lists parameters that should be set by the test.\n" +
	"              env:\n" +
	"                - # Default if not set, optional, makes the parameter not required if set.\n" +
	"                  default: \"\"\n" +
	"                  # Documentation is a textual description of the parameter.\n" +
	"                  documentation: ' '\n" +
	"                  # Name of the environment variable.\n" +
	"                  name: ' '\n" +
	"              # From is the container image that will be used for this step.\n" +
	"              from: ' '\n" +
	"              # FromImage is a literal ImageStreamTag reference to use for this step.\n" +
	"              from_image:\n" +
	"                # As is an optional string to use as the intermediate name for this reference.\n" +
	"                as: ' '\n" +
	"                name: ' '\n" +
	"                namespace: ' '\n" +
	"                tag: ' '\n" +
	"              # GracePeriod is how long the we will wait after sending SIGINT to send\n" +
	"              # SIGKILL when aborting a Step.\n" +
	"              grace_period: 0s\n" +
	"              # Leases lists resources that should be acquired for the test.\n" +
	"              leases:\n" +
	"                - # Env is the environment variable that will contain the resource name.\n" +
	"                  env: ' '\n" +
	"                  # ResourceType is the type of resource that will be leased.\n" +
	"                  resource_type: ' '\n" +
	"              # Observers are the observers that should be running\n" +
	"              observers:\n" +
	"                - \"\"\n" +
	"              # OptionalOnSuccess defines if this step should be skipped as long\n" +
	"              # as all `pre` and `test` steps were successful and AllowSkipOnSuccess\n" +
	"              # flag is set to true in MultiStageTestConfiguration. This option is\n" +
	"              # applicable to `post` steps.\n" +
	"              optional_on_success: false\n" +
	"              # Resources defines the resource requirements for the step.\n" +
	"              resources:\n" +
	"                # Limits are resource limits applied to an individual step in the job.\n" +
	"                # These are directly used in creating the Pods that execute the Job.\n" +
	"                limits:\n" +
	"                    \"\": \"\"\n" +
	"                # Requests are resource requests applied to an individual step in the job.\n" +
	"                # These are directly used in creating the Pods that execute the Job.\n" +
	"                requests:\n" +
	"                    \"\": \"\"\n" +
	"              # RunAsScript defines if this step should be executed as a script mounted\n" +
	"              # in the test container instead of being executed directly via bash\n" +
	"              run_as_script: false\n" +
	"              # Timeout is how long the we will wait before aborting a job with SIGINT.\n" +
	"              timeout: 0s\n" +
	"        # Override job timeout\n" +
	"        timeout: 0s\n" +
	"      openshift_ansible:\n" +
	"        cluster_profile: ' '\n" +
	"      openshift_ansible_custom:\n" +
	"        cluster_profile: ' '\n" +
	"      openshift_ansible_src:\n" +
	"        cluster_profile: ' '\n" +
	"      openshift_installer:\n" +
	"        cluster_profile: ' '\n" +
	"        # If upgrade is true, RELEASE_IMAGE_INITIAL will be used as\n" +
	"        # the initial payload and the installer image from that\n" +
	"        # will be upgraded. The `run-upgrade-tests` function will be\n" +
	"        # available for the commands.\n" +
	"        upgrade: true\n" +
	"      openshift_installer_custom_test_image:\n" +
	"        cluster_profile: ' '\n" +
	"        # From defines the imagestreamtag that will be used to run the\n" +
	"        # provided test command. e.g. stable:console-test\n" +
	"        from: ' '\n" +
	"      openshift_installer_upi:\n" +
	"        cluster_profile: ' '\n" +
	"      openshift_installer_upi_src:\n" +
	"        cluster_profile: ' '\n" +
	"      # Optional indicates that the job's status context, that is generated from the corresponding test, should not be required for merge.\n" +
	"      optional: true\n" +
	"      # Postsubmit configures prowgen to generate the job as a postsubmit rather than a presubmit\n" +
	"      postsubmit: true\n" +
	"      # ReleaseController configures prowgen to create a periodic that\n" +
	"      # does not get run by prow and instead is run by release-controller.\n" +
	"      # The job must be configured as a verification or periodic job in a\n" +
	"      # release-controller config file when this field is set to `true`.\n" +
	"      release_controller: true\n" +
	"      # RunIfChanged is a regex that will result in the test only running if something that matches it was changed.\n" +
	"      run_if_changed: ' '\n" +
	"      # Secret is an optional secret object which\n" +
	"      # will be mounted inside the test container.\n" +
	"      # You cannot set the Secret and Secrets attributes\n" +
	"      # at the same time.\n" +
	"      secret:\n" +
	"        # Secret mount path. Defaults to /usr/test-secrets for first\n" +
	"        # secret. /usr/test-secrets-2 for second, and so on.\n" +
	"        mount_path: ' '\n" +
	"        # Secret name, used inside test containers\n" +
	"        name: ' '\n" +
	"      # Secrets is an optional array of secret objects\n" +
	"      # which will be mounted inside the test container.\n" +
	"      # You cannot set the Secret and Secrets attributes\n" +
	"      # at the same time.\n" +
	"      secrets:\n" +
	"        - # Secret mount path. Defaults to /usr/test-secrets for first\n" +
	"          # secret. /usr/test-secrets-2 for second, and so on.\n" +
	"          mount_path: ' '\n" +
	"          # Secret name, used inside test containers\n" +
	"          name: ' '\n" +
	"      # SkipIfOnlyChanged is a regex that will result in the test being skipped if all changed files match that regex.\n" +
	"      skip_if_only_changed: ' '\n" +
	"      steps:\n" +
	"        # AllowBestEffortPostSteps defines if any `post` steps can be ignored when\n" +
	"        # they fail. The given step must explicitly ask for being ignored by setting\n" +
	"        # the OptionalOnSuccess flag to true.\n" +
	"        allow_best_effort_post_steps: false\n" +
	"        # AllowSkipOnSuccess defines if any steps can be skipped when\n" +
	"        # all previous `pre` and `test` steps were successful. The given step must explicitly\n" +
	"        # ask for being skipped by setting the OptionalOnSuccess flag to true.\n" +
	"        allow_skip_on_success: false\n" +
	"        # ClusterProfile defines the profile/cloud provider for end-to-end test steps.\n" +
	"        cluster_profile: ' '\n" +
	"        # Dependencies holds override values for dependency parameters.\n" +
	"        dependencies:\n" +
	"            \"\": \"\"\n" +
	"        # DependencyOverrides allows a step to override a dependency with a fully-qualified pullspec. This will probably only ever\n" +
	"        # be used with rehearsals. Otherwise, the overrides should be passed in as parameters to ci-operator.\n" +
	"        dependency_overrides:\n" +
	"            \"\": \"\"\n" +
	"        # DnsConfig for step's Pod.\n" +
	"        dnsConfig:\n" +
	"            # Nameservers is a list of IP addresses that will be used as DNS servers for the Pod\n" +
	"            nameservers:\n" +
	"                - \"\"\n" +
	"            # Searches is a list of DNS search domains for host-name lookup\n" +
	"            searches:\n" +
	"                - \"\"\n" +
	"        # Environment has the values of parameters for the steps.\n" +
	"        env:\n" +
	"            \"\": \"\"\n" +
	"        # Leases lists resources that should be acquired for the test.\n" +
	"        leases:\n" +
	"            - # Env is the environment variable that will contain the resource name.\n" +
	"              env: ' '\n" +
	"              # ResourceType is the type of resource that will be leased.\n" +
	"              resource_type: ' '\n" +
	"        # Observers are the observers that should be running\n" +
	"        observers:\n" +
	"            # Disable is a list of named observers that should be disabled\n" +
	"            disable:\n" +
	"                - \"\"\n" +
	"            # Enable is a list of named observer that should be enabled\n" +
	"            enable:\n" +
	"                - \"\"\n" +
	"        # Post is the array of test steps run after the tests finish and teardown/deprovision resources.\n" +
	"        # Post steps always run, even if previous steps fail. However, they have an option to skip\n" +
	"        # execution if previous Pre and Test steps passed.\n" +
	"        post:\n" +
	"            # LiteralTestStep is a full test step definition.\n" +
	"            - as: ' '\n" +
	"              best_effort: false\n" +
	"              # Chain is the name of a step chain reference.\n" +
	"              chain: \"\"\n" +
	"              # Cli is the (optional) name of the release from which the `oc` binary\n" +
	"              # will be injected into this step.\n" +
	"              cli: ' '\n" +
	"              commands: ' '\n" +
	"              credentials:\n" +
	"                # LiteralTestStep is a full test step definition.\n" +
	"                - mount_path: ' '\n" +
	"                  name: ' '\n" +
	"                  namespace: ' '\n" +
	"              dependencies:\n" +
	"                # LiteralTestStep is a full test step definition.\n" +
	"                - env: ' '\n" +
	"                  name: ' '\n" +
	"              dnsConfig:\n" +
	"                # LiteralTestStep is a full test step definition.\n" +
	"                nameservers:\n" +
	"                    # LiteralTestStep is a full test step definition.\n" +
	"                    - \"\"\n" +
	"                searches:\n" +
	"                    # LiteralTestStep is a full test step definition.\n" +
	"                    - \"\"\n" +
	"              env:\n" +
	"                # LiteralTestStep is a full test step definition.\n" +
	"                - default: \"\"\n" +
	"                  documentation: ' '\n" +
	"                  name: ' '\n" +
	"              from: ' '\n" +
	"              from_image:\n" +
	"                # LiteralTestStep is a full test step definition.\n" +
	"                as: ' '\n" +
	"                name: ' '\n" +
	"                namespace: ' '\n" +
	"                tag: ' '\n" +
	"              grace_period: 0s\n" +
	"              leases:\n" +
	"                # LiteralTestStep is a full test step definition.\n" +
	"                - env: ' '\n" +
	"                  resource_type: ' '\n" +
	"              observers:\n" +
	"                # LiteralTestStep is a full test step definition.\n" +
	"                - \"\"\n" +
	"              optional_on_success: false\n" +
	"              # Reference is the name of a step reference.\n" +
	"              ref: \"\"\n" +
	"              # Resources defines the resource requirements for the step.\n" +
	"              resources:\n" +
	"                # LiteralTestStep is a full test step definition.\n" +
	"                limits:\n" +
	"                    # LiteralTestStep is a full test step definition.\n" +
	"                    \"\": \"\"\n" +
	"                requests:\n" +
	"                    # LiteralTestStep is a full test step definition.\n" +
	"                    \"\": \"\"\n" +
	"              run_as_script: false\n" +
	"              timeout: 0s\n" +
	"        # Pre is the array of test steps run to set up the environment for the test.\n" +
	"        pre:\n" +
	"            # LiteralTestStep is a full test step definition.\n" +
	"            - as: ' '\n" +
	"              best_effort: false\n" +
	"              # Chain is the name of a step chain reference.\n" +
	"              chain: \"\"\n" +
	"              # Cli is the (optional) name of the release from which the `oc` binary\n" +
	"              # will be injected into this step.\n" +
	"              cli: ' '\n" +
	"              commands: ' '\n" +
	"              credentials:\n" +
	"                # LiteralTestStep is a full test step definition.\n" +
	"                - mount_path: ' '\n" +
	"                  name: ' '\n" +
	"                  namespace: ' '\n" +
	"              dependencies:\n" +
	"                # LiteralTestStep is a full test step definition.\n" +
	"                - env: ' '\n" +
	"                  name: ' '\n" +
	"              dnsConfig:\n" +
	"                # LiteralTestStep is a full test step definition.\n" +
	"                nameservers:\n" +
	"                    # LiteralTestStep is a full test step definition.\n" +
	"                    - \"\"\n" +
	"                searches:\n" +
	"                    # LiteralTestStep is a full test step definition.\n" +
	"                    - \"\"\n" +
	"              env:\n" +
	"                # LiteralTestStep is a full test step definition.\n" +
	"                - default: \"\"\n" +
	"                  documentation: ' '\n" +
	"                  name: ' '\n" +
	"              from: ' '\n" +
	"              from_image:\n" +
	"                # LiteralTestStep is a full test step definition.\n" +
	"                as: ' '\n" +
	"                name: ' '\n" +
	"                namespace: ' '\n" +
	"                tag: ' '\n" +
	"              grace_period: 0s\n" +
	"              leases:\n" +
	"                # LiteralTestStep is a full test step definition.\n" +
	"                - env: ' '\n" +
	"                  resource_type: ' '\n" +
	"              observers:\n" +
	"                # LiteralTestStep is a full test step definition.\n" +
	"                - \"\"\n" +
	"              optional_on_success: false\n" +
	"              # Reference is the name of a step reference.\n" +
	"              ref: \"\"\n" +
	"              # Resources defines the resource requirements for the step.\n" +
	"              resources:\n" +
	"                # LiteralTestStep is a full test step definition.\n" +
	"                limits:\n" +
	"                    # LiteralTestStep is a full test step definition.\n" +
	"                    \"\": \"\"\n" +
	"                requests:\n" +
	"                    # LiteralTestStep is a full test step definition.\n" +
	"                    \"\": \"\"\n" +
	"              run_as_script: false\n" +
	"              timeout: 0s\n" +
	"        # Test is the array of test steps that define the actual test.\n" +
	"        test:\n" +
	"            # LiteralTestStep is a full test step definition.\n" +
	"            - as: ' '\n" +
	"              best_effort: false\n" +
	"              # Chain is the name of a step chain reference.\n" +
	"              chain: \"\"\n" +
	"              # Cli is the (optional) name of the release from which the `oc` binary\n" +
	"              # will be injected into this step.\n" +
	"              cli: ' '\n" +
	"              commands: ' '\n" +
	"              credentials:\n" +
	"                # LiteralTestStep is a full test step definition.\n" +
	"                - mount_path: ' '\n" +
	"                  name: ' '\n" +
	"                  namespace: ' '\n" +
	"              dependencies:\n" +
	"                # LiteralTestStep is a full test step definition.\n" +
	"                - env: ' '\n" +
	"                  name: ' '\n" +
	"              dnsConfig:\n" +
	"                # LiteralTestStep is a full test step definition.\n" +
	"                nameservers:\n" +
	"                    # LiteralTestStep is a full test step definition.\n" +
	"                    - \"\"\n" +
	"                searches:\n" +
	"                    # LiteralTestStep is a full test step definition.\n" +
	"                    - \"\"\n" +
	"              env:\n" +
	"                # LiteralTestStep is a full test step definition.\n" +
	"                - default: \"\"\n" +
	"                  documentation: ' '\n" +
	"                  name: ' '\n" +
	"              from: ' '\n" +
	"              from_image:\n" +
	"                # LiteralTestStep is a full test step definition.\n" +
	"                as: ' '\n" +
	"                name: ' '\n" +
	"                namespace: ' '\n" +
	"                tag: ' '\n" +
	"              grace_period: 0s\n" +
	"              leases:\n" +
	"                # LiteralTestStep is a full test step definition.\n" +
	"                - env: ' '\n" +
	"                  resource_type: ' '\n" +
	"              observers:\n" +
	"                # LiteralTestStep is a full test step definition.\n" +
	"                - \"\"\n" +
	"              optional_on_success: false\n" +
	"              # Reference is the name of a step reference.\n" +
	"              ref: \"\"\n" +
	"              # Resources defines the resource requirements for the step.\n" +
	"              resources:\n" +
	"                # LiteralTestStep is a full test step definition.\n" +
	"                limits:\n" +
	"                    # LiteralTestStep is a full test step definition.\n" +
	"                    \"\": \"\"\n" +
	"                requests:\n" +
	"                    # LiteralTestStep is a full test step definition.\n" +
	"                    \"\": \"\"\n" +
	"              run_as_script: false\n" +
	"              timeout: 0s\n" +
	"        # Workflow is the name of the workflow to be used for this configuration. For fields defined in both\n" +
	"        # the config and the workflow, the fields from the config will override what is set in Workflow.\n" +
	"        workflow: \"\"\n" +
	"      # Timeout overrides maximum prowjob duration\n" +
	"      timeout: 0s\n" +
	"zz_generated_metadata:\n" +
	"    branch: ' '\n" +
	"    org: ' '\n" +
	"    repo: ' '\n" +
	"    variant: ' '\n" +
	""
