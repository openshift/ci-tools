package clusterinstall

import (
	configv1 "github.com/openshift/api/config/v1"
	installertypes "github.com/openshift/installer/pkg/types"

	"github.com/openshift/ci-tools/pkg/clusterinit/manifest"
	"github.com/openshift/ci-tools/pkg/clusterinit/types/aws"
	"github.com/openshift/ci-tools/pkg/clusterinit/types/gcp"
)

type ClusterInstall struct {
	ClusterName    string    `json:"clusterName,omitempty"`
	Provision      Provision `json:"provision,omitempty"`
	Onboard        Onboard   `json:"onboard,omitempty"`
	InstallBase    string
	Infrastructure configv1.Infrastructure
	InstallConfig  installertypes.InstallConfig
}

type Provision struct {
	AWS *aws.Provision `json:"aws,omitempty"`
	GCP *gcp.Provision `json:"gcp,omitempty"`
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
	CISchedulingWebhook      CISchedulingWebhook    `json:"ciSchedulingWebhook,omitempty"`
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
type CISchedulingWebhook struct {
	SkipStep
	AWS         aws.CISchedulingWebhook `json:"aws,omitempty"`
	GenerateDNS bool                    `json:"dns,omitempty"`
	Patches     []manifest.Patch        `json:"patches,omitempty"`
}

type CIWorkload string

const (
	BuildsWorkload    CIWorkload = "builds"
	TestsWorkload     CIWorkload = "tests"
	LongTestsWorkload CIWorkload = "longtests"
	ProwJobsWorkload  CIWorkload = "prowjobs"
)

var (
	CIWorkloadDefaults []CIWorkload = []CIWorkload{BuildsWorkload, TestsWorkload, LongTestsWorkload, ProwJobsWorkload}
)

type Architecture string

var (
	ArchAMD64   Architecture = "amd64"
	ArchARM64   Architecture = "arm64"
	ArchAARCH64 Architecture = "aarch64"
)

type Certificate struct {
	BaseDomains             string                             `json:"baseDomains,omitempty"`
	ImageRegistryPublicHost string                             `json:"imageRegistryPublicHost,omitempty"`
	ClusterIssuer           map[string]string                  `json:"clusterIssuer,omitempty"`
	ProjectLabel            map[string]CertificateProjectLabel `json:"projectLabel,omitempty"`
}

type SkipStep struct {
	Skip   bool   `json:"skip,omitempty"`
	Reason string `json:"reason,omitempty"`
}
