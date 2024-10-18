package aws

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
	Workloads map[string]CISchedulingWebhookArchToAZ `json:"workloads,omitempty"`
}

type CISchedulingWebhookArchToAZ map[string][]string
