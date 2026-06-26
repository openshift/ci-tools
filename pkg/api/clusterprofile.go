package api

import (
	"fmt"
	"slices"

	aggerrs "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"

	utilregexp "github.com/openshift/ci-tools/pkg/util/regexp"
)

// ClusterProfile is the name of a set of input variables
// provided to the installer defining the target cloud,
// cluster topology, etc.
type ClusterProfile string

const (
	ClusterProfileAWS                        ClusterProfile = "aws"
	ClusterProfileAWSUSEast1                 ClusterProfile = "aws-us-east-1"
	ClusterProfileAWSCSPIQE                  ClusterProfile = "aws-cspi-qe"
	ClusterProfileAWSQE                      ClusterProfile = "aws-qe"
	ClusterProfileAWSC2SQE                   ClusterProfile = "aws-c2s-qe"
	ClusterProfileAWSChinaQE                 ClusterProfile = "aws-china-qe"
	ClusterProfileAWSGovCloudQE              ClusterProfile = "aws-usgov-qe"
	ClusterProfileAWSSC2SQE                  ClusterProfile = "aws-sc2s-qe"
	ClusterProfileAWS1QE                     ClusterProfile = "aws-1-qe"
	ClusterProfileAWSAutoreleaseQE           ClusterProfile = "aws-autorelease-qe"
	ClusterProfileAWSSdQE                    ClusterProfile = "aws-sd-qe"
	ClusterProfileOEXAWSQE                   ClusterProfile = "oex-aws-qe"
	ClusterProfileHyperfleetE2E              ClusterProfile = "hyperfleet-e2e"
	ClusterProfileAWSPerfScale               ClusterProfile = "aws-perfscale"
	ClusterProfileAWSPerfScaleOKD            ClusterProfile = "aws-perfscale-okd"
	ClusterProfileAWSPerfScaleQE             ClusterProfile = "aws-perfscale-qe"
	ClusterProfileAWSPerfScaleLRCQE          ClusterProfile = "aws-perfscale-lrc-qe"
	ClusterProfileAWSRestrictedQE            ClusterProfile = "aws-restricted-qe"
	ClusterProfileROSAE2E01                  ClusterProfile = "rosa-e2e-01"
	ClusterProfileROSAE2E02                  ClusterProfile = "rosa-e2e-02"
	ClusterProfileROSAE2E03                  ClusterProfile = "rosa-e2e-03"
	ClusterProfileAWSEUSC                    ClusterProfile = "aws-eusc"
	ClusterProfileAWSOutpostQE               ClusterProfile = "aws-outpost-qe"
	ClusterProfileAWSChaos                   ClusterProfile = "aws-chaos"
	ClusterProfileAWSManagedCSPIQE           ClusterProfile = "aws-managed-cspi-qe"
	ClusterProfileAWSOSDMSP                  ClusterProfile = "aws-osd-msp"
	ClusterProfileAWSINTEROPQE               ClusterProfile = "aws-interop-qe"
	ClusterProfileAWSTerraformQE             ClusterProfile = "aws-terraform-qe"
	ClusterProfileAWSPipelinesPerf           ClusterProfile = "aws-pipelines-performance"
	ClusterProfileAWSRHTAPQE                 ClusterProfile = "aws-rhtap-qe"
	ClusterProfileAWSKonfluxQE               ClusterProfile = "aws-konflux-qe"
	ClusterProfileAWSRHTAPPerformance        ClusterProfile = "aws-rhtap-performance"
	ClusterProfileAWSRHDHPerf                ClusterProfile = "aws-rhdh-performance"
	ClusterProfileAWSRHDHDisconnected        ClusterProfile = "aws-rhdh-disconnected"
	ClusterProfileAWSServerless              ClusterProfile = "aws-serverless"
	ClusterProfileAWSTelco                   ClusterProfile = "aws-telco"
	ClusterProfileAWSOpendatahub             ClusterProfile = "aws-opendatahub"
	ClusterProfileAWSDevfile                 ClusterProfile = "aws-devfile"
	ClusterProfileAWSSustAutoRel412          ClusterProfile = "aws-sustaining-autorelease-412"
	ClusterProfileAWSKubeVirt                ClusterProfile = "aws-kubevirt"
	ClusterProfileAWSOVNPerfScale            ClusterProfile = "aws-ovn-perfscale"
	ClusterProfileAWSConfidentialQE          ClusterProfile = "aws-confidential-qe"
	ClusterProfileAlibabaCloud               ClusterProfile = "alibabacloud"
	ClusterProfileAlibabaCloudQE             ClusterProfile = "alibabacloud-qe"
	ClusterProfileAlibabaCloudCNQE           ClusterProfile = "alibabacloud-cn-qe"
	ClusterProfileAzure2                     ClusterProfile = "azure-2"
	ClusterProfileAzure4                     ClusterProfile = "azure4"
	ClusterProfileAzureArm64                 ClusterProfile = "azure-arm64"
	ClusterProfileAzurePerfScale             ClusterProfile = "azure-perfscale"
	ClusterProfileAzureStack                 ClusterProfile = "azurestack"
	ClusterProfileAzureStackDEV              ClusterProfile = "azurestack-dev"
	ClusterProfileAzureStackQE               ClusterProfile = "azurestack-qe"
	ClusterProfileAzureMag                   ClusterProfile = "azuremag"
	ClusterProfileAzureQE                    ClusterProfile = "azure-qe"
	ClusterProfileAzureObservability         ClusterProfile = "azure-observability"
	ClusterProfileAzureHCPQE                 ClusterProfile = "azure-hcp-qe"
	ClusterProfileAzureHCPHAQE               ClusterProfile = "azure-hcp-ha-qe"
	ClusterProfileAzureAutoreleaseQE         ClusterProfile = "azure-autorelease-qe"
	ClusterProfileAzureArm64QE               ClusterProfile = "azure-arm64-qe"
	ClusterProfileAzureMagQE                 ClusterProfile = "azuremag-qe"
	ClusterProfileAzureSustAutoRel412        ClusterProfile = "azure-sustaining-autorelease-412"
	ClusterProfileAzureConfidentialQE        ClusterProfile = "azure-confidential-qe"
	ClusterProfileAzurePerfScaleQE           ClusterProfile = "azure-perfscale-qe"
	ClusterProfileAzureCNVDevOps             ClusterProfile = "azure-cnv-devops"
	ClusterProfileEquinixOcpMetal            ClusterProfile = "equinix-ocp-metal"
	ClusterProfileEquinixOcpMetalQE          ClusterProfile = "equinix-ocp-metal-qe"
	ClusterProfileEquinixOcpHCP              ClusterProfile = "equinix-ocp-hcp"
	ClusterProfileFleetManagerQE             ClusterProfile = "fleet-manager-qe"
	ClusterProfileGCPQE                      ClusterProfile = "gcp-qe"
	ClusterProfileGCPQEC3Metal               ClusterProfile = "gcp-qe-c3-metal"
	ClusterProfileGCPAutoReleaseQE           ClusterProfile = "gcp-autorelease-qe"
	ClusterProfileGCPArm64                   ClusterProfile = "gcp-arm64"
	ClusterProfileGCP                        ClusterProfile = "gcp"
	ClusterProfileGCP3                       ClusterProfile = "gcp-3"
	ClusterProfileGCP2                       ClusterProfile = "gcp-openshift-gce-devel-ci-2"
	ClusterProfileGCPOpendatahub             ClusterProfile = "gcp-opendatahub"
	ClusterProfileGCPTelco                   ClusterProfile = "gcp-telco"
	ClusterProfileGCPChaos                   ClusterProfile = "gcp-chaos"
	ClusterProfileGCPConfidentialQE          ClusterProfile = "gcp-confidential-qe"
	ClusterProfileGCPPerfScaleQE             ClusterProfile = "gcp-perfscale-qe"
	ClusterProfileIBMCloud                   ClusterProfile = "ibmcloud"
	ClusterProfileGCPSustAutoRel412          ClusterProfile = "gcp-sustaining-autorelease-412"
	ClusterProfileIBMCloudCSPIQE             ClusterProfile = "ibmcloud-cspi-qe"
	ClusterProfileIBMCloudRHOAIQE            ClusterProfile = "ibmcloud-rhoai-qe"
	ClusterProfileIBMCloudQE                 ClusterProfile = "ibmcloud-qe"
	ClusterProfileIBMCloudQE2                ClusterProfile = "ibmcloud-qe-2"
	ClusterProfileIBMCloudMultiPpc64le       ClusterProfile = "ibmcloud-multi-ppc64le"
	ClusterProfileIBMCloudMultiS390x         ClusterProfile = "ibmcloud-multi-s390x"
	ClusterProfilePOWERVC1                   ClusterProfile = "powervc-1"
	ClusterProfilePOWERVSMulti1              ClusterProfile = "powervs-multi-1"
	ClusterProfilePOWERVS1                   ClusterProfile = "powervs-1"
	ClusterProfilePOWERVS2                   ClusterProfile = "powervs-2"
	ClusterProfilePOWERVS3                   ClusterProfile = "powervs-3"
	ClusterProfilePOWERVS4                   ClusterProfile = "powervs-4"
	ClusterProfilePOWERVS5                   ClusterProfile = "powervs-5"
	ClusterProfilePOWERVS6                   ClusterProfile = "powervs-6"
	ClusterProfilePOWERVS7                   ClusterProfile = "powervs-7"
	ClusterProfilePOWERVS8                   ClusterProfile = "powervs-8"
	ClusterProfilePOWERVS9                   ClusterProfile = "powervs-9"
	ClusterProfileLibvirtPpc64le             ClusterProfile = "libvirt-ppc64le"
	ClusterProfileLibvirtPpc64leS2S          ClusterProfile = "libvirt-ppc64le-s2s"
	ClusterProfileLibvirtS390x               ClusterProfile = "libvirt-s390x"
	ClusterProfileLibvirtS390x1              ClusterProfile = "libvirt-s390x-1"
	ClusterProfileLibvirtS390x2              ClusterProfile = "libvirt-s390x-2"
	ClusterProfileLibvirtS390xAmd64          ClusterProfile = "libvirt-s390x-amd64"
	ClusterProfileLibvirtS390xVPN            ClusterProfile = "libvirt-s390x-vpn"
	ClusterProfileLibvirtS390xVPNOZ          ClusterProfile = "libvirt-s390x-vpn-oz"
	ClusterProfileLibvirtS390xVPNVirt        ClusterProfile = "libvirt-s390x-vpn-virt"
	ClusterProfileMetalPerfscaleBMCPT        ClusterProfile = "metal-perfscale-cpt"
	ClusterProfileMetalPerfscaleJetlag       ClusterProfile = "metal-perfscale-jetlag"
	ClusterProfileMetalPerfscaleOSP          ClusterProfile = "metal-perfscale-osp"
	ClusterProfileMetalPerfscaleOspNfv       ClusterProfile = "metal-perfscale-osp-nfv"
	ClusterProfileMetalPerfscaleOspSelfSched ClusterProfile = "metal-perfscale-osp-selfsched"
	ClusterProfileMetalPerfscaleSelfSched    ClusterProfile = "metal-perfscale-selfsched"
	ClusterProfileMetalPerfscaleTelco        ClusterProfile = "metal-perfscale-telco"
	ClusterProfileNutanix                    ClusterProfile = "nutanix"
	ClusterProfileNutanixQE                  ClusterProfile = "nutanix-qe"
	ClusterProfileNutanixQEDis               ClusterProfile = "nutanix-qe-dis"
	ClusterProfileNutanixQEZone              ClusterProfile = "nutanix-qe-zone"
	ClusterProfileNutanixQEGPU               ClusterProfile = "nutanix-qe-gpu"
	ClusterProfileNutanixQEFlow              ClusterProfile = "nutanix-qe-flow"
	ClusterProfileOpenStackHwoffload         ClusterProfile = "openstack-hwoffload"
	ClusterProfileOpenStackIBMOSP            ClusterProfile = "openstack-ibm-osp"
	ClusterProfileOpenStackNFV               ClusterProfile = "openstack-nfv"
	ClusterProfileOpenStackMechaCentral      ClusterProfile = "openstack-vh-mecha-central"
	ClusterProfileOpenStackOsuosl            ClusterProfile = "openstack-osuosl"
	ClusterProfileOpenStackVexxhost          ClusterProfile = "openstack-vexxhost"
	ClusterProfileOpenStackVexxhostRHOS      ClusterProfile = "openstack-vh-bm-rhos"
	ClusterProfileOpenStackPpc64le           ClusterProfile = "openstack-ppc64le"
	ClusterProfileOpenStackOpVexxhost        ClusterProfile = "openstack-operators-vexxhost"
	ClusterProfileOpenStackNercDev           ClusterProfile = "openstack-nerc-dev"
	ClusterProfileOpenStackRHOSO             ClusterProfile = "openstack-rhoso"
	ClusterProfileOpenStackRHOSCI            ClusterProfile = "openstack-rhos-ci"
	ClusterProfileOvirt                      ClusterProfile = "ovirt"
	ClusterProfilePacket                     ClusterProfile = "packet"
	ClusterProfilePacketAssisted             ClusterProfile = "packet-assisted"
	ClusterProfilePacketSNO                  ClusterProfile = "packet-sno"
	ClusterProfilePacketOSAC                 ClusterProfile = "packet-osac"
	ClusterProfileVSphereDis2                ClusterProfile = "vsphere-dis-2"
	ClusterProfileVSphereMultizone2          ClusterProfile = "vsphere-multizone-2"
	ClusterProfileVSphereConnected2          ClusterProfile = "vsphere-connected-2"
	ClusterProfileVSphereElastic             ClusterProfile = "vsphere-elastic"
	ClusterProfileVSphereElasticPoc          ClusterProfile = "vsphere-elastic-poc"
	ClusterProfileKubevirt                   ClusterProfile = "kubevirt"
	ClusterProfileAWSCPaaS                   ClusterProfile = "aws-cpaas"
	ClusterProfileOSDEphemeral               ClusterProfile = "osd-ephemeral"
	ClusterProfileAWS2                       ClusterProfile = "aws-2"
	ClusterProfileAWS3                       ClusterProfile = "aws-3"
	ClusterProfileAWS4                       ClusterProfile = "aws-4"
	ClusterProfileAWS5                       ClusterProfile = "aws-5"
	ClusterProfileOpenshiftOrgAWS            ClusterProfile = "openshift-org-aws"
	ClusterProfileOpenshiftOrgAzure          ClusterProfile = "openshift-org-azure"
	ClusterProfileOpenshiftOrgGCP            ClusterProfile = "openshift-org-gcp"
	ClusterProfileGCPVirtualization          ClusterProfile = "gcp-virtualization"
	ClusterProfileAWSVirtualization          ClusterProfile = "aws-virtualization"
	ClusterProfileAzureVirtualization        ClusterProfile = "azure-virtualization"
	ClusterProfileOCIAgent                   ClusterProfile = "oci-agent-qe"
	ClusterProfileOCIAssisted                ClusterProfile = "oci-assisted"
	ClusterProfileHypershiftAWS              ClusterProfile = "hypershift-aws"
	ClusterProfileHypershiftAzure            ClusterProfile = "hypershift-azure"
	ClusterProfileHypershiftAKS              ClusterProfile = "hypershift-aks"
	ClusterProfileHypershiftPowerVS          ClusterProfile = "hypershift-powervs"
	ClusterProfileHypershiftPowerVSCB        ClusterProfile = "hypershift-powervs-cb"
	ClusterProfileHypershiftGCP              ClusterProfile = "hypershift-gcp"
	ClusterProfileOSSM                       ClusterProfile = "ossm-aws"
	ClusterProfileMedik8sAWS                 ClusterProfile = "medik8s-aws"
	ClusterProfileGitOpsAWS                  ClusterProfile = "gitops-aws"
	ClusterProfileCheAWS                     ClusterProfile = "che-aws"
	ClusterProfileOSLGCP                     ClusterProfile = "osl-gcp"
	ClusterProfileDevSandboxCIAWS            ClusterProfile = "devsandboxci-aws"
	ClusterProfileQuayAWS                    ClusterProfile = "quay-aws"
	ClusterProfileAWSEdgeInfra               ClusterProfile = "aws-edge-infra"
	ClusterProfileRHOpenShiftEcosystem       ClusterProfile = "rh-openshift-ecosystem"
	ClusterProfileODFAWS                     ClusterProfile = "odf-aws"
	ClusterProfileAWSObservabiltity          ClusterProfile = "aws-observability"
	ClusterProfileAWSStackrox                ClusterProfile = "aws-stackrox"
	ClusterProfileAroRH                      ClusterProfile = "aro-redhat-tenant"
	ClusterProfileAWSRHOAIQE                 ClusterProfile = "aws-rhoai-qe"
	ClusterProfileAWSManagedRosaRHOAIQE      ClusterProfile = "aws-managed-rosa-rhoai-qe"
	ClusterProfileAWSManagedOSDRHOAIQE       ClusterProfile = "aws-managed-osd-rhoai-qe"
	ClusterProfileGCPObservability           ClusterProfile = "gcp-observability"
	ClusterProfileEquinixEdgeEnablement      ClusterProfile = "equinix-edge-enablement"
	ClusterProfileAWSQUAYQE                  ClusterProfile = "aws-quay-qe"
	ClusterProfileGCPQUAYQE                  ClusterProfile = "gcp-quay-qe"
	ClusterProfileAzureQUAYQE                ClusterProfile = "azure-quay-qe"
	ClusterProfileAWSMCOQE                   ClusterProfile = "aws-mco-qe"
	ClusterProfileAWSOADPQE                  ClusterProfile = "aws-oadp-qe"
	ClusterProfileAzureOADPQE                ClusterProfile = "azure-oadp-qe"
	ClusterProfileAWSlpChaos                 ClusterProfile = "aws-lp-chaos"
	ClusterProfileMetalRHgs                  ClusterProfile = "metal-redhat-gs"
	ClusterProfileAWSOSPQE                   ClusterProfile = "aws-osp-qe"
	ClusterProfileAWSOSC                     ClusterProfile = "aws-sandboxed-containers-operator"

	ClusterProfileRosaRegionalPlatformInt ClusterProfile = "rosa-regional-platform-int"

	ClusterProfileAROHCPInt  ClusterProfile = "aro-hcp-int"
	ClusterProfileAROHCPStg  ClusterProfile = "aro-hcp-stg"
	ClusterProfileAROHCPProd ClusterProfile = "aro-hcp-prod"
	ClusterProfileAROHCPDev  ClusterProfile = "aro-hcp-dev"

	ClusterProfileAROClassicInt  ClusterProfile = "aro-classic-int"
	ClusterProfileAROClassicStg  ClusterProfile = "aro-classic-stg"
	ClusterProfileAROClassicProd ClusterProfile = "aro-classic-prod"
	ClusterProfileAROClassicDev  ClusterProfile = "aro-classic-dev"
)

