package v1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// OverrideKind describes the runtime scheduling control applied by an override.
// +kubebuilder:validation:Enum=Capacity;Drain
type OverrideKind string

const (
	// OverrideKindCapacity changes the effective capacity of a cluster or capability scope.
	OverrideKindCapacity OverrideKind = "Capacity"
	// OverrideKindDrain removes a cluster or capability scope from runtime placement.
	OverrideKindDrain OverrideKind = "Drain"
)

// OverrideState describes the lifecycle state of a DispatchOverride.
// +kubebuilder:validation:Enum=Pending;Active;Expired;Revoked;Rejected;Failed
type OverrideState string

const (
	OverrideStatePending  OverrideState = "Pending"
	OverrideStateActive   OverrideState = "Active"
	OverrideStateExpired  OverrideState = "Expired"
	OverrideStateRevoked  OverrideState = "Revoked"
	OverrideStateRejected OverrideState = "Rejected"
	OverrideStateFailed   OverrideState = "Failed"
)

// DispatchOverrideScope narrows an override to jobs requiring one capability.
type DispatchOverrideScope struct {
	// +kubebuilder:validation:MinLength=1
	Capability string `json:"capability,omitempty"`
}

// DispatchApproval records one Slack user approval.
type DispatchApproval struct {
	// +kubebuilder:validation:MinLength=1
	UserID string      `json:"userID"`
	At     metav1.Time `json:"at"`
}

// DispatchAuditEvent records a durable state transition for an override.
type DispatchAuditEvent struct {
	At         metav1.Time   `json:"at"`
	State      OverrideState `json:"state"`
	Actor      string        `json:"actor,omitempty"`
	Message    string        `json:"message,omitempty"`
	Generation uint64        `json:"generation,omitempty"`
}

