package cischedulingwebhook

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

var (
	awsVolumeSizeDefValues = map[string]int{
		clusterinstall.ProwJobsWorkload:  100,
		clusterinstall.BuildsWorkload:    500,
		clusterinstall.LongTestsWorkload: 300,
		clusterinstall.TestsWorkload:     400,
	}
)

type awsProvider struct {
	ec2ClientGetter awstypes.EC2ClientGetter
}

func (ap *awsProvider) GenerateManifests(ctx context.Context, log *logrus.Entry, ci *clusterinstall.ClusterInstall, config *clusterinstall.CISchedulingWebhook) (map[string][]interface{}, error) {
	manifests := make(map[string][]interface{}, 0)
	for _, workload := range util.DefKeys(config.AWS.Workloads, clusterinstall.CIWorkloadDefaults) {
		archToAZ := config.AWS.Workloads[workload]
		for _, arch := range util.DefKeys(archToAZ, []string{types.ArchAMD64, types.ArchAARCH64}) {
			manifest, err := ap.manifests(ctx, log, ci, workload, arch, archToAZ[arch])
			if err != nil {
				return nil, err
			}
			manifests[fmt.Sprintf("ci-%s-worker-%s.yaml", workload, arch)] = manifest
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

func (ap *awsProvider) manifests(ctx context.Context, log *logrus.Entry, ci *clusterinstall.ClusterInstall, workload string, arch string, azs []string) ([]interface{}, error) {
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

	for _, compute := range ci.InstallConfig.Compute {
		computeName := compute.Name
		for _, az := range util.DefSlice(azs, compute.Platform.AWS.Zones) {
			log = log.WithFields(logrus.Fields{"infraId": infraId, "AZ": az})
			name := fmt.Sprintf("%s-ci-%s-%s-%s-%s", infraId, workload, computeName, arch, az)

			subnetId, err := cloudprovideraws.SubnetIdForMachineSet(ctx, client, infraId, az)
			if err != nil {
				return nil, err
			}
			machineSetTemplateSpec := map[string]interface{}{
				"metadata": map[string]interface{}{
					"labels": map[string]interface{}{
						"ci-workload": workload,
					},
				},
				"providerSpec": map[string]interface{}{
					"value": map[string]interface{}{
						"kind": "AWSMachineProviderConfig",
						"metadata": map[string]interface{}{
							"creationTimestamp": nil,
						},
						"placement": map[string]interface{}{
							"availabilityZone": az,
							"region":           region,
						},
						"subnet": map[string]interface{}{
							"id": subnetId,
						},
						"apiVersion": "machine.openshift.io/v1beta1",
						"blockDevices": []interface{}{
							map[string]interface{}{
								"ebs": map[string]interface{}{
									"kmsKey": map[string]interface{}{
										"arn": "",
									},
									"volumeSize": ap.volumeSize(workload),
									"volumeType": "gp3",
									"encrypted":  true,
									"iops":       5000,
								},
							},
						},
						"deviceIndex":    0,
						"instanceType":   instanceType,
						"securityGroups": securityGroups,
						"userDataSecret": map[string]interface{}{
							"name": fmt.Sprintf("%s-user-data", computeName),
						},
						"publicIp": true,
						"tags": []interface{}{
							map[string]interface{}{
								"name":  fmt.Sprintf("kubernetes.io/cluster/%s", infraId),
								"value": "owned",
							},
						},
						"ami": map[string]interface{}{
							"id": ami,
						},
						"credentialsSecret": map[string]interface{}{
							"name": "aws-cloud-credentials",
						},
						"iamInstanceProfile": map[string]interface{}{
							"id": fmt.Sprintf("%s-%s-profile", infraId, computeName),
						},
						"metadataServiceOptions": map[string]interface{}{},
					},
				},
				"taints": []interface{}{
					map[string]interface{}{
						"effect": "NoSchedule",
						"key":    fmt.Sprintf("node-role.kubernetes.io/ci-%s-%s", workload, computeName),
						"value":  fmt.Sprintf("ci-%s-%s", workload, computeName),
					},
				},
			}
			machineSet := map[string]interface{}{
				"kind": "MachineSet",
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
						"metadata": map[string]interface{}{
							"labels": map[string]interface{}{
								"machine.openshift.io/cluster-api-cluster":      infraId,
								"machine.openshift.io/cluster-api-machine-role": computeName,
								"machine.openshift.io/cluster-api-machine-type": computeName,
								"machine.openshift.io/cluster-api-machineset":   name,
							},
						},
						"spec": machineSetTemplateSpec,
					},
				},
				"apiVersion": "machine.openshift.io/v1beta1",
			}
			machineAutoscaler := map[string]interface{}{
				"apiVersion": "autoscaling.openshift.io/v1beta1",
				"kind":       "MachineAutoscaler",
				"metadata": map[string]interface{}{
					"name":      name,
					"namespace": "openshift-machine-api",
				},
				"spec": map[string]interface{}{
					"maxReplicas": 40,
					"minReplicas": 1,
					"scaleTargetRef": map[string]interface{}{
						"apiVersion": "machine.openshift.io/v1beta1",
						"kind":       "MachineSet",
						"name":       name,
					},
				},
			}

			manifests = append(manifests, machineSet, machineAutoscaler)
		}
	}

	return manifests, nil
}

func (ap *awsProvider) volumeSize(workload string) int {
	if size, ok := awsVolumeSizeDefValues[workload]; ok {
		return size
	}
	return 200
}

func NewAWSProvider(ec2ClientGetter awstypes.EC2ClientGetter) *awsProvider {
	return &awsProvider{
		ec2ClientGetter: ec2ClientGetter,
	}
}