// ClusterProfiles are all valid cluster profiles
func ClusterProfiles() []ClusterProfile {
	return []ClusterProfile{
		ClusterProfileAWS,
		ClusterProfileAWSUSEast1,
		ClusterProfileAWS2,
		ClusterProfileAWS3,
		ClusterProfileAWS4,
		ClusterProfileAWS5,
		ClusterProfileOpenshiftOrgAWS,
		ClusterProfileOpenshiftOrgAzure,
		ClusterProfileOpenshiftOrgGCP,
		ClusterProfileAWSC2SQE,
		ClusterProfileAWSCPaaS,
		ClusterProfileAWSCSPIQE,
		ClusterProfileAWSPerfScale,
		ClusterProfileAWSPerfScaleOKD,
		ClusterProfileAWSPerfScaleQE,
		ClusterProfileAWSPerfScaleLRCQE,
		ClusterProfileAWSRestrictedQE,
		ClusterProfileROSAE2E01,
		ClusterProfileROSAE2E02,
		ClusterProfileROSAE2E03,
		ClusterProfileAWSEUSC,
		ClusterProfileAWSChaos,
		ClusterProfileAWSChinaQE,
		ClusterProfileAWSManagedCSPIQE,
		ClusterProfileAWSGovCloudQE,
		ClusterProfileAWSOSDMSP,
		ClusterProfileAWSQE,
		ClusterProfileAWS1QE,
		ClusterProfileAWSAutoreleaseQE,
		ClusterProfileAWSSdQE,
		ClusterProfileOEXAWSQE,
		ClusterProfileHyperfleetE2E,
		ClusterProfileAWSSC2SQE,
		ClusterProfileAWSOutpostQE,
		ClusterProfileAWSINTEROPQE,
		ClusterProfileAWSTerraformQE,
		ClusterProfileAWSPipelinesPerf,
		ClusterProfileAWSRHTAPQE,
		ClusterProfileAWSKonfluxQE,
		ClusterProfileAWSRHTAPPerformance,
		ClusterProfileAWSRHDHPerf,
		ClusterProfileAWSRHDHDisconnected,
		ClusterProfileAWSServerless,
		ClusterProfileAWSStackrox,
		ClusterProfileAWSTelco,
		ClusterProfileAWSOpendatahub,
		ClusterProfileAWSDevfile,
		ClusterProfileAWSSustAutoRel412,
		ClusterProfileAWSKubeVirt,
		ClusterProfileAWSOVNPerfScale,
		ClusterProfileAWSConfidentialQE,
		ClusterProfileAlibabaCloud,
		ClusterProfileAlibabaCloudQE,
		ClusterProfileAlibabaCloudCNQE,
		ClusterProfileAzure2,
		ClusterProfileAzure4,
		ClusterProfileAzureArm64,
		ClusterProfileAzureArm64QE,
		ClusterProfileAzureMag,
		ClusterProfileAzureMagQE,
		ClusterProfileAzureQE,
		ClusterProfileAzureObservability,
		ClusterProfileAzureHCPQE,
		ClusterProfileAzureHCPHAQE,
		ClusterProfileAzureAutoreleaseQE,
		ClusterProfileAzurePerfScale,
		ClusterProfileAzureStack,
		ClusterProfileAzureStackDEV,
		ClusterProfileAzureStackQE,
		ClusterProfileAzureSustAutoRel412,
		ClusterProfileAzureConfidentialQE,
		ClusterProfileAzurePerfScaleQE,
		ClusterProfileAzureCNVDevOps,
		ClusterProfileEquinixOcpMetal,
		ClusterProfileEquinixOcpMetalQE,
		ClusterProfileEquinixOcpHCP,
		ClusterProfileFleetManagerQE,
		ClusterProfileGCP,
		ClusterProfileGCP2,
		ClusterProfileGCP3,
		ClusterProfileGCPQE,
		ClusterProfileGCPQEC3Metal,
		ClusterProfileGCPAutoReleaseQE,
		ClusterProfileGCPArm64,
		ClusterProfileGCPVirtualization,
		ClusterProfileGCPOpendatahub,
		ClusterProfileGCPTelco,
		ClusterProfileGCPChaos,
		ClusterProfileGCPConfidentialQE,
		ClusterProfileGCPPerfScaleQE,
		ClusterProfileGCPSustAutoRel412,
		ClusterProfileGCPObservability,
		ClusterProfileAWSVirtualization,
		ClusterProfileAzureVirtualization,
		ClusterProfileIBMCloud,
		ClusterProfileIBMCloudCSPIQE,
		ClusterProfileIBMCloudRHOAIQE,
		ClusterProfileIBMCloudQE,
		ClusterProfileIBMCloudQE2,
		ClusterProfileIBMCloudMultiPpc64le,
		ClusterProfilePOWERVC1,
		ClusterProfilePOWERVSMulti1,
		ClusterProfileIBMCloudMultiS390x,
		ClusterProfilePOWERVS1,
		ClusterProfilePOWERVS2,
		ClusterProfilePOWERVS3,
		ClusterProfilePOWERVS4,
		ClusterProfilePOWERVS5,
		ClusterProfilePOWERVS6,
		ClusterProfilePOWERVS7,
		ClusterProfilePOWERVS8,
		ClusterProfilePOWERVS9,
		ClusterProfileKubevirt,
		ClusterProfileLibvirtPpc64le,
		ClusterProfileLibvirtPpc64leS2S,
		ClusterProfileLibvirtS390x,
		ClusterProfileLibvirtS390x1,
		ClusterProfileLibvirtS390x2,
		ClusterProfileLibvirtS390xAmd64,
		ClusterProfileLibvirtS390xVPN,
		ClusterProfileLibvirtS390xVPNOZ,
		ClusterProfileLibvirtS390xVPNVirt,
		ClusterProfileMetalPerfscaleBMCPT,
		ClusterProfileMetalPerfscaleJetlag,
		ClusterProfileMetalPerfscaleOSP,
		ClusterProfileMetalPerfscaleOspNfv,
		ClusterProfileMetalPerfscaleOspSelfSched,
		ClusterProfileMetalPerfscaleSelfSched,
		ClusterProfileMetalPerfscaleTelco,
		ClusterProfileNutanix,
		ClusterProfileNutanixQE,
		ClusterProfileNutanixQEDis,
		ClusterProfileNutanixQEZone,
		ClusterProfileNutanixQEGPU,
		ClusterProfileNutanixQEFlow,
		ClusterProfileOSDEphemeral,
		ClusterProfileOpenStackHwoffload,
		ClusterProfileOpenStackIBMOSP,
		ClusterProfileOpenStackMechaCentral,
		ClusterProfileOpenStackNFV,
		ClusterProfileOpenStackOsuosl,
		ClusterProfileOpenStackPpc64le,
		ClusterProfileOpenStackVexxhost,
		ClusterProfileOpenStackVexxhostRHOS,
		ClusterProfileOpenStackOpVexxhost,
		ClusterProfileOpenStackNercDev,
		ClusterProfileOpenStackRHOSO,
		ClusterProfileOpenStackRHOSCI,
		ClusterProfileOvirt,
		ClusterProfilePacket,
		ClusterProfilePacketAssisted,
		ClusterProfilePacketSNO,
		ClusterProfilePacketOSAC,

		ClusterProfileVSphereDis2,
		ClusterProfileVSphereMultizone2,
		ClusterProfileVSphereConnected2,
		ClusterProfileVSphereElastic,
		ClusterProfileVSphereElasticPoc,
		ClusterProfileOCIAgent,
		ClusterProfileOCIAssisted,
		ClusterProfileHypershiftAWS,
		ClusterProfileHypershiftAzure,
		ClusterProfileHypershiftAKS,
		ClusterProfileHypershiftPowerVS,
		ClusterProfileHypershiftPowerVSCB,
		ClusterProfileHypershiftGCP,
		ClusterProfileOSSM,
		ClusterProfileMedik8sAWS,
		ClusterProfileGitOpsAWS,
		ClusterProfileCheAWS,
		ClusterProfileOSLGCP,
		ClusterProfileDevSandboxCIAWS,
		ClusterProfileQuayAWS,
		ClusterProfileAWSEdgeInfra,
		ClusterProfileRHOpenShiftEcosystem,
		ClusterProfileODFAWS,
		ClusterProfileAWSObservabiltity,
		ClusterProfileAroRH,
		ClusterProfileAWSRHOAIQE,
		ClusterProfileAWSManagedRosaRHOAIQE,
		ClusterProfileAWSManagedOSDRHOAIQE,
		ClusterProfileEquinixEdgeEnablement,
		ClusterProfileAWSQUAYQE,
		ClusterProfileGCPQUAYQE,
		ClusterProfileAzureQUAYQE,
		ClusterProfileAWSMCOQE,
		ClusterProfileAWSOADPQE,
		ClusterProfileAzureOADPQE,
		ClusterProfileAWSlpChaos,
		ClusterProfileMetalRHgs,
		ClusterProfileAWSOSPQE,
		ClusterProfileAWSOSC,

		ClusterProfileRosaRegionalPlatformInt,

		ClusterProfileAROHCPInt,
		ClusterProfileAROHCPStg,
		ClusterProfileAROHCPProd,
		ClusterProfileAROHCPDev,

		ClusterProfileAROClassicInt,
		ClusterProfileAROClassicStg,
		ClusterProfileAROClassicProd,
		ClusterProfileAROClassicDev,
	}
}

