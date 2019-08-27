package steptypes

import (
	api "github.com/openshift/ci-tools/pkg/api"
)

// TestStep contains a full definition of a test step from a MultiStageTestConfiguration
type TestStep struct {
	Name        string
	Image       string
	Commands    string
	ArtifactDir string
	Resources   api.ResourceRequirements
}

// TestFlow contains the separate stages of a MultiStageTestConfigurations with full TestSteps in each stage
// ClusterProfile can be used to define an infrastructure/profile to use (i.e. aws, azure4, gcp, etc.)
type TestFlow struct {
	ClusterProfile api.ClusterProfile
	Pre            []TestStep
	Test           []TestStep
	Post           []TestStep
}
