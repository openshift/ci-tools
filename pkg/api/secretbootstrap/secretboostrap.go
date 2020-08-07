package secretbootstrap

import (
	corev1 "k8s.io/api/core/v1"
)

type AttributeType string

const (
	AttributeTypePassword AttributeType = "password"
)

type BitWardenContext struct {
	BWItem     string        `json:"bw_item"`
	Field      string        `json:"field,omitempty"`
	Attachment string        `json:"attachment,omitempty"`
	Attribute  AttributeType `json:"attribute,omitempty"`
}

type SecretContext struct {
	Cluster   string            `json:"cluster"`
	Namespace string            `json:"namespace"`
	Name      string            `json:"name"`
	Type      corev1.SecretType `json:"type,omitempty"`
}

type SecretConfig struct {
	From map[string]BitWardenContext `json:"from"`
	To   []SecretContext             `json:"to"`
}

// Config is what we version in our repository.
type Config []SecretConfig