func (p ClusterProfile) Name() string {
	return string(p)
}

// ClusterType maps profiles to the type string used by tests.
func (p ClusterProfile) ClusterType() string {
	switch p {
	case
		ClusterProfileAWS,
		ClusterProfileAWSUSEast1,
		ClusterProfileAWSCSPIQE,
		ClusterProfileAWSManagedCSPIQE,
		ClusterProfileAWSCPaaS,
		ClusterProfileAWS2,
		ClusterProfileAWS3,
		ClusterProfileAWS4,
		ClusterProfileAWS5,
		ClusterProfileOpenshiftOrgAWS,
		ClusterProfileAWSQE,
		ClusterProfileAWSINTEROPQE,
		ClusterProfileAWS1QE,
		ClusterProfileAWSAutoreleaseQE,
		ClusterProfileAWSSdQE,
		ClusterProfileOEXAWSQE,
		ClusterProfileAWSVirtualization,
		ClusterProfileFleetManagerQE,
		ClusterProfileAWSPerfScale,
		ClusterProfileAWSPerfScaleOKD,
		ClusterProfileAWSPerfScaleQE,
		ClusterProfileAWSPerfScaleLRCQE,
		ClusterProfileAWSRestrictedQE,
		ClusterProfileROSAE2E01,
		ClusterProfileROSAE2E02,
		ClusterProfileROSAE2E03,
		ClusterProfileAWSServerless,
		ClusterProfileAWSStackrox,
		ClusterProfileAWSOutpostQE,
		ClusterProfileAWSChaos,
		ClusterProfileAWSTerraformQE,
		ClusterProfileAWSPipelinesPerf,
		ClusterProfileAWSRHTAPQE,
		ClusterProfileAWSKonfluxQE,
		ClusterProfileAWSRHTAPPerformance,
		ClusterProfileAWSRHDHPerf,
		ClusterProfileAWSRHDHDisconnected,
		ClusterProfileAWSSustAutoRel412,
		ClusterProfileAWSKubeVirt,
		ClusterProfileAWSOVNPerfScale,
		ClusterProfileOSSM,
		ClusterProfileAWSOpendatahub,
		ClusterProfileAWSDevfile,
		ClusterProfileAWSTelco,
		ClusterProfileMedik8sAWS,
		ClusterProfileGitOpsAWS,
		ClusterProfileCheAWS,
		ClusterProfileDevSandboxCIAWS,
		ClusterProfileQuayAWS,
		ClusterProfileAWSEdgeInfra,
		ClusterProfileODFAWS,
		ClusterProfileAWSObservabiltity,
		ClusterProfileAWSRHOAIQE,
		ClusterProfileAWSManagedRosaRHOAIQE,
		ClusterProfileAWSQUAYQE,
		ClusterProfileAWSMCOQE,
		ClusterProfileAWSManagedOSDRHOAIQE,
		ClusterProfileAWSOADPQE,
		ClusterProfileAWSConfidentialQE,
		ClusterProfileAWSlpChaos,
		ClusterProfileAWSOSPQE,
		ClusterProfileAWSOSC:
		return string(CloudAWS)
	case
		ClusterProfileAlibabaCloud,
		ClusterProfileAlibabaCloudQE,
		ClusterProfileAlibabaCloudCNQE:
		return "alibabacloud"
	case ClusterProfileAWSC2SQE:
		return "aws-c2s"
	case ClusterProfileAWSChinaQE:
		return "aws-china"
	case ClusterProfileAWSGovCloudQE:
		return "aws-usgov"
	case ClusterProfileAWSSC2SQE:
		return "aws-sc2s"
	case ClusterProfileAWSEUSC:
		return "aws-eusc"
	case ClusterProfileAWSOSDMSP:
		return "aws-osd-msp"
	case
		ClusterProfileAzure2,
		ClusterProfileAzure4,
		ClusterProfileOpenshiftOrgAzure,
		ClusterProfileAzureQE,
		ClusterProfileAzureObservability,
		ClusterProfileAzureHCPQE,
		ClusterProfileAzureHCPHAQE,
		ClusterProfileAzureAutoreleaseQE,
		ClusterProfileAzurePerfScale,
		ClusterProfileAzureSustAutoRel412,
		ClusterProfileAzureQUAYQE,
		ClusterProfileAzureConfidentialQE,
		ClusterProfileAzurePerfScaleQE,
		ClusterProfileAzureCNVDevOps,
		ClusterProfileAzureVirtualization,
		ClusterProfileAzureOADPQE:
		return "azure4"
	case
		ClusterProfileAzureArm64,
		ClusterProfileAzureArm64QE:
		return "azure-arm64"
	case
		ClusterProfileAzureStack,
		ClusterProfileAzureStackDEV,
		ClusterProfileAzureStackQE:
		return "azurestack"
	case
		ClusterProfileAzureMag,
		ClusterProfileAzureMagQE:
		return "azuremag"
	case
		ClusterProfileEquinixOcpMetal,
		ClusterProfileEquinixOcpMetalQE,
		ClusterProfileEquinixOcpHCP:
		return "equinix-ocp-metal"
	case
		ClusterProfileGCPQE,
		ClusterProfileGCPQEC3Metal,
		ClusterProfileGCPAutoReleaseQE,
		ClusterProfileGCPArm64,
		ClusterProfileGCP,
		ClusterProfileGCP3,
		ClusterProfileGCP2,
		ClusterProfileGCPVirtualization,
		ClusterProfileGCPSustAutoRel412,
		ClusterProfileGCPObservability,
		ClusterProfileGCPOpendatahub,
		ClusterProfileGCPTelco,
		ClusterProfileGCPChaos,
		ClusterProfileGCPConfidentialQE,
		ClusterProfileGCPPerfScaleQE,
		ClusterProfileGCPQUAYQE,
		ClusterProfileOSLGCP,
		ClusterProfileOpenshiftOrgGCP:
		return string(CloudGCP)
	case
		ClusterProfileIBMCloud,
		ClusterProfileIBMCloudCSPIQE,
		ClusterProfileIBMCloudQE,
		ClusterProfileIBMCloudQE2,
		ClusterProfileIBMCloudRHOAIQE:
		return "ibmcloud"
	case ClusterProfileIBMCloudMultiPpc64le:
		return "ibmcloud-multi-ppc64le"
	case ClusterProfileIBMCloudMultiS390x:
		return "ibmcloud-multi-s390x"
	case ClusterProfilePOWERVC1:
		return "powervc-1"
	case ClusterProfilePOWERVSMulti1:
		return "powervs-multi-1"
	case ClusterProfilePOWERVS1:
		return "powervs-1"
	case ClusterProfilePOWERVS2:
		return "powervs-2"
	case ClusterProfilePOWERVS3:
		return "powervs-3"
	case ClusterProfilePOWERVS4:
		return "powervs-4"
	case ClusterProfilePOWERVS5:
		return "powervs-5"
	case ClusterProfilePOWERVS6:
		return "powervs-6"
	case ClusterProfilePOWERVS7:
		return "powervs-7"
	case ClusterProfilePOWERVS8:
		return "powervs-8"
	case ClusterProfilePOWERVS9:
		return "powervs-9"
	case ClusterProfileLibvirtPpc64le:
		return "libvirt-ppc64le"
	case ClusterProfileLibvirtPpc64leS2S:
		return "libvirt-ppc64le-s2s"
	case ClusterProfileLibvirtS390x:
		return "libvirt-s390x"
	case ClusterProfileLibvirtS390x1:
		return "libvirt-s390x-1"
	case ClusterProfileLibvirtS390x2:
		return "libvirt-s390x-2"
	case ClusterProfileLibvirtS390xAmd64:
		return "libvirt-s390x-amd64"
	case ClusterProfileLibvirtS390xVPN:
		return "libvirt-s390x-vpn"
	case ClusterProfileLibvirtS390xVPNOZ:
		return "libvirt-s390x-vpn-oz"
	case ClusterProfileLibvirtS390xVPNVirt:
		return "libvirt-s390x-vpn-virt"
	case ClusterProfileMetalRHgs:
		return "metal-redhat-gs"
	case ClusterProfileMetalPerfscaleBMCPT:
		return "metal-perfscale-cpt"
	case ClusterProfileMetalPerfscaleJetlag:
		return "metal-perfscale-jetlag"
	case ClusterProfileMetalPerfscaleOSP:
		return "metal-perfscale-osp"
	case ClusterProfileMetalPerfscaleOspNfv:
		return "metal-perfscale-osp-nfv"
	case ClusterProfileMetalPerfscaleOspSelfSched:
		return "metal-perfscale-osp-selfsched"
	case ClusterProfileMetalPerfscaleSelfSched:
		return "metal-perfscale-selfsched"
	case ClusterProfileMetalPerfscaleTelco:
		return "metal-perfscale-telco"
	case
		ClusterProfileNutanix,
		ClusterProfileNutanixQE,
		ClusterProfileNutanixQEDis,
		ClusterProfileNutanixQEZone,
		ClusterProfileNutanixQEGPU,
		ClusterProfileNutanixQEFlow:
		return "nutanix"
	case ClusterProfileOpenStackHwoffload:
		return "openstack-hwoffload"
	case ClusterProfileOpenStackIBMOSP:
		return "openstack-ibm-osp"
	case ClusterProfileOpenStackNFV:
		return "openstack-nfv"
	case ClusterProfileOpenStackMechaCentral:
		return "openstack-vh-mecha-central"
	case ClusterProfileOpenStackOsuosl:
		return "openstack-osuosl"
	case ClusterProfileOpenStackVexxhost:
		return "openstack-vexxhost"
	case ClusterProfileOpenStackVexxhostRHOS:
		return "openstack-vh-bm-rhos"
	case ClusterProfileOpenStackPpc64le:
		return "openstack-ppc64le"
	case ClusterProfileOpenStackOpVexxhost:
		return "openstack-operators-vexxhost"
	case ClusterProfileOpenStackNercDev:
		return "openstack-nerc-dev"
	case ClusterProfileOpenStackRHOSO:
		return "openstack-rhoso"
	case ClusterProfileOpenStackRHOSCI:
		return "openstack-rhos-ci"
	case
		ClusterProfileVSphereMultizone2,
		ClusterProfileVSphereDis2,
		ClusterProfileVSphereElastic,
		ClusterProfileVSphereConnected2,
		ClusterProfileVSphereElasticPoc:

		return "vsphere"
	case ClusterProfileOvirt:
		return "ovirt"
	case
		ClusterProfilePacket:
		return "packet"
	case
		ClusterProfilePacketAssisted,
		ClusterProfilePacketSNO,
		ClusterProfilePacketOSAC:
		return "packet-edge"
	case ClusterProfileKubevirt:
		return "kubevirt"
	case ClusterProfileOSDEphemeral:
		return "osd-ephemeral"
	case ClusterProfileHyperfleetE2E:
		return "hyperfleet-e2e"
	case ClusterProfileOCIAgent:
		return "oci-agent-qe"
	case ClusterProfileOCIAssisted:
		return "oci-edge"
	case ClusterProfileHypershiftAWS:
		return "hypershift-aws"
	case ClusterProfileHypershiftAzure:
		return "hypershift-azure"
	case ClusterProfileHypershiftAKS:
		return "hypershift-aks"
	case ClusterProfileHypershiftPowerVS:
		return "hypershift-powervs"
	case ClusterProfileHypershiftPowerVSCB:
		return "hypershift-powervs-cb"
	case ClusterProfileHypershiftGCP:
		return "hypershift-gcp"
	case ClusterProfileRHOpenShiftEcosystem:
		return string(CloudAWS)
	case ClusterProfileAroRH:
		return "aro"
	case ClusterProfileEquinixEdgeEnablement:
		return "equinix-edge-enablement"

	case ClusterProfileRosaRegionalPlatformInt:
		return "rosa-regional-platform-int"

	case ClusterProfileAROHCPInt:
		return "aro-hcp-int"
	case ClusterProfileAROHCPStg:
		return "aro-hcp-stg"
	case ClusterProfileAROHCPProd:
		return "aro-hcp-prod"
	case ClusterProfileAROHCPDev:
		return "aro-hcp-dev"

	case ClusterProfileAROClassicInt:
		return "aro-classic-int"
	case ClusterProfileAROClassicStg:
		return "aro-classic-stg"
	case ClusterProfileAROClassicProd:
		return "aro-classic-prod"
	case ClusterProfileAROClassicDev:
		return "aro-classic-dev"

	default:
		return ""
	}
}

