package results

type Reason string

const (
	// ReasonUnknown is default reason. Occurrences of this reason in metrics
	// indicate a bug, a failure to identify the reason for an error somewhere.
	ReasonUnknown Reason = "unknown"
)
