package machineset

import (
	"context"
	"fmt"
	"slices"

	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/sirupsen/logrus"

	cloudprovideraws "github.com/openshift/ci-tools/pkg/clusterinit/cloudprovider/aws"
	"github.com/openshift/ci-tools/pkg/clusterinit/clusterinstall"
	"github.com/openshift/ci-tools/pkg/clusterinit/types"
	awstypes "github.com/openshift/ci-tools/pkg/clusterinit/types/aws"
	"github.com/openshift/ci-tools/pkg/util"
)

type awsProvider struct {
	ec2ClientGetter awstypes.EC2ClientGetter
}

func (ap *awsProvider) GenerateManifests(ctx context.Context, log *logrus.Entry, ci *clusterinstall.ClusterInstall) (map[string][]interface{}, error) {
	manifests := make(map[string][]interface{}, 0)
	for _, profileName := range util.DefKeys(ci.Onboard.MachineSet.AWS.Profiles, clusterinstall.MachineProfileDefaults) {
		profile := ci.Onboard.MachineSet.AWS.Profiles[profileName]
		for _, arch := range util.DefKeys(profile.Architectures, []string{types.ArchAMD64, types.ArchAARCH64}) {
			generateAutoscaler := true
			if profile.MachineAutoscaler != nil {
				generateAutoscaler = *profile.MachineAutoscaler
			}
			manifest, err := ap.manifests(ctx, log, ci, generateAutoscaler, profileName, arch, profile.Architectures[arch])
			if err != nil {
				return nil, err
			}
			manifests[fmt.Sprintf("%s-%s.yaml", profileName, arch)] = manifest
		}
	}
	return manifests, nil
}

func (ap *awsProvider) securityGroups(ctx context.Context, client awstypes.EC2Client, infraId string, roles ...string) ([]interface{}, error) {
	sg, err := cloudprovideraws.SecurityGroupNamesForMachineSet(ctx, client, infraId, roles...)
	if err != nil {
		return nil, err
	}

	slices.Sort(sg)

	securityGroups := make([]interface{}, 0)
	for i := range sg {
		securityGroups = append(securityGroups, map[string]interface{}{
			"filters": []interface{}{
				map[string]interface{}{
					"name":   "tag:Name",
					"values": []interface{}{sg[i]},
				},
			},
		})
	}
	return securityGroups, nil
}