// LeaseType maps profiles to the type string used in leases.
func (p ClusterProfile) LeaseType() string {
	switch p {
	case
		ClusterProfileAWS:
		return "aws-quota-slice"
	case
		ClusterProfileAWSUSEast1:
		return "aws-us-east-1-quota-slice"
	case ClusterProfileAWSQE:
		return "aws-qe-quota-slice"
	case ClusterProfileAWS1QE:
		return "aws-1-qe-quota-slice"
	case ClusterProfileAWSAutoreleaseQE:
		return "aws-autorelease-qe-quota-slice"
	case ClusterProfileAWSSdQE:
		return "aws-sd-qe-quota-slice"
	case ClusterProfileOEXAWSQE:
		return "oex-aws-qe-quota-slice"
	case ClusterProfileHyperfleetE2E:
		return "hyperfleet-e2e-quota-slice"
	case ClusterProfileAWSOutpostQE:
		return "aws-outpost-qe-quota-slice"
	case ClusterProfileAWSC2SQE:
		return "aws-c2s-qe-quota-slice"
	case ClusterProfileAWSChinaQE:
		return "aws-china-qe-quota-slice"
	case ClusterProfileAWSCSPIQE:
		return "aws-cspi-qe-quota-slice"
	case ClusterProfileAWSChaos:
		return "aws-chaos-quota-slice"
	case ClusterProfileAWSPerfScale:
		return "aws-perfscale-quota-slice"
	case ClusterProfileAWSPerfScaleOKD:
		return "aws-perfscale-okd-quota-slice"
	case ClusterProfileAWSPerfScaleQE:
		return "aws-perfscale-qe-quota-slice"
	case ClusterProfileAWSPerfScaleLRCQE:
		return "aws-perfscale-lrc-qe-quota-slice"
	case ClusterProfileAWSRestrictedQE:
		return "aws-restricted-qe"
	case ClusterProfileROSAE2E01:
		return "rosa-e2e-01-quota-slice"
	case ClusterProfileROSAE2E02:
		return "rosa-e2e-02-quota-slice"
	case ClusterProfileROSAE2E03:
		return "rosa-e2e-03-quota-slice"
	case ClusterProfileAWSEUSC:
		return "aws-eusc-quota-slice"
	case ClusterProfileAWSManagedCSPIQE:
		return "aws-managed-cspi-qe-quota-slice"
	case ClusterProfileAWSGovCloudQE:
		return "aws-usgov-qe-quota-slice"
	case ClusterProfileAWSSC2SQE:
		return "aws-sc2s-qe-quota-slice"
	case ClusterProfileAWSSustAutoRel412:
		return "aws-sustaining-autorelease-412-quota-slice"
	case ClusterProfileAWSINTEROPQE:
		return "aws-interop-qe-quota-slice"
	case ClusterProfileAWSVirtualization:
		return "aws-virtualization-quota-slice"
	case ClusterProfileAWSTerraformQE:
		return "aws-terraform-qe-quota-slice"
	case ClusterProfileAWSPipelinesPerf:
		return "aws-pipelines-performance-quota-slice"
	case ClusterProfileAWSRHTAPQE:
		return "aws-rhtap-qe-quota-slice"
	case ClusterProfileAWSKonfluxQE:
		return "aws-konflux-qe-quota-slice"
	case ClusterProfileAWSRHTAPPerformance:
		return "aws-rhtap-performance-quota-slice"
	case ClusterProfileAWSRHDHPerf:
		return "aws-rhdh-performance-quota-slice"
	case ClusterProfileAWSRHDHDisconnected:
		return "aws-rhdh-disconnected-quota-slice"
	case ClusterProfileAWSServerless:
		return "aws-serverless-quota-slice"
	case ClusterProfileAWSStackrox:
		return "aws-stackrox-quota-slice"
	case ClusterProfileAWSTelco:
		return "aws-telco-quota-slice"
	case ClusterProfileAWSOpendatahub:
		return "aws-opendatahub-quota-slice"
	case ClusterProfileAWSDevfile:
		return "aws-devfile-quota-slice"
	case ClusterProfileAWSKubeVirt:
		return "aws-kubevirt-quota-slice"
	case ClusterProfileAWSRHOAIQE:
		return "aws-rhoai-qe-quota-slice"
	case ClusterProfileAWSOVNPerfScale:
		return "aws-ovn-perfscale-quota-slice"
	case ClusterProfileAlibabaCloud:
		return "alibabacloud-quota-slice"
	case ClusterProfileAlibabaCloudQE:
		return "alibabacloud-qe-quota-slice"
	case ClusterProfileAlibabaCloudCNQE:
		return "alibabacloud-cn-qe-quota-slice"
	case ClusterProfileAzure2:
		return "azure-2-quota-slice"
	case ClusterProfileAzure4:
		return "azure4-quota-slice"
	case ClusterProfileAzureArm64:
		return "azure-arm64-quota-slice"
	case ClusterProfileAzurePerfScale:
		return "azure-perfscale-quota-slice"
	case ClusterProfileAzureStack:
		return "azurestack-quota-slice"
	case ClusterProfileAzureStackDEV:
		return "azurestack-dev-quota-slice"
	case ClusterProfileAzureStackQE:
		return "azurestack-qe-quota-slice"
	case ClusterProfileAWSOSDMSP:
		return "aws-osd-msp-quota-slice"
	case ClusterProfileAzureMag:
		return "azuremag-quota-slice"
	case ClusterProfileAzureQE:
		return "azure-qe-quota-slice"
	case ClusterProfileAzureObservability:
		return "azure-observability-quota-slice"
	case ClusterProfileAzureHCPQE:
		return "azure-hcp-qe-quota-slice"
	case ClusterProfileAzureHCPHAQE:
		return "azure-hcp-ha-qe-quota-slice"
	case ClusterProfileAzureAutoreleaseQE:
		return "azure-autorelease-qe-quota-slice"
	case ClusterProfileAzureMagQE:
		return "azuremag-qe-quota-slice"
	case ClusterProfileAzureArm64QE:
		return "azure-arm64-qe-quota-slice"
	case ClusterProfileAzureVirtualization:
		return "azure-virtualization-quota-slice"
	case ClusterProfileAzureSustAutoRel412:
		return "azure-sustaining-autorelease-412-quota-slice"
	case ClusterProfileAzureConfidentialQE:
		return "azure-confidential-qe-quota-slice"
	case ClusterProfileAzurePerfScaleQE:
		return "azure-perfscale-qe-quota-slice"
	case ClusterProfileAzureCNVDevOps:
		return "azure-cnv-devops-quota-slice"
	case ClusterProfileEquinixOcpMetal:
		return "equinix-ocp-metal-quota-slice"
	case ClusterProfileEquinixOcpMetalQE:
		return "equinix-ocp-metal-qe-quota-slice"
	case ClusterProfileEquinixOcpHCP:
		return "equinix-ocp-hcp-quota-slice"
	case ClusterProfileFleetManagerQE:
		return "fleet-manager-qe-quota-slice"
	case ClusterProfileGCPQE:
		return "gcp-qe-quota-slice"
	case ClusterProfileGCPQEC3Metal:
		return "gcp-qe-c3-metal-quota-slice"
	case ClusterProfileGCPAutoReleaseQE:
		return "gcp-autorelease-qe-quota-slice"
	case ClusterProfileGCPArm64:
		return "gcp-arm64-quota-slice"
	case
		ClusterProfileGCP:
		return "gcp-quota-slice"
	case ClusterProfileGCP2:
		return "gcp-openshift-gce-devel-ci-2-quota-slice"
	case ClusterProfileGCP3:
		return "gcp-3-quota-slice"
	case ClusterProfileGCPVirtualization:
		return "gcp-virtualization-quota-slice"
	case ClusterProfileGCPOpendatahub:
		return "gcp-opendatahub-quota-slice"
	case ClusterProfileGCPTelco:
		return "gcp-telco-quota-slice"
	case ClusterProfileGCPSustAutoRel412:
		return "gcp-sustaining-autorelease-412-quota-slice"
	case ClusterProfileGCPChaos:
		return "gcp-chaos-quota-slice"
	case ClusterProfileGCPConfidentialQE:
		return "gcp-confidential-qe-slice"
	case ClusterProfileGCPPerfScaleQE:
		return "gcp-perfscale-qe-quota-slice"
	case ClusterProfileIBMCloud:
		return "ibmcloud-quota-slice"
	case ClusterProfileIBMCloudCSPIQE:
		return "ibmcloud-cspi-qe-quota-slice"
	case ClusterProfileIBMCloudQE:
		return "ibmcloud-qe-quota-slice"
	case ClusterProfileIBMCloudQE2:
		return "ibmcloud-qe-2-quota-slice"
	case ClusterProfileIBMCloudMultiPpc64le:
		return "ibmcloud-multi-ppc64le-quota-slice"
	case ClusterProfileIBMCloudMultiS390x:
		return "ibmcloud-multi-s390x-quota-slice"
	case ClusterProfilePOWERVC1:
		return "powervc-1-quota-slice"
	case ClusterProfilePOWERVSMulti1:
		return "powervs-multi-1-quota-slice"
	case ClusterProfilePOWERVS1:
		return "powervs-1-quota-slice"
	case ClusterProfilePOWERVS2:
		return "powervs-2-quota-slice"
	case ClusterProfilePOWERVS3:
		return "powervs-3-quota-slice"
	case ClusterProfilePOWERVS4:
		return "powervs-4-quota-slice"
	case ClusterProfilePOWERVS5:
		return "powervs-5-quota-slice"
	case ClusterProfilePOWERVS6:
		return "powervs-6-quota-slice"
	case ClusterProfilePOWERVS7:
		return "powervs-7-quota-slice"
	case ClusterProfilePOWERVS8:
		return "powervs-8-quota-slice"
	case ClusterProfilePOWERVS9:
		return "powervs-9-quota-slice"
	case ClusterProfileLibvirtPpc64le:
		return "libvirt-ppc64le-quota-slice"
	case ClusterProfileLibvirtPpc64leS2S:
		return "libvirt-ppc64le-s2s-quota-slice"
	case ClusterProfileLibvirtS390x:
		return "libvirt-s390x-quota-slice"
	case ClusterProfileLibvirtS390x1:
		return "libvirt-s390x-1-quota-slice"
	case ClusterProfileLibvirtS390x2:
		return "libvirt-s390x-2-quota-slice"
	case ClusterProfileLibvirtS390xAmd64:
		return "libvirt-s390x-amd64-quota-slice"
	case ClusterProfileLibvirtS390xVPN:
		return "libvirt-s390x-vpn-quota-slice"
	case ClusterProfileLibvirtS390xVPNOZ:
		return "libvirt-s390x-vpn-oz-quota-slice"
	case ClusterProfileLibvirtS390xVPNVirt:
		return "libvirt-s390x-vpn-virt-quota-slice"
	case ClusterProfileMetalPerfscaleBMCPT:
		return "metal-perfscale-cpt-quota-slice"
	case ClusterProfileMetalPerfscaleJetlag:
		return "metal-perfscale-jetlag-quota-slice"
	case ClusterProfileMetalPerfscaleOSP:
		return "metal-perfscale-osp-quota-slice"
	case ClusterProfileMetalPerfscaleOspNfv:
		return "metal-perfscale-osp-nfv-quota-slice"
	case ClusterProfileMetalPerfscaleOspSelfSched:
		return "metal-perfscale-osp-selfsched-quota-slice"
	case ClusterProfileMetalPerfscaleSelfSched:
		return "metal-perfscale-selfsched-quota-slice"
	case ClusterProfileMetalPerfscaleTelco:
		return "metal-perfscale-telco-quota-slice"
	case ClusterProfileNutanix:
		return "nutanix-quota-slice"
	case ClusterProfileNutanixQE:
		return "nutanix-qe-quota-slice"
	case ClusterProfileNutanixQEDis:
		return "nutanix-qe-dis-quota-slice"
	case ClusterProfileNutanixQEZone:
		return "nutanix-qe-zone-quota-slice"
	case ClusterProfileNutanixQEGPU:
		return "nutanix-qe-gpu-quota-slice"
	case ClusterProfileNutanixQEFlow:
		return "nutanix-qe-flow-quota-slice"
	case ClusterProfileOpenStackHwoffload:
		return "openstack-hwoffload-quota-slice"
	case ClusterProfileOpenStackIBMOSP:
		return "openstack-ibm-osp-quota-slice"
	case ClusterProfileOpenStackNFV:
		return "openstack-nfv-quota-slice"
	case ClusterProfileOpenStackMechaCentral:
		return "openstack-vh-mecha-central-quota-slice"
	case ClusterProfileOpenStackNercDev:
		return "openstack-nerc-dev-quota-slice"
	case ClusterProfileOpenStackRHOSO:
		return "openstack-rhoso-quota-slice"
	case ClusterProfileOpenStackRHOSCI:
		return "openstack-rhos-ci-quota-slice"
	case ClusterProfileOpenStackOsuosl:
		return "openstack-osuosl-quota-slice"
	case ClusterProfileOpenStackVexxhost:
		return "openstack-vexxhost-quota-slice"
	case ClusterProfileOpenStackVexxhostRHOS:
		return "openstack-vh-bm-rhos-quota-slice"
	case ClusterProfileOpenStackPpc64le:
		return "openstack-ppc64le-quota-slice"
	case ClusterProfileOpenStackOpVexxhost:
		return "openstack-operators-vexxhost-quota-slice"
	case ClusterProfileOvirt:
		return "ovirt-quota-slice"
	case ClusterProfilePacket:
		return "packet-quota-slice"
	case
		ClusterProfilePacketAssisted,
		ClusterProfilePacketSNO,
		ClusterProfilePacketOSAC:
		return "packet-edge-quota-slice"
	case ClusterProfileVSphereDis2:
		return "vsphere-dis-2-quota-slice"
	case ClusterProfileVSphereMultizone2:
		return "vsphere-multizone-2-quota-slice"
	case ClusterProfileVSphereConnected2:
		return "vsphere-connected-2-quota-slice"
	case ClusterProfileVSphereElasticPoc:
		return "vsphere-elastic-poc-quota-slice"
	case ClusterProfileVSphereElastic:
		return "vsphere-elastic-quota-slice"
	case ClusterProfileKubevirt:
		return "kubevirt-quota-slice"
	case ClusterProfileAWSCPaaS:
		return "aws-cpaas-quota-slice"
	case ClusterProfileOSDEphemeral:
		return "osd-ephemeral-quota-slice"
	case ClusterProfileAWS2:
		return "aws-2-quota-slice"
	case ClusterProfileAWS3:
		return "aws-3-quota-slice"
	case ClusterProfileAWS4:
		return "aws-4-quota-slice"
	case ClusterProfileAWS5:
		return "aws-5-quota-slice"
	case ClusterProfileOpenshiftOrgAWS:
		return "openshift-org-aws-quota-slice"
	case ClusterProfileOpenshiftOrgAzure:
		return "openshift-org-azure-quota-slice"
	case ClusterProfileOpenshiftOrgGCP:
		return "openshift-org-gcp-quota-slice"
	case ClusterProfileOCIAgent:
		return "oci-agent-qe-quota-slice"
	case ClusterProfileOCIAssisted:
		return "oci-edge-quota-slice"
	case ClusterProfileHypershiftAWS:
		return "hypershift-aws-quota-slice"
	case ClusterProfileHypershiftAzure:
		return "hypershift-azure-quota-slice"
	case ClusterProfileHypershiftAKS:
		return "hypershift-aks-quota-slice"
	case ClusterProfileHypershiftPowerVS:
		return "hypershift-powervs-quota-slice"
	case ClusterProfileHypershiftPowerVSCB:
		return "hypershift-powervs-cb-quota-slice"
	case ClusterProfileHypershiftGCP:
		return "hypershift-gcp-quota-slice"
	case ClusterProfileOSSM:
		return "ossm-aws-quota-slice"
	case ClusterProfileAWSConfidentialQE:
		return "aws-confidential-qe-quota-slice"
	case ClusterProfileMedik8sAWS:
		return "medik8s-aws-quota-slice"
	case ClusterProfileGitOpsAWS:
		return "gitops-aws-quota-slice"
	case ClusterProfileCheAWS:
		return "che-aws-quota-slice"
	case ClusterProfileOSLGCP:
		return "osl-gcp-quota-slice"
	case ClusterProfileDevSandboxCIAWS:
		return "devsandboxci-aws-quota-slice"
	case ClusterProfileQuayAWS:
		return "quay-aws-quota-slice"
	case ClusterProfileAWSQUAYQE:
		return "aws-quay-qe-quota-slice"
	case ClusterProfileGCPQUAYQE:
		return "gcp-quay-qe-quota-slice"
	case ClusterProfileAzureQUAYQE:
		return "azure-quay-qe-quota-slice"
	case ClusterProfileAWSEdgeInfra:
		return "aws-edge-infra-quota-slice"
	case ClusterProfileRHOpenShiftEcosystem:
		return "rh-openshift-ecosystem-quota-slice"
	case ClusterProfileODFAWS:
		return "odf-aws-quota-slice"
	case ClusterProfileAWSObservabiltity:
		return "aws-observability-quota-slice"
	case ClusterProfileAroRH:
		return "aro-redhat-tenant-quota-slice"
	case ClusterProfileAWSManagedRosaRHOAIQE:
		return "aws-managed-rosa-rhoai-qe-quota-slice"
	case ClusterProfileAWSManagedOSDRHOAIQE:
		return "aws-managed-osd-rhoai-qe-quota-slice"
	case ClusterProfileIBMCloudRHOAIQE:
		return "ibmcloud-rhoai-qe-quota-slice"
	case ClusterProfileGCPObservability:
		return "gcp-observability-quota-slice"
	case ClusterProfileEquinixEdgeEnablement:
		return "equinix-edge-enablement-quota-slice"
	case ClusterProfileAWSMCOQE:
		return "aws-mco-qe-quota-slice"
	case ClusterProfileAWSOADPQE:
		return "aws-oadp-qe-quota-slice"
	case ClusterProfileAzureOADPQE:
		return "azure-oadp-qe-quota-slice"
	case ClusterProfileAWSlpChaos:
		return "aws-lp-chaos-quota-slice"
	case ClusterProfileMetalRHgs:
		return "metal-redhat-gs-quota-slice"
	case ClusterProfileAWSOSPQE:
		return "aws-osp-qe-quota-slice"
	case ClusterProfileAWSOSC:
		return "aws-sandboxed-containers-operator-quota-slice"
	case ClusterProfileRosaRegionalPlatformInt:
		return "rosa-regional-platform-int-quota-slice"

	case ClusterProfileAROHCPInt:
		return "aro-hcp-int-quota-slice"
	case ClusterProfileAROHCPStg:
		return "aro-hcp-stg-quota-slice"
	case ClusterProfileAROHCPProd:
		return "aro-hcp-prod-quota-slice"
	case ClusterProfileAROHCPDev:
		return "aro-hcp-dev-quota-slice"

	case ClusterProfileAROClassicInt:
		return "aro-classic-int-quota-slice"
	case ClusterProfileAROClassicStg:
		return "aro-classic-stg-quota-slice"
	case ClusterProfileAROClassicProd:
		return "aro-classic-prod-quota-slice"
	case ClusterProfileAROClassicDev:
		return "aro-classic-dev-quota-slice"

	default:
		return ""
	}
}

