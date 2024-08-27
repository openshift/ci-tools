package clustermgmt

import "context"

type ClusterInstall struct {
	Provision Provision `json:"provision,omitempty"`
}

type ClusterInstallGetter func() (*ClusterInstall, error)

type Provision struct {
	AWS *AWSProvision `json:"aws,omitempty"`
}

type AWSProvision struct {
	CloudFormationTemplates []AWSCloudFormationTemplate `json:"cloudFormationTemplates,omitempty"`
}

type AWSCloudFormationTemplate struct {
	StackName    string `json:"stackName,omitempty"`
	TemplateBody string `json:"templateBody,omitempty"`
	Parameters   []struct {
		Key   string `json:"key,omitempty"`
		Value string `json:"value,omitempty"`
	} `json:"parameters,omitempty"`
	Capabilities []string `json:"capabilities,omitempty"`
}

type Step interface {
	Run(ctx context.Context) error
	Name() string
}
