package machineset

import (
	"context"
	"os"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/google/go-cmp/cmp"
	"github.com/sirupsen/logrus"

	"k8s.io/utils/ptr"
	kyaml "sigs.k8s.io/yaml"

	installertypes "github.com/openshift/installer/pkg/types"
	installeraws "github.com/openshift/installer/pkg/types/aws"

	"github.com/openshift/ci-tools/pkg/clusterinit/clusterinstall"
	"github.com/openshift/ci-tools/pkg/clusterinit/types"
	awstypes "github.com/openshift/ci-tools/pkg/clusterinit/types/aws"
)

func TestGenerateManifests(t *testing.T) {
	tests := []struct {
		name           string
		ci             *clusterinstall.ClusterInstall
		subnets        *ec2.DescribeSubnetsOutput
		securityGroups *ec2.DescribeSecurityGroupsOutput
		wantManifests  map[string][]interface{}
		wantErr        error
	}{
		{
			name: "Infra and worker profile in different AZ",
			ci: &clusterinstall.ClusterInstall{
				Onboard: clusterinstall.Onboard{
					MachineSet: clusterinstall.MachineSet{
						AWS: awstypes.MachineSet{
							Profiles: map[string]awstypes.MachineSetProfile{
								"worker": {
									Architectures: map[string][]string{
										types.ArchAMD64: {"us-east-1a"},
									},
									MachineAutoscaler: ptr.To(true),
								},
								"infra": {
									Architectures: map[string][]string{
										types.ArchAMD64: {"us-east-1b"},
									},
								},
							},
						},
					},
				},
				InstallConfig: installertypes.InstallConfig{
					Platform: installertypes.Platform{
						AWS: &installeraws.Platform{
							Region: "us-east-1",
						},
					},
				},
			},
			subnets: &ec2.DescribeSubnetsOutput{
				Subnets: []ec2types.Subnet{
					{SubnetId: ptr.To("subnet-12345"), AvailabilityZone: ptr.To("us-east-1a")},
					{SubnetId: ptr.To("subnet-67890"), AvailabilityZone: ptr.To("us-east-1b")},
				},
			},
			securityGroups: &ec2.DescribeSecurityGroupsOutput{
				SecurityGroups: []ec2types.SecurityGroup{
					{GroupId: ptr.To("sg-12345"), GroupName: ptr.To("test-sg")},
				},
			},
			wantManifests: map[string][]interface{}{
				"worker-amd64.yaml": {
					map[string]interface{}{
						"apiVersion": "machine.openshift.io/v1beta1",
						"kind":       "MachineSet",
						"metadata": map[string]interface{}{
							"labels": map[string]interface{}{
								"machine.openshift.io/cluster-api-cluster": "",
							},
							"name":      "-worker-amd64-us-east-1a",
							"namespace": "openshift-machine-api",
						},
						"spec": map[string]interface{}{
							"selector": map[string]interface{}{
								"matchLabels": map[string]interface{}{
									"machine.openshift.io/cluster-api-cluster":    "",
									"machine.openshift.io/cluster-api-machineset": "-worker-amd64-us-east-1a",
								},
							},
							"template": map[string]interface{}{
								"metadata": map[string]interface{}{
									"labels": map[string]interface{}{
										"machine.openshift.io/cluster-api-cluster":      "",
										"machine.openshift.io/cluster-api-machine-role": "worker",
										"machine.openshift.io/cluster-api-machine-type": "worker",
										"machine.openshift.io/cluster-api-machineset":   "-worker-amd64-us-east-1a",
									},
								},
								"spec": map[string]interface{}{
									"metadata": map[string]interface{}{
										"labels": map[string]interface{}{
											"node-role.kubernetes.io":        "worker",
											"node-role.kubernetes.io/worker": "",
										},
									},
									"providerSpec": map[string]interface{}{
										"value": map[string]interface{}{
											"credentialsSecret": map[string]interface{}{
												"name": "aws-cloud-credentials",
											},
											"metadata": map[string]interface{}{
												"creationTimestamp": nil,
											},
											"metadataServiceOptions": map[string]interface{}{},
											"userDataSecret": map[string]interface{}{
												"name": "worker-user-data",
											},
											"ami": map[string]interface{}{
												"id": "ami-0545fae7edbbbf061",
											},
											"tags": []interface{}{
												map[string]interface{}{
													"name":  "kubernetes.io/cluster/",
													"value": "owned",
												},
											},
											"securityGroups": []interface{}{
												map[string]interface{}{
													"filters": []interface{}{
														map[string]interface{}{
															"name": "tag:Name",
															"values": []interface{}{
																"test-sg",
															},
														},
													},
												},
											},
											"blockDevices": []interface{}{
												map[string]interface{}{
													"ebs": map[string]interface{}{
														"volumeType": "gp3",
														"encrypted":  true,
														"iops":       0,
														"kmsKey": map[string]interface{}{
															"arn": "",
														},
														"volumeSize": 120,
													},
												},
											},
											"deviceIndex": 0,
											"iamInstanceProfile": map[string]interface{}{
												"id": "-worker-profile",
											},
											"instanceType": ec2types.InstanceType("m6a.4xlarge"),
											"kind":         "AWSMachineProviderConfig",
											"publicIp":     true,
											"apiVersion":   "machine.openshift.io/v1beta1",
											"subnet": map[string]interface{}{
												"id": "subnet-12345",
											},
											"placement": map[string]interface{}{
												"availabilityZone": "us-east-1a",
												"region":           "us-east-1",
											},
										},
									},
								},
							},
						},
					},
					map[string]interface{}{
						"apiVersion": "autoscaling.openshift.io/v1beta1",
						"kind":       "MachineAutoscaler",
						"metadata": map[string]interface{}{
							"name":      "-worker-amd64-us-east-1a",
							"namespace": "openshift-machine-api",
						},
						"spec": map[string]interface{}{
							"maxReplicas": 5,
							"minReplicas": 0,
							"scaleTargetRef": map[string]interface{}{
								"apiVersion": "machine.openshift.io/v1beta1",
								"kind":       "MachineSet",
								"name":       "-worker-amd64-us-east-1a",
							},
						},
					},
				},
				"infra-amd64.yaml": {
					map[string]interface{}{
						"apiVersion": "machine.openshift.io/v1beta1",
						"kind":       "MachineSet",
						"metadata": map[string]interface{}{
							"labels": map[string]interface{}{
								"machine.openshift.io/cluster-api-cluster": "",
							},
							"name":      "-infra-amd64-us-east-1b",
							"namespace": "openshift-machine-api",
						},
						"spec": map[string]interface{}{
							"selector": map[string]interface{}{
								"matchLabels": map[string]interface{}{
									"machine.openshift.io/cluster-api-cluster":    "",
									"machine.openshift.io/cluster-api-machineset": "-infra-amd64-us-east-1b",
								},
							},
							"template": map[string]interface{}{
								"metadata": map[string]interface{}{
									"labels": map[string]interface{}{
										"machine.openshift.io/cluster-api-cluster":      "",
										"machine.openshift.io/cluster-api-machine-role": "infra",
										"machine.openshift.io/cluster-api-machine-type": "infra",
										"machine.openshift.io/cluster-api-machineset":   "-infra-amd64-us-east-1b",
									},
								},
								"spec": map[string]interface{}{
									"providerSpec": map[string]interface{}{
										"value": map[string]interface{}{
											"metadataServiceOptions": map[string]interface{}{},
											"placement": map[string]interface{}{
												"availabilityZone": "us-east-1b",
												"region":           "us-east-1",
											},
											"securityGroups": []interface{}{
												map[string]interface{}{
													"filters": []interface{}{
														map[string]interface{}{
															"values": []interface{}{
																"test-sg",
															},
															"name": "tag:Name",
														},
													},
												},
											},
											"subnet": map[string]interface{}{
												"id": "subnet-12345",
											},
											"tags": []interface{}{
												map[string]interface{}{
													"name":  "kubernetes.io/cluster/",
													"value": "owned",
												},
											},
											"metadata": map[string]interface{}{
												"creationTimestamp": nil,
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
											"iamInstanceProfile": map[string]interface{}{
												"id": "-worker-profile",
											},
											"instanceType": ec2types.InstanceType("m6a.4xlarge"),
											"publicIp":     true,
											"userDataSecret": map[string]interface{}{
												"name": "worker-user-data",
											},
											"apiVersion":  "machine.openshift.io/v1beta1",
											"deviceIndex": 0,
											"kind":        "AWSMachineProviderConfig",
											"ami": map[string]interface{}{
												"id": "ami-0545fae7edbbbf061",
											},
										},
									},
									"metadata": map[string]interface{}{
										"labels": map[string]interface{}{
											"node-role.kubernetes.io":       "infra",
											"node-role.kubernetes.io/infra": "",
										},
									},
								},
							},
						},
					},
					map[string]interface{}{
						"kind": "MachineAutoscaler",
						"metadata": map[string]interface{}{
							"name":      "-infra-amd64-us-east-1b",
							"namespace": "openshift-machine-api",
						},
						"spec": map[string]interface{}{
							"maxReplicas": 5,
							"minReplicas": 0,
							"scaleTargetRef": map[string]interface{}{
								"name":       "-infra-amd64-us-east-1b",
								"apiVersion": "machine.openshift.io/v1beta1",
								"kind":       "MachineSet",
							},
						},
						"apiVersion": "autoscaling.openshift.io/v1beta1",
					},
				},
			},
		},
		{
			name: "No config, defaults everything",
			ci: &clusterinstall.ClusterInstall{
				Onboard: clusterinstall.Onboard{
					MachineSet: clusterinstall.MachineSet{
						AWS: awstypes.MachineSet{},
					},
				},
				InstallConfig: installertypes.InstallConfig{
					Compute: []installertypes.MachinePool{
						{Platform: installertypes.MachinePoolPlatform{AWS: &installeraws.MachinePool{Zones: []string{"us-east-1a"}}}},
					},
					Platform: installertypes.Platform{
						AWS: &installeraws.Platform{
							Region: "us-east-1",
						},
					},
				},
			},
			subnets: &ec2.DescribeSubnetsOutput{
				Subnets: []ec2types.Subnet{
					{SubnetId: ptr.To("subnet-12345"), AvailabilityZone: ptr.To("us-east-1a")},
				},
			},
			securityGroups: &ec2.DescribeSecurityGroupsOutput{
				SecurityGroups: []ec2types.SecurityGroup{
					{GroupId: ptr.To("sg-12345"), GroupName: ptr.To("test-sg")},
				},
			},
			wantManifests: map[string][]interface{}{
				"infra-aarch64.yaml": {
					map[string]interface{}{
						"apiVersion": "machine.openshift.io/v1beta1",
						"kind":       "MachineSet",
						"metadata": map[string]interface{}{
							"labels": map[string]interface{}{
								"machine.openshift.io/cluster-api-cluster": "",
							},
							"name":      "-infra-aarch64-us-east-1a",
							"namespace": "openshift-machine-api",
						},
						"spec": map[string]interface{}{
							"selector": map[string]interface{}{
								"matchLabels": map[string]interface{}{
									"machine.openshift.io/cluster-api-cluster":    "",
									"machine.openshift.io/cluster-api-machineset": "-infra-aarch64-us-east-1a",
								},
							},
							"template": map[string]interface{}{
								"metadata": map[string]interface{}{
									"labels": map[string]interface{}{
										"machine.openshift.io/cluster-api-cluster":      "",
										"machine.openshift.io/cluster-api-machine-role": "infra",
										"machine.openshift.io/cluster-api-machine-type": "infra",
										"machine.openshift.io/cluster-api-machineset":   "-infra-aarch64-us-east-1a",
									},
								},
								"spec": map[string]interface{}{
									"metadata": map[string]interface{}{
										"labels": map[string]interface{}{
											"node-role.kubernetes.io":       "infra",
											"node-role.kubernetes.io/infra": "",
										},
									},
									"providerSpec": map[string]interface{}{
										"value": map[string]interface{}{
											"placement": map[string]interface{}{
												"availabilityZone": "us-east-1a",
												"region":           "us-east-1",
											},
											"tags": []interface{}{
												map[string]interface{}{
													"value": "owned",
													"name":  "kubernetes.io/cluster/",
												},
											},
											"apiVersion": "machine.openshift.io/v1beta1",
											"iamInstanceProfile": map[string]interface{}{
												"id": "-worker-profile",
											},
											"instanceType": ec2types.InstanceType("m6g.4xlarge"),
											"deviceIndex":  0,
											"publicIp":     true,
											"userDataSecret": map[string]interface{}{
												"name": "worker-user-data",
											},
											"metadataServiceOptions": map[string]interface{}{},
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
											"kind": "AWSMachineProviderConfig",
											"metadata": map[string]interface{}{
												"creationTimestamp": nil,
											},
											"securityGroups": []interface{}{
												map[string]interface{}{
													"filters": []interface{}{
														map[string]interface{}{
															"name": "tag:Name",
															"values": []interface{}{
																"test-sg",
															},
														},
													},
												},
											},
											"subnet": map[string]interface{}{
												"id": "subnet-12345",
											},
											"ami": map[string]interface{}{
												"id": "ami-0e9cdc0e85e0a6aeb",
											},
										},
									},
								},
							},
						},
					},
					map[string]interface{}{
						"apiVersion": "autoscaling.openshift.io/v1beta1",
						"kind":       "MachineAutoscaler",
						"metadata": map[string]interface{}{
							"name":      "-infra-aarch64-us-east-1a",
							"namespace": "openshift-machine-api",
						},
						"spec": map[string]interface{}{
							"minReplicas": 0,
							"scaleTargetRef": map[string]interface{}{
								"apiVersion": "machine.openshift.io/v1beta1",
								"kind":       "MachineSet",
								"name":       "-infra-aarch64-us-east-1a",
							},
							"maxReplicas": 5,
						},
					},
				},
				"infra-amd64.yaml": {
					map[string]interface{}{
						"apiVersion": "machine.openshift.io/v1beta1",
						"kind":       "MachineSet",
						"metadata": map[string]interface{}{
							"name":      "-infra-amd64-us-east-1a",
							"namespace": "openshift-machine-api",
							"labels": map[string]interface{}{
								"machine.openshift.io/cluster-api-cluster": "",
							},
						},
						"spec": map[string]interface{}{
							"selector": map[string]interface{}{
								"matchLabels": map[string]interface{}{
									"machine.openshift.io/cluster-api-cluster":    "",
									"machine.openshift.io/cluster-api-machineset": "-infra-amd64-us-east-1a",
								},
							},
							"template": map[string]interface{}{
								"spec": map[string]interface{}{
									"metadata": map[string]interface{}{
										"labels": map[string]interface{}{
											"node-role.kubernetes.io/infra": "",
											"node-role.kubernetes.io":       "infra",
										},
									},
									"providerSpec": map[string]interface{}{
										"value": map[string]interface{}{
											"ami": map[string]interface{}{
												"id": "ami-0545fae7edbbbf061",
											},
											"kind": "AWSMachineProviderConfig",
											"metadata": map[string]interface{}{
												"creationTimestamp": nil,
											},
											"userDataSecret": map[string]interface{}{
												"name": "worker-user-data",
											},
											"credentialsSecret": map[string]interface{}{
												"name": "aws-cloud-credentials",
											},
											"securityGroups": []interface{}{
												map[string]interface{}{
													"filters": []interface{}{
														map[string]interface{}{
															"name": "tag:Name",
															"values": []interface{}{
																"test-sg",
															},
														},
													},
												},
											},
											"apiVersion": "machine.openshift.io/v1beta1",
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
											"deviceIndex": 0,
											"iamInstanceProfile": map[string]interface{}{
												"id": "-worker-profile",
											},
											"metadataServiceOptions": map[string]interface{}{},
											"placement": map[string]interface{}{
												"availabilityZone": "us-east-1a",
												"region":           "us-east-1",
											},
											"subnet": map[string]interface{}{
												"id": "subnet-12345",
											},
											"instanceType": ec2types.InstanceType("m6a.4xlarge"),
											"publicIp":     true,
											"tags": []interface{}{
												map[string]interface{}{
													"name":  "kubernetes.io/cluster/",
													"value": "owned",
												},
											},
										},
									},
								},
								"metadata": map[string]interface{}{
									"labels": map[string]interface{}{
										"machine.openshift.io/cluster-api-cluster":      "",
										"machine.openshift.io/cluster-api-machine-role": "infra",
										"machine.openshift.io/cluster-api-machine-type": "infra",
										"machine.openshift.io/cluster-api-machineset":   "-infra-amd64-us-east-1a",
									},
								},
							},
						},
					},
					map[string]interface{}{
						"metadata": map[string]interface{}{
							"name":      "-infra-amd64-us-east-1a",
							"namespace": "openshift-machine-api",
						},
						"spec": map[string]interface{}{
							"maxReplicas": 5,
							"minReplicas": 0,
							"scaleTargetRef": map[string]interface{}{
								"apiVersion": "machine.openshift.io/v1beta1",
								"kind":       "MachineSet",
								"name":       "-infra-amd64-us-east-1a",
							},
						},
						"apiVersion": "autoscaling.openshift.io/v1beta1",
						"kind":       "MachineAutoscaler",
					},
				},
				"worker-aarch64.yaml": {
					map[string]interface{}{
						"spec": map[string]interface{}{
							"selector": map[string]interface{}{
								"matchLabels": map[string]interface{}{
									"machine.openshift.io/cluster-api-machineset": "-worker-aarch64-us-east-1a",
									"machine.openshift.io/cluster-api-cluster":    "",
								},
							},
							"template": map[string]interface{}{
								"metadata": map[string]interface{}{
									"labels": map[string]interface{}{
										"machine.openshift.io/cluster-api-cluster":      "",
										"machine.openshift.io/cluster-api-machine-role": "worker",
										"machine.openshift.io/cluster-api-machine-type": "worker",
										"machine.openshift.io/cluster-api-machineset":   "-worker-aarch64-us-east-1a",
									},
								},
								"spec": map[string]interface{}{
									"metadata": map[string]interface{}{
										"labels": map[string]interface{}{
											"node-role.kubernetes.io/worker": "",
											"node-role.kubernetes.io":        "worker",
										},
									},
									"providerSpec": map[string]interface{}{
										"value": map[string]interface{}{
											"ami": map[string]interface{}{
												"id": "ami-0e9cdc0e85e0a6aeb",
											},
											"apiVersion": "machine.openshift.io/v1beta1",
											"credentialsSecret": map[string]interface{}{
												"name": "aws-cloud-credentials",
											},
											"instanceType": ec2types.InstanceType("m6g.4xlarge"),
											"placement": map[string]interface{}{
												"availabilityZone": "us-east-1a",
												"region":           "us-east-1",
											},
											"tags": []interface{}{
												map[string]interface{}{
													"name":  "kubernetes.io/cluster/",
													"value": "owned",
												},
											},
											"kind": "AWSMachineProviderConfig",
											"metadata": map[string]interface{}{
												"creationTimestamp": nil,
											},
											"securityGroups": []interface{}{
												map[string]interface{}{
													"filters": []interface{}{
														map[string]interface{}{
															"values": []interface{}{
																"test-sg",
															},
															"name": "tag:Name",
														},
													},
												},
											},
											"deviceIndex":            0,
											"metadataServiceOptions": map[string]interface{}{},
											"publicIp":               true,
											"blockDevices": []interface{}{
												map[string]interface{}{
													"ebs": map[string]interface{}{
														"encrypted": true,
														"iops":      0,
														"kmsKey": map[string]interface{}{
															"arn": "",
														},
														"volumeSize": 120,
														"volumeType": "gp3",
													},
												},
											},
											"iamInstanceProfile": map[string]interface{}{
												"id": "-worker-profile",
											},
											"subnet": map[string]interface{}{
												"id": "subnet-12345",
											},
											"userDataSecret": map[string]interface{}{
												"name": "worker-user-data",
											},
										},
									},
								},
							},
						},
						"apiVersion": "machine.openshift.io/v1beta1",
						"kind":       "MachineSet",
						"metadata": map[string]interface{}{
							"labels": map[string]interface{}{
								"machine.openshift.io/cluster-api-cluster": "",
							},
							"name":      "-worker-aarch64-us-east-1a",
							"namespace": "openshift-machine-api",
						},
					},
					map[string]interface{}{
						"apiVersion": "autoscaling.openshift.io/v1beta1",
						"kind":       "MachineAutoscaler",
						"metadata": map[string]interface{}{
							"name":      "-worker-aarch64-us-east-1a",
							"namespace": "openshift-machine-api",
						},
						"spec": map[string]interface{}{
							"maxReplicas": 5,
							"minReplicas": 0,
							"scaleTargetRef": map[string]interface{}{
								"apiVersion": "machine.openshift.io/v1beta1",
								"kind":       "MachineSet",
								"name":       "-worker-aarch64-us-east-1a",
							},
						},
					},
				},
				"worker-amd64.yaml": {
					map[string]interface{}{
						"kind": "MachineSet",
						"metadata": map[string]interface{}{
							"labels": map[string]interface{}{
								"machine.openshift.io/cluster-api-cluster": "",
							},
							"name":      "-worker-amd64-us-east-1a",
							"namespace": "openshift-machine-api",
						},
						"spec": map[string]interface{}{
							"selector": map[string]interface{}{
								"matchLabels": map[string]interface{}{
									"machine.openshift.io/cluster-api-cluster":    "",
									"machine.openshift.io/cluster-api-machineset": "-worker-amd64-us-east-1a",
								},
							},
							"template": map[string]interface{}{
								"metadata": map[string]interface{}{
									"labels": map[string]interface{}{
										"machine.openshift.io/cluster-api-machine-type": "worker",
										"machine.openshift.io/cluster-api-machineset":   "-worker-amd64-us-east-1a",
										"machine.openshift.io/cluster-api-cluster":      "",
										"machine.openshift.io/cluster-api-machine-role": "worker",
									},
								},
								"spec": map[string]interface{}{
									"metadata": map[string]interface{}{
										"labels": map[string]interface{}{
											"node-role.kubernetes.io":        "worker",
											"node-role.kubernetes.io/worker": "",
										},
									},
									"providerSpec": map[string]interface{}{
										"value": map[string]interface{}{
											"publicIp": true,
											"tags": []interface{}{
												map[string]interface{}{
													"name":  "kubernetes.io/cluster/",
													"value": "owned",
												},
											},
											"blockDevices": []interface{}{
												map[string]interface{}{
													"ebs": map[string]interface{}{
														"encrypted": true,
														"iops":      0,
														"kmsKey": map[string]interface{}{
															"arn": "",
														},
														"volumeSize": 120,
														"volumeType": "gp3",
													},
												},
											},
											"deviceIndex": 0,
											"securityGroups": []interface{}{
												map[string]interface{}{
													"filters": []interface{}{
														map[string]interface{}{
															"name": "tag:Name",
															"values": []interface{}{
																"test-sg",
															},
														},
													},
												},
											},
											"subnet": map[string]interface{}{
												"id": "subnet-12345",
											},
											"ami": map[string]interface{}{
												"id": "ami-0545fae7edbbbf061",
											},
											"credentialsSecret": map[string]interface{}{
												"name": "aws-cloud-credentials",
											},
											"instanceType":           ec2types.InstanceType("m6a.4xlarge"),
											"metadataServiceOptions": map[string]interface{}{},
											"placement": map[string]interface{}{
												"availabilityZone": "us-east-1a",
												"region":           "us-east-1",
											},
											"userDataSecret": map[string]interface{}{
												"name": "worker-user-data",
											},
											"apiVersion": "machine.openshift.io/v1beta1",
											"iamInstanceProfile": map[string]interface{}{
												"id": "-worker-profile",
											},
											"kind": "AWSMachineProviderConfig",
											"metadata": map[string]interface{}{
												"creationTimestamp": nil,
											},
										},
									},
								},
							},
						},
						"apiVersion": "machine.openshift.io/v1beta1",
					},
					map[string]interface{}{
						"spec": map[string]interface{}{
							"maxReplicas": 5,
							"minReplicas": 0,
							"scaleTargetRef": map[string]interface{}{
								"name":       "-worker-amd64-us-east-1a",
								"apiVersion": "machine.openshift.io/v1beta1",
								"kind":       "MachineSet",
							},
						},
						"apiVersion": "autoscaling.openshift.io/v1beta1",
						"kind":       "MachineAutoscaler",
						"metadata": map[string]interface{}{
							"name":      "-worker-amd64-us-east-1a",
							"namespace": "openshift-machine-api",
						},
					},
				},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			provider := NewAWSProvider(awstypes.EC2ClientGetterFunc(func(context.Context) (awstypes.EC2Client, error) {
				return &awstypes.FakeEC2Client{
					Subnets:        tc.subnets,
					SecurityGroups: tc.securityGroups,
				}, nil
			}))

			manifests, err := provider.GenerateManifests(context.TODO(), logrus.NewEntry(logrus.StandardLogger()), tc.ci)

			b, _ := kyaml.Marshal(manifests)
			_ = os.WriteFile("/tmp/m.yaml", b, 0644)

			if err != nil && tc.wantErr == nil {
				t.Fatalf("want err nil but got: %v", err)
			}
			if err == nil && tc.wantErr != nil {
				t.Fatalf("want err %v but nil", tc.wantErr)
			}
			if err != nil && tc.wantErr != nil {
				if tc.wantErr.Error() != err.Error() {
					t.Fatalf("expect error %q but got %q", tc.wantErr.Error(), err.Error())
				}
				return
			}

			if diff := cmp.Diff(tc.wantManifests, manifests); diff != "" {
				t.Errorf("manifests differs:\n%s", diff)
			}
		})
	}
}