// GetDefaultClusterProfileSecretName returns the default secret name for the profile
func GetDefaultClusterProfileSecretName(profile ClusterProfile) string {
	return fmt.Sprintf("cluster-secrets-%s", string(profile))
}

// LeaseTypeFromClusterType maps cluster types to lease types
func LeaseTypeFromClusterType(t string) (string, error) {
	switch t {
	case
		"aws", "aws-us-east-1", "aws-c2s", "aws-china", "aws-usgov", "aws-sc2s", "aws-eusc", "aws-osd-msp", "aws-opendatahub",
		"alibaba", "azure-2", "azure4", "azure-arm64", "azurestack", "azuremag", "equinix-ocp-metal",
		"gcp", "gcp-arm64", "gcp-opendatahub", "libvirt-ppc64le", "libvirt-ppc64le-s2s", "libvirt-s390x",
		"libvirt-s390x-1", "libvirt-s390x-2", "libvirt-s390x-amd64", "libvirt-s390x-vpn", "libvirt-s390x-vpn-oz", "libvirt-s390x-vpn-virt", "ibmcloud-multi-ppc64le",
		"ibmcloud-multi-s390x", "nutanix", "nutanix-qe", "nutanix-qe-dis", "nutanix-qe-zone", "nutanix-qe-gpu",
		"nutanix-qe-flow", "openstack", "openstack-osuosl", "openstack-vexxhost", "openstack-ppc64le",
		"openstack-nerc-dev", "vsphere", "ovirt", "packet", "packet-edge", "powervc-1", "powervs-multi-1",
		"powervs-1", "powervs-2", "powervs-3", "powervs-4", "powervs-5", "powervs-6", "powervs-7", "powervs-8", "powervs-9",
		"kubevirt", "aws-cpaas", "osd-ephemeral", "gcp-virtualization", "aws-virtualization",
		"azure-virtualization", "hypershift-aws", "hypershift-aks", "hypershift-azure",
		"hypershift-powervs", "hypershift-powervs-cb", "hypershift-gcp", "aws-mco-qe",
		"equinix-edge-enablement", "aws-oadp-qe", "azure-oadp-qe", "aws-lp-chaos", "aws-osp-qe",
		"metal-redhat-gs", "aro-hcp-int", "aro-hcp-stg", "aro-hcp-prod", "aro-hcp-dev", "rosa-regional-platform-int", "hyperfleet-e2e",
		"aro-classic-int", "aro-classic-stg", "aro-classic-prod", "aro-classic-dev", "rosa-e2e-01", "rosa-e2e-02", "rosa-e2e-03":
		return t + "-quota-slice", nil
	default:
		return "", fmt.Errorf("invalid cluster type %q", t)
	}
}

