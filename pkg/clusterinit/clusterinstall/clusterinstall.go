package clusterinstall

import (
	"context"
	"os/exec"
)

type ClusterInstall struct {
	ClusterName string `json:"clusterName,omitempty"`
	InstallBase string
	Provision   Provision `json:"provision,omitempty"`
	Onboard     Onboard   `json:"onboard,omitempty"`
}

type ClusterInstallGetter func() (*ClusterInstall, error)

type Provision struct {
	AWS *AWSProvision `json:"aws,omitempty"`
	GCP *GCPProvision `json:"gcp,omitempty"`
}

type GCPProvision struct{}

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
	ReleaseRepo string
	// True if the cluster is an OSD cluster. Set to true by default
	OSD *bool `json:"osd,omitempty"`
	// True if the cluster is hosted (i.e., HyperShift hosted cluster). Set to false by default
	Hosted *bool `json:"hosted,omitempty"`
	// True if the cluster is unmanaged (i.e., not managed by DPTP). Set to false by default
	Unmanaged *bool `json:"unmanaged,omitempty"`
	// True if the token files are used in kubeconfigs. Set to true by default
	UseTokenFileInKubeconfig *bool                  `json:"useTokenFileInKubeconfig,omitempty"`
	Dex                      Dex                    `json:"dex,omitempty"`
	QuayioPullThroughCache   QuayioPullThroughCache `json:"quayioPullThroughCache,omitempty"`
	Certificate              Certificate            `json:"certificate,omitempty"`
}

type Dex struct {
	RedirectURI string `json:"redirectURI,omitempty"`
}

type QuayioPullThroughCache struct {
	MirrorURI string `json:"mirrorURI,omitempty"`
}

type CertificateProjectLabel struct {
	Key   string `json:"key,omitempty"`
	Value string `json:"value,omitempty"`
}

type Certificate struct {
	BaseDomains             string                             `json:"baseDomains,omitempty"`
	ImageRegistryPublicHost string                             `json:"imageRegistryPublicHost,omitempty"`
	ClusterIssuer           map[string]string                  `json:"clusterIssuer,omitempty"`
	ProjectLabel            map[string]CertificateProjectLabel `json:"projectLabel,omitempty"`
}

type Step interface {
	Run(ctx context.Context) error
	Name() string
}

type CmdBuilder func(ctx context.Context, program string, args ...string) *exec.Cmd
type CmdRunner func(cmd *exec.Cmd) error
