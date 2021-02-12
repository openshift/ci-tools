package nsttl

// This package contains constants for tools that create namespaces
// to be reaped by https://github.com/openshift/ci-ns-ttl-controller/

const (
	// AnnotationIdleCleanupDurationTTL is the annotation for requesting namespace cleanup after all pods complete
	AnnotationIdleCleanupDurationTTL = "ci.openshift.io/ttl.soft"
	// AnnotationCleanupDurationTTL is the annotation for requesting namespace cleanup after the namespace has been active
	AnnotationCleanupDurationTTL = "ci.openshift.io/ttl.hard"
	// AnnotationNamespaceLastActive contains time.RFC3339 timestamp at which the namespace was last in active use. We
	// update this every ten minutes.
	AnnotationNamespaceLastActive = "ci.openshift.io/active"
)