func ClusterProfileFromParams(params Parameters) (*ClusterProfileDetails, error) {
	return GetParamTyped[*ClusterProfileDetails](params, ClusterProfileDetailsParam)
}

type ClusterProfileKonfluxConfig struct {
	ClusterGroups map[string][]string `yaml:"cluster_groups,omitempty" json:"cluster_groups,omitempty"`
}

type ClusterProfilesList struct {
	KonfluxConfig   *ClusterProfileKonfluxConfig `yaml:"konflux,omitempty" json:"konflux,omitempty"`
	ClusterProfiles []ClusterProfileDetails      `yaml:"cluster_profiles,omitempty" json:"cluster_profiles,omitempty"`
}

func (cpl *ClusterProfilesList) Resolve() error {
	errs := make([]error, 0)

	clusterGroups := make(map[string][]string)
	if cpl.KonfluxConfig != nil {
		clusterGroups = cpl.KonfluxConfig.ClusterGroups
	}

	for i := range cpl.ClusterProfiles {
		profile := &cpl.ClusterProfiles[i]

	ownersLoop:
		for j := range profile.Owners {
			owner := &profile.Owners[j]
			if owner.Konflux == nil {
				continue ownersLoop
			}

			allClusters := sets.New(owner.Konflux.Clusters...)

		clusterGroupsLoop:
			for _, clusterGroupName := range owner.Konflux.ClusterGroups {
				clusters, ok := clusterGroups[clusterGroupName]
				if !ok {
					err := fmt.Errorf("profiles[%d].owners[%d] cluster group %s not found", i, j, clusterGroupName)
					errs = append(errs, err)
					continue clusterGroupsLoop
				}
				allClusters.Insert(clusters...)
			}

			if allClusters.Len() > 0 {
				owner.Konflux.Clusters = allClusters.UnsortedList()
				slices.Sort(owner.Konflux.Clusters)
			}
		}
	}

	return aggerrs.NewAggregate(errs)
}