// DispatchOverrideSpec is the desired temporary scheduling policy.
// +kubebuilder:validation:XValidation:rule="self.kind == 'Capacity' ? has(self.capacity) : !has(self.capacity)",message="capacity is required only for Capacity overrides"
// +kubebuilder:validation:XValidation:rule="self.id == oldSelf.id && self.planID == oldSelf.planID && self.sourceGeneration == oldSelf.sourceGeneration && self.policyInputDigest == oldSelf.policyInputDigest && self.kind == oldSelf.kind && self.cluster == oldSelf.cluster && self.startsAt == oldSelf.startsAt && self.expiresAt == oldSelf.expiresAt && self.createdBy == oldSelf.createdBy && self.sourceChannelID == oldSelf.sourceChannelID && self.reason == oldSelf.reason && self.requiredApprovals == oldSelf.requiredApprovals && self.fallbackProtected == oldSelf.fallbackProtected && self.idempotencyKey == oldSelf.idempotencyKey",message="immutable override identity and policy fields cannot change"
// +kubebuilder:validation:XValidation:rule="has(self.scope) == has(oldSelf.scope) && (!has(self.scope) || self.scope == oldSelf.scope)",message="override scope is immutable"
// +kubebuilder:validation:XValidation:rule="has(self.capacity) == has(oldSelf.capacity) && (!has(self.capacity) || self.capacity == oldSelf.capacity)",message="override capacity is immutable"
// +kubebuilder:validation:XValidation:rule="has(self.incidentURL) == has(oldSelf.incidentURL) && (!has(self.incidentURL) || self.incidentURL == oldSelf.incidentURL)",message="incident URL is immutable"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.approvals) || (has(self.approvals) && oldSelf.approvals.all(a, self.approvals.exists(b, b.userID == a.userID && b.at == a.at)))",message="approvals may only be appended"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.fallbackConfirmed) || !oldSelf.fallbackConfirmed || (has(self.fallbackConfirmed) && self.fallbackConfirmed)",message="fallback confirmation cannot be withdrawn"
// +kubebuilder:validation:XValidation:rule="has(self.revokedAt) == has(self.revokedBy) && (!has(self.revokedBy) || size(self.revokedBy) > 0)",message="revokedAt and a non-empty revokedBy must be set together"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.revokedAt) || (has(self.revokedAt) && self.revokedAt == oldSelf.revokedAt && has(oldSelf.revokedBy) && has(self.revokedBy) && self.revokedBy == oldSelf.revokedBy)",message="revocation cannot be removed or changed"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.slackThreadTS) || (has(self.slackThreadTS) && self.slackThreadTS == oldSelf.slackThreadTS)",message="Slack thread binding cannot be changed"
// +kubebuilder:validation:XValidation:rule="self.kind != 'Drain' || self.requiredApprovals == 2",message="drain overrides require two approvals"
// +kubebuilder:validation:XValidation:rule="self.kind != 'Drain' || self.fallbackProtected || (has(self.fallbackConfirmed) && self.fallbackConfirmed)",message="an unprotected drain requires explicit fallback confirmation"
type DispatchOverrideSpec struct {
	// +kubebuilder:validation:MinLength=1
	ID string `json:"id"`
	// +kubebuilder:validation:MinLength=1
	PlanID string `json:"planID"`
	// +kubebuilder:validation:Minimum=1
	SourceGeneration uint64 `json:"sourceGeneration"`
	// +kubebuilder:validation:MinLength=1
	PolicyInputDigest string       `json:"policyInputDigest"`
	Kind              OverrideKind `json:"kind"`
	// +kubebuilder:validation:MinLength=1
	Cluster string                `json:"cluster"`
	Scope   DispatchOverrideScope `json:"scope,omitempty"`
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=100
	Capacity  *int32      `json:"capacity,omitempty"`
	StartsAt  metav1.Time `json:"startsAt"`
	ExpiresAt metav1.Time `json:"expiresAt"`
	// +kubebuilder:validation:MinLength=1
	CreatedBy string `json:"createdBy"`
	// +kubebuilder:validation:MinLength=1
	SourceChannelID string `json:"sourceChannelID"`
	// +kubebuilder:validation:MinLength=1
	Reason      string `json:"reason"`
	IncidentURL string `json:"incidentURL,omitempty"`
	// +kubebuilder:validation:MaxItems=2
	// +listType=map
	// +listMapKey=userID
	Approvals []DispatchApproval `json:"approvals,omitempty"`
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=2
	RequiredApprovals int32        `json:"requiredApprovals"`
	FallbackProtected bool         `json:"fallbackProtected"`
	FallbackConfirmed bool         `json:"fallbackConfirmed,omitempty"`
	RevokedAt         *metav1.Time `json:"revokedAt,omitempty"`
	RevokedBy         string       `json:"revokedBy,omitempty"`
	SlackThreadTS     string       `json:"slackThreadTS,omitempty"`
	// +kubebuilder:validation:MinLength=1
	IdempotencyKey string `json:"idempotencyKey"`
}

// DispatchOverrideStatus is the observed override and publication state.
type DispatchOverrideStatus struct {
	State              OverrideState `json:"state,omitempty"`
	ObservedGeneration int64         `json:"observedGeneration,omitempty"`
	PolicyGeneration   uint64        `json:"policyGeneration,omitempty"`
	SnapshotChecksum   string        `json:"snapshotChecksum,omitempty"`
	FallbackProtected  bool          `json:"fallbackProtected,omitempty"`
	Message            string        `json:"message,omitempty"`
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
	// +kubebuilder:validation:MaxItems=50
	History []DispatchAuditEvent `json:"history,omitempty"`
}

// DispatchOverride is a durable, temporary overlay on dispatcher baseline policy.
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:resource:shortName=doverride
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Kind",type=string,JSONPath=`.spec.kind`
// +kubebuilder:printcolumn:name="Cluster",type=string,JSONPath=`.spec.cluster`
// +kubebuilder:printcolumn:name="State",type=string,JSONPath=`.status.state`
// +kubebuilder:printcolumn:name="Expires",type=date,JSONPath=`.spec.expiresAt`
// +kubebuilder:validation:XValidation:rule="self.metadata.name == self.spec.id",message="metadata.name must equal spec.id"
type DispatchOverride struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`

	Spec   DispatchOverrideSpec   `json:"spec"`
	Status DispatchOverrideStatus `json:"status,omitempty"`
}

// DispatchOverrideList contains DispatchOverride resources.
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type DispatchOverrideList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`

	Items []DispatchOverride `json:"items"`
}
