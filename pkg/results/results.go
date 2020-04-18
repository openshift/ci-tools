package results

type Reason string

const (
	// ReasonUnknown is default reason. Occurrences of this reason in metrics
	// indicate a bug, a failure to identify the reason for an error somewhere.
	ReasonUnknown Reason = "unknown"

	// ReasonLoadingArgs indicates a failure to load arguments at startup
	ReasonLoadingArgs Reason = "loading_args"
	// ReasonMissingJobSpec indicates a missing job specification
	ReasonMissingJobSpec Reason = "missing_job_spec"
	// ReasonLoadingConfig indicates a failure to load configuration
	ReasonLoadingConfig Reason = "loading_config"
	// ReasonConfigResolver indicates a failure to load configuration from the registry
	ReasonConfigResolver Reason = "config_resolver"
	// ReasonValidatingConfig indicates a failure to validate configuration
	ReasonValidatingConfig Reason = "validating_config"
	// ReasonDefaultingConfig indicates a failure defaulting configuration
	ReasonDefaultingConfig Reason = "defaulting_config"

	// ReasonResolvingInputs indicates a failure to resolve inputs
	ReasonResolvingInputs Reason = "resolving_inputs"
	// ReasonBuildingGraph indicates a failure to build the execution graph
	ReasonBuildingGraph Reason = "building_graph"
	// ReasonInitializingNamespace indicates a failure to initialize the namespace
	ReasonInitializingNamespace Reason = "initializing_namespace"
	// ReasonExecutingGraph indicates a failure to execute the job graph
	ReasonExecutingGraph Reason = "executing_graph"
	// ReasonExecutingPost indicates a failure to execute the post steps
	ReasonExecutingPost Reason = "executing_post"
)