type ClusterProfilesMap map[ClusterProfile]ClusterProfileDetails

type ClusterProfileDetails struct {
	Name            ClusterProfile         `yaml:"name,omitempty" json:"name,omitempty"`
	Owners          []ClusterProfileOwners `yaml:"owners,omitempty" json:"owners,omitempty"`
	ClusterType     string                 `yaml:"cluster_type,omitempty" json:"cluster_type,omitempty"`
	LeaseType       string                 `yaml:"lease_type,omitempty" json:"lease_type,omitempty"`
	IPPoolLeaseType string                 `yaml:"ip_pool_lease_type,omitempty" json:"ip_pool_lease_type,omitempty"`
	Secret          string                 `yaml:"secret,omitempty" json:"secret,omitempty"`
	ConfigMap       string                 `yaml:"config_map,omitempty" json:"config_map,omitempty"`
}

type ClusterProfileKonfluxOwner struct {
	Tenant        string   `yaml:"tenant,omitempty" json:"tenant,omitempty"`
	Clusters      []string `yaml:"clusters,omitempty" json:"clusters,omitempty"`
	ClusterGroups []string `yaml:"cluster_groups,omitempty" json:"cluster_groups,omitempty"`
}

type ClusterProfileOwners struct {
	Org     string                      `yaml:"org,omitempty" json:"org,omitempty"`
	Repos   []string                    `yaml:"repos,omitempty" json:"repos,omitempty"`
	Konflux *ClusterProfileKonfluxOwner `yaml:"konflux,omitempty" json:"konflux,omitempty"`
}
type ClusterClaimOwnersMap map[string]ClusterClaimDetails

