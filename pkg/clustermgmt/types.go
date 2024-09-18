package clustermgmt

import (
	"context"
	"os/exec"
)

type ClusterInstall struct {
	ClusterName string    `json:"clusterName,omitempty"`
	InstallBase string    `json:"installBase,omitempty"`
	Provision   Provision `json:"provision,omitempty"`
	Onboard     Onboard   `json:"onboard,omitempty"`
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

type Onboard struct {
	ReleaseRepo              string `json:"releaseRepo,omitempty"`
	KubeconfigDir            string `json:"kubeconfigDir,omitempty"`
	KubeconfigSuffix         string `json:"kubeconfigSuffix,omitempty"`
	OSD                      *bool  `json:"osd,omitempty"`
	Hosted                   *bool  `json:"hosted,omitempty"`
	Unmanaged                *bool  `json:"unmanaged,omitempty"`
	UseTokenFileInKubeconfig *bool  `json:"useTokenFileInKubeconfig,omitempty"`
	Dex                      Dex    `json:"dex,omitempty"`
}

type Dex struct {
	RedirectURIs map[string]string `json:"redirectURI,omitempty"`
}

type Step interface {
	Run(ctx context.Context) error
	Name() string
}

type CmdBuilder func(ctx context.Context, program string, args ...string) *exec.Cmd
type CmdRunner func(cmd *exec.Cmd) error
