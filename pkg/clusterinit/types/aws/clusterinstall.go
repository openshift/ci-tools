package aws

import "github.com/openshift/ci-tools/pkg/clusterinit/types"

type CloudFormationTemplate struct {
	StackName    string `json:"stackName,omitempty"`
	TemplateBody string `json:"templateBody,omitempty"`
	Parameters   []struct {
		Key   string `json:"key,omitempty"`
		Value string `json:"value,omitempty"`
	} `json:"parameters,omitempty"`
	Capabilities []string `json:"capabilities,omitempty"`
}

type Provision struct {
	CloudFormationTemplates []CloudFormationTemplate `json:"cloudFormationTemplates,omitempty"`
}

type CISchedulingWebhook struct {
	Workloads map[string]ArchToAZ `json:"workloads,omitempty"`
}

type MachineSet struct {
	Profiles map[string]MachineSetProfile `json:"profiles,omitempty"`
}

type MachineSetProfile struct {
	MachineAutoscaler *bool    `json:"machineAutoscaler,omitempty"`
	Architectures     ArchToAZ `json:"architectures,omitempty"`
}

type ArchToAZ map[types.Architecture][]string