// TODO: This will replace `ClusterProfileSetDetails` once the migration is complete.
// +kubebuilder:object:generate=false
type ClusterProfileSetDetails struct {
	ClusterProfileSets map[ClusterProfile][]string `json:"cluster_profile_sets,omitempty"`

	// TestsAllowlist holds a list of tests for which we do not enfoce policy
	// regarding the cluster profile sets usage.
	// This deeply nested type match the following pattern:
	//  "org/repo": "branch": "variant": "test"
	TestsAllowlist map[utilregexp.Regexp]map[utilregexp.Regexp]map[utilregexp.Regexp][]utilregexp.Regexp `json:"tests_allowlist,omitempty"`
}

func (cps ClusterProfileSetDetails) FindSetByProfile(profileName string) (ClusterProfile, bool) {
	for cpsName, cpDetails := range cps.ClusterProfileSets {
		if slices.Contains(cpDetails, profileName) {
			return cpsName, true
		}
	}
	return "", false
}

func (cps ClusterProfileSetDetails) IsTestAllowlisted(test string, metadata Metadata) bool {
	if cps.TestsAllowlist == nil {
		return false
	}

	orgRepo, ok := utilregexp.LookupByMatch(cps.TestsAllowlist, metadata.Org+"/"+metadata.Repo)
	if !ok {
		return false
	}

	branch, ok := utilregexp.LookupByMatch(orgRepo, metadata.Branch)
	if !ok {
		return false
	}

	tests, ok := utilregexp.LookupByMatch(branch, metadata.Variant)
	if !ok {
		return false
	}

	for _, t := range tests {
		if t.Pattern.MatchString(test) {
			return true
		}
	}

	return false
}
