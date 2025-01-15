package clusterinstall

import (
	rhcostream "github.com/coreos/stream-metadata-go/stream"

	"k8s.io/client-go/rest"

	configv1 "github.com/openshift/api/config/v1"
	installertypes "github.com/openshift/installer/pkg/types"

	"github.com/openshift/ci-tools/pkg/clusterinit/manifest"
	"github.com/openshift/ci-tools/pkg/clusterinit/types"
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
	// This is needed to get info about available OS images
	CoreOSStream rhcostream.Stream
	Config       *rest.Config
}

func (ci *ClusterInstall) IsOCP() bool {
	return !(*ci.Onboard.Hosted || *ci.Onboard.OSD || *ci.Onboard.Unmanaged)
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
	UseTokenFileInKubeconfig   *bool                      `json:"useTokenFileInKubeconfig,omitempty"`
	Multiarch                  *bool                      `json:"multiarch,omitempty"`
	Dex                        Dex                        `json:"dex,omitempty"`
	QuayioPullThroughCache     QuayioPullThroughCache     `json:"quayioPullThroughCache,omitempty"`
	Certificate                Certificate                `json:"certificate,omitempty"`
	CISchedulingWebhook        CISchedulingWebhook        `json:"ciSchedulingWebhook,omitempty"`
	MachineSet                 MachineSet                 `json:"machineSet,omitempty"`
	MultiarchBuilderController MultiarchBuilderController `json:"multiarchBuilderController,omitempty"`
	ImageRegistry              ImageRegistry              `json:"imageRegistry,omitempty"`
	PassthroughManifest        PassthroughManifest        `json:"passthrough,omitempty"`
	CloudabilityAgent          CloudabilityAgent          `json:"cloudabilityAgent,omitempty"`
	OpenshiftMonitoring        OpenshiftMonitoring        `json:"openshiftMonitoring,omitempty"`
	MultiarchTuningOperator    MultiarchTuningOperator    `json:"multiarchTuningOperator,omitempty"`
	CertManagerOperator        CertManagerOperator        `json:"certManagerOperator,omitempty"`
	OAuthTemplate              OAuthTemplate              `json:"oauthTemplate,omitempty"`
}

type Dex struct {
	types.SkipStep
	types.ExcludeManifest
	Patches []manifest.Patch `json:"patches,omitempty"`
}

type QuayioPullThroughCache struct {
	types.SkipStep
	types.ExcludeManifest
	Patches []manifest.Patch `json:"patches,omitempty"`
}

type CertificateProjectLabel struct {
	Key   string `json:"key,omitempty"`
	Value string `json:"value,omitempty"`
}
type CISchedulingWebhook struct {
	types.SkipStep
	types.ExcludeManifest
	Patches     []manifest.Patch        `json:"patches,omitempty"`
	AWS         aws.CISchedulingWebhook `json:"aws,omitempty"`
	GenerateDNS bool                    `json:"dns,omitempty"`
}

type MachineSet struct {
	types.SkipStep
	types.ExcludeManifest
	Patches []manifest.Patch `json:"patches,omitempty"`
	AWS     aws.MachineSet   `json:"aws,omitempty"`
}

type MultiarchBuilderController struct {
	types.SkipStep
	types.ExcludeManifest
	Patches []manifest.Patch `json:"patches,omitempty"`
}

type ImageRegistry struct {
	types.SkipStep
	types.ExcludeManifest
	Patches []manifest.Patch `json:"patches,omitempty"`
}

type PassthroughManifest struct {
	types.SkipStep
	types.ExcludeManifest
}

type CloudabilityAgent struct {
	types.SkipStep
	types.ExcludeManifest
	Patches []manifest.Patch `json:"patches,omitempty"`
}

type OpenshiftMonitoring struct {
	types.SkipStep
	types.ExcludeManifest
	Patches []manifest.Patch `json:"patches,omitempty"`
}

type MultiarchTuningOperator struct {
	types.SkipStep
}

const (
	MachineProfileWorker string = "worker"
	MachineProfileInfra  string = "infra"
)

var (
	MachineProfileDefaults []string = []string{MachineProfileWorker, MachineProfileInfra}
)

const (
	BuildsWorkload    string = "builds"
	TestsWorkload     string = "tests"
	LongTestsWorkload string = "longtests"
	ProwJobsWorkload  string = "prowjobs"
)

var (
	CIWorkloadDefaults []string = []string{BuildsWorkload, TestsWorkload, LongTestsWorkload, ProwJobsWorkload}
)

type Certificate struct {
	types.SkipStep
	types.ExcludeManifest
	Patches                 []manifest.Patch `json:"patches,omitempty"`
	ImageRegistryPublicHost string           `json:"imageRegistryPublicHost,omitempty"`
}

type CertManagerOperator struct {
	types.SkipStep
	types.ExcludeManifest
	Patches []manifest.Patch `json:"patches,omitempty"`
}

type OAuthTemplate struct {
	types.SkipStep
	types.ExcludeManifest
	Patches []manifest.Patch `json:"patches,omitempty"`
}