func (ap *awsProvider) manifests(ctx context.Context, log *logrus.Entry, ci *clusterinstall.ClusterInstall, generateAutoscaler bool, profile string, arch string, azs []string) ([]interface{}, error) {
	manifests := make([]interface{}, 0)
	infraId := ci.Infrastructure.Status.InfrastructureName
	region := ci.InstallConfig.Platform.AWS.Region

	ami, err := awstypes.FindAMI(ci.CoreOSStream, types.ToCoreOSStreamArch(arch), region)
	if err != nil {
		return nil, err
	}

	instanceType := ec2types.InstanceTypeM6g4xlarge
	if arch == types.ArchAMD64 {
		instanceType = ec2types.InstanceTypeM6a4xlarge
	}

	client, err := ap.ec2ClientGetter.EC2Client(ctx)
	if err != nil {
		return nil, fmt.Errorf("aws ec2 client: %w", err)
	}
	securityGroups, err := ap.securityGroups(ctx, client, infraId, "node", "lb")
	if err != nil {
		return nil, fmt.Errorf("generate security groups: %w", err)
	}

	zones := []string{}
	if len(ci.InstallConfig.Compute) > 0 {
		zones = ci.InstallConfig.Compute[0].Platform.AWS.Zones
	}

	for _, az := range util.DefSlice(azs, zones) {
		log = log.WithFields(logrus.Fields{"infraId": infraId, "AZ": az})
		name := fmt.Sprintf("%s-%s-%s-%s", infraId, profile, arch, az)

		subnetId, err := cloudprovideraws.SubnetIdForMachineSet(ctx, client, infraId, az)
		if err != nil {
			return nil, err
		}

		machineSetTemplateSpec := map[string]interface{}{
			"metadata": map[string]interface{}{
				"labels": map[string]interface{}{
					"node-role.kubernetes.io":            profile,
					"node-role.kubernetes.io/" + profile: "",
				},
			},
			"providerSpec": map[string]interface{}{
				"value": map[string]interface{}{
					"instanceType": instanceType,
					"kind":         "AWSMachineProviderConfig",
					"subnet": map[string]interface{}{
						"id": subnetId,
					},
					"userDataSecret": map[string]interface{}{
						"name": "worker-user-data",
					},
					"apiVersion": "machine.openshift.io/v1beta1",
					"tags": []interface{}{
						map[string]interface{}{
							"name":  "kubernetes.io/cluster/" + infraId,
							"value": "owned",
						},
					},
					"publicIp": true,
					"ami": map[string]interface{}{
						"id": ami,
					},
					"blockDevices": []interface{}{
						map[string]interface{}{
							"ebs": map[string]interface{}{
								"kmsKey": map[string]interface{}{
									"arn": "",
								},
								"volumeSize": 120,
								"volumeType": "gp3",
								"encrypted":  true,
								"iops":       0,
							},
						},
					},
					"credentialsSecret": map[string]interface{}{
						"name": "aws-cloud-credentials",
					},
					"deviceIndex":    0,
					"securityGroups": securityGroups,
					"iamInstanceProfile": map[string]interface{}{
						"id": infraId + "-worker-profile",
					},
					"metadata": map[string]interface{}{
						"creationTimestamp": nil,
					},
					"metadataServiceOptions": map[string]interface{}{},
					"placement": map[string]interface{}{
						"availabilityZone": az,
						"region":           region,
					},
				},
			},
		}
		machineSet := map[string]interface{}{
			"apiVersion": "machine.openshift.io/v1beta1",
			"kind":       "MachineSet",
			"metadata": map[string]interface{}{
				"name":      name,
				"namespace": "openshift-machine-api",
				"labels": map[string]interface{}{
					"machine.openshift.io/cluster-api-cluster": infraId,
				},
			},
			"spec": map[string]interface{}{
				"selector": map[string]interface{}{
					"matchLabels": map[string]interface{}{
						"machine.openshift.io/cluster-api-cluster":    infraId,
						"machine.openshift.io/cluster-api-machineset": name,
					},
				},
				"template": map[string]interface{}{
					"spec": machineSetTemplateSpec,
					"metadata": map[string]interface{}{
						"labels": map[string]interface{}{
							"machine.openshift.io/cluster-api-machine-type": profile,
							"machine.openshift.io/cluster-api-machineset":   name,
							"machine.openshift.io/cluster-api-cluster":      infraId,
							"machine.openshift.io/cluster-api-machine-role": profile,
						},
					},
				},
			},
		}

		manifests = append(manifests, machineSet)

		if generateAutoscaler {
			machineAutoscaler := map[string]interface{}{
				"metadata": map[string]interface{}{
					"name":      name,
					"namespace": "openshift-machine-api",
				},
				"spec": map[string]interface{}{
					"maxReplicas": 5,
					"minReplicas": 0,
					"scaleTargetRef": map[string]interface{}{
						"name":       name,
						"apiVersion": "machine.openshift.io/v1beta1",
						"kind":       "MachineSet",
					},
				},
				"apiVersion": "autoscaling.openshift.io/v1beta1",
				"kind":       "MachineAutoscaler",
			}
			manifests = append(manifests, machineAutoscaler)
		}
	}

	return manifests, nil
}

func NewAWSProvider(ec2ClientGetter awstypes.EC2ClientGetter) *awsProvider {
	return &awsProvider{
		ec2ClientGetter: ec2ClientGetter,
	}
}
