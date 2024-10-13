package cischedulingwebhook

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/openshift/ci-tools/pkg/clusterinit/clusterinstall"
	awstypes "github.com/openshift/ci-tools/pkg/clusterinit/types/aws"
	"github.com/sirupsen/logrus"
	"k8s.io/utils/ptr"
)

type awsProvider struct {
	ec2ClientGetter awstypes.EC2ClientGetter
	subnetsCache    map[string]string
}

func (aw *awsProvider) GenerateManifests(ctx context.Context, log *logrus.Entry, ci *clusterinstall.ClusterInstall, config *clusterinstall.CISchedulingWebhook) (map[string][]interface{}, error) {
	manifests := make(map[string][]interface{}, 0)
	for _, workload := range aw.workloadKeysOrDefault(config.AWS.Workloads) {
		archToAZ := config.AWS.Workloads[string(workload)]
		for _, arch := range aw.archsOrDefault(archToAZ) {
			manifest, err := aw.manifests(ctx, log, ci, string(workload), string(arch), archToAZ[arch])
			if err != nil {
				return nil, err
			}
			manifests[fmt.Sprintf("ci-%s-worker-%s.yaml", workload, arch)] = manifest
		}
	}
	return manifests, nil
}

func (aw *awsProvider) findSubnetId(ctx context.Context, client awstypes.EC2Client, log *logrus.Entry, infraId, az string) (string, error) {
	cacheKey := infraId + "-" + az
	if subnetId, ok := aw.subnetsCache[cacheKey]; ok {
		return subnetId, nil
	}
	paginator := ec2.NewDescribeSubnetsPaginator(client, &ec2.DescribeSubnetsInput{Filters: []ec2types.Filter{
		{Name: ptr.To("availability-zone"), Values: []string{az}},
		{Name: ptr.To("tag-key"), Values: []string{fmt.Sprintf("kubernetes.io/cluster/%s", infraId)}},
	}})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return "", fmt.Errorf("describe subnets %s %s: %w", infraId, az, err)
		}
		if len(page.Subnets) > 0 {
			if len(page.Subnets) > 1 {
				log.Info("more than 1 subnet found, picking the first one")
			}
			if page.Subnets[0].SubnetId == nil {
				return "", fmt.Errorf("subnet %s %s: subnetId is nil", infraId, az)
			}
			aw.subnetsCache[cacheKey] = *page.Subnets[0].SubnetId
			return *page.Subnets[0].SubnetId, nil
		}
	}
	return "", fmt.Errorf("%s %s: no subnet ids found", infraId, az)
}

func (aw *awsProvider) securityGroups(ctx context.Context, client awstypes.EC2Client, infraId string, roles ...string) ([]interface{}, error) {
	paginator := ec2.NewDescribeSecurityGroupsPaginator(client, &ec2.DescribeSecurityGroupsInput{Filters: []ec2types.Filter{
		{Name: ptr.To("tag:sigs.k8s.io/cluster-api-provider-aws/role"), Values: roles},
		{Name: ptr.To("tag-key"), Values: []string{fmt.Sprintf("sigs.k8s.io/cluster-api-provider-aws/cluster/%s", infraId)}},
	}})
	securityGroups := make([]interface{}, 0)
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("describe security groups %s: %w", infraId, err)
		}
		for i := range page.SecurityGroups {
			if page.SecurityGroups[i].GroupName != nil {
				securityGroups = append(securityGroups, map[string]interface{}{
					"filters": []interface{}{
						map[string]interface{}{
							"name":   "tag:Name",
							"values": []interface{}{*page.SecurityGroups[i].GroupName},
						},
					},
				})
			}
		}
	}
	return securityGroups, nil
}

func (aw *awsProvider) manifests(ctx context.Context, log *logrus.Entry, ci *clusterinstall.ClusterInstall, workload, arch string, azs []string) ([]interface{}, error) {
	manifests := make([]interface{}, 0)
	infraId := ci.Infrastructure.Status.InfrastructureName
	region := ci.InstallConfig.Platform.AWS.Region

	ami, err := aw.getAMI(arch)
	if err != nil {
		return nil, err
	}

	instanceType := ec2types.InstanceTypeM6g4xlarge
	if arch == string(clusterinstall.ArchAMD64) {
		instanceType = ec2types.InstanceTypeM6a4xlarge
	}

	client, err := aw.ec2ClientGetter.EC2Client(ctx)
	if err != nil {
		return nil, fmt.Errorf("aws ec2 client: %w", err)
	}
	securityGroups, err := aw.securityGroups(ctx, client, infraId, "node", "lb")
	if err != nil {
		return nil, fmt.Errorf("generate security groups: %w", err)
	}

	for _, compute := range ci.InstallConfig.Compute {
		computeName := compute.Name
		for _, az := range aw.azsOrDefault(azs, compute.Platform.AWS.Zones) {
			log = log.WithFields(logrus.Fields{"infraId": infraId, "AZ": az})
			name := fmt.Sprintf("%s-ci-%s-%s-%s-%s", infraId, workload, computeName, arch, az)

			subnetId, err := aw.findSubnetId(ctx, client, log, infraId, az)
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
									"volumeSize": 800,
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

func (aw *awsProvider) workloadKeysOrDefault(w map[string]awstypes.CISchedulingWebhookArchToAZ) []clusterinstall.CIWorkload {
	if len(w) == 0 {
		return clusterinstall.CIWorkloadDefaults
	}
	keys := make([]clusterinstall.CIWorkload, 0, len(w))
	for k := range w {
		keys = append(keys, clusterinstall.CIWorkload(k))
	}
	return keys
}

func (aw *awsProvider) archsOrDefault(archToAZ awstypes.CISchedulingWebhookArchToAZ) []string {
	if len(archToAZ) == 0 {
		return []string{string(clusterinstall.ArchAMD64), string(clusterinstall.ArchAARCH64)}
	}
	keys := make([]string, 0, len(archToAZ))
	for k := range archToAZ {
		keys = append(keys, k)
	}
	return keys
}

func (aw *awsProvider) azsOrDefault(azs []string, def []string) []string {
	if len(azs) == 0 {
		return def
	}
	return azs
}

func (aw *awsProvider) getAMI(arch string) (string, error) {
	switch arch {
	case string(clusterinstall.ArchAMD64):
		return "ami-0545fae7edbbbf061", nil
	case string(clusterinstall.ArchAARCH64):
		return "ami-0e9cdc0e85e0a6aeb", nil
	default:
		return "", fmt.Errorf("no ami for arch %s", arch)
	}
}

func NewAWSProvider(ec2ClientGetter awstypes.EC2ClientGetter) *awsProvider {
	return &awsProvider{
		ec2ClientGetter: ec2ClientGetter,
		subnetsCache:    make(map[string]string),
	}
}
