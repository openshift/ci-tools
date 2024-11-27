package onboard

import (
	"context"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/sirupsen/logrus"

	"github.com/openshift/ci-tools/pkg/clusterinit/clusterinstall"
	"github.com/openshift/ci-tools/pkg/clusterinit/types/aws"
)

func TestCloudabilityAgentManifests(t *testing.T) {
	releaseRepo := "/release/repo"
	for _, tc := range []struct {
		name          string
		ci            clusterinstall.ClusterInstall
		wantManifests map[string][]interface{}
		wantErr       error
	}{
		{
			name: "Write manifests successfully",
			ci: clusterinstall.ClusterInstall{
				ClusterName: "build99",
				Provision:   clusterinstall.Provision{AWS: &aws.Provision{}},
				Onboard:     clusterinstall.Onboard{ReleaseRepo: releaseRepo},
			},
			wantManifests: map[string][]interface{}{
				"/release/repo/clusters/build-clusters/build99/cloudability-agent.yaml": {
					map[string]interface{}{
						"apiVersion": "v1",
						"kind":       "Namespace",
						"metadata": map[string]interface{}{
							"name": "cloudability",
						},
					},
					map[string]interface{}{
						"apiVersion": "v1",
						"kind":       "ServiceAccount",
						"metadata": map[string]interface{}{
							"name":      "cloudability",
							"namespace": "cloudability",
						},
					},
					map[string]interface{}{
						"apiVersion": "rbac.authorization.k8s.io/v1",
						"kind":       "ClusterRole",
						"metadata": map[string]interface{}{
							"name":      "cloudability-metrics-agent",
							"namespace": "kube-system",
						},
						"rules": []interface{}{
							map[string]interface{}{
								"verbs": []interface{}{
									"get",
									"watch",
									"list",
								},
								"apiGroups": []interface{}{
									"",
									"extensions",
									"apps",
									"batch",
								},
								"resources": []interface{}{
									"namespaces",
									"replicationcontrollers",
									"services",
									"nodes",
									"nodes/spec",
									"pods",
									"jobs",
									"cronjobs",
									"persistentvolumes",
									"persistentvolumeclaims",
									"deployments",
									"replicasets",
									"daemonsets",
								},
							},
							map[string]interface{}{
								"resources": []interface{}{
									"services/proxy",
									"pods/proxy",
									"nodes/proxy",
									"nodes/stats",
									"nodes/metrics",
								},
								"verbs": []interface{}{
									"get",
									"list",
								},
								"apiGroups": []interface{}{
									"",
								},
							},
						},
					},
					map[string]interface{}{
						"apiVersion": "rbac.authorization.k8s.io/v1",
						"kind":       "ClusterRoleBinding",
						"metadata": map[string]interface{}{
							"name":      "cloudability-metrics-agent",
							"namespace": "kube-system",
						},
						"roleRef": map[string]interface{}{
							"apiGroup": "rbac.authorization.k8s.io",
							"kind":     "ClusterRole",
							"name":     "cloudability-metrics-agent",
						},
						"subjects": []interface{}{
							map[string]interface{}{
								"name":      "cloudability",
								"namespace": "cloudability",
								"kind":      "ServiceAccount",
							},
						},
					},
					map[string]interface{}{
						"apiVersion": "rbac.authorization.k8s.io/v1",
						"kind":       "Role",
						"metadata": map[string]interface{}{
							"name":      "cloudability-metrics-agent",
							"namespace": "cloudability",
						},
						"rules": []interface{}{
							map[string]interface{}{
								"apiGroups": []interface{}{
									"*",
								},
								"resources": []interface{}{
									"pods",
									"pods/log",
								},
								"verbs": []interface{}{
									"get",
									"list",
								},
							},
						},
					},
					map[string]interface{}{
						"metadata": map[string]interface{}{
							"name":      "cloudability-metrics-agent",
							"namespace": "cloudability",
						},
						"roleRef": map[string]interface{}{
							"apiGroup": "rbac.authorization.k8s.io",
							"kind":     "Role",
							"name":     "cloudability-metrics-agent",
						},
						"subjects": []interface{}{
							map[string]interface{}{
								"kind":      "ServiceAccount",
								"name":      "cloudability",
								"namespace": "cloudability",
							},
						},
						"apiVersion": "rbac.authorization.k8s.io/v1",
						"kind":       "RoleBinding",
					},
					map[string]interface{}{
						"metadata": map[string]interface{}{
							"labels": map[string]interface{}{
								"name": "cloudability-metrics-agent",
							},
							"name":      "cloudability-metrics-agent",
							"namespace": "cloudability",
						},
						"spec": map[string]interface{}{
							"replicas": 1,
							"selector": map[string]interface{}{
								"matchLabels": map[string]interface{}{
									"app": "cloudability-metrics-agent",
								},
							},
							"template": map[string]interface{}{
								"metadata": map[string]interface{}{
									"labels": map[string]interface{}{
										"app": "cloudability-metrics-agent",
									},
								},
								"spec": map[string]interface{}{
									"containers": []interface{}{
										map[string]interface{}{
											"args": []interface{}{
												"kubernetes",
											},
											"env": []interface{}{
												map[string]interface{}{
													"name": "CLOUDABILITY_API_KEY",
													"valueFrom": map[string]interface{}{
														"secretKeyRef": map[string]interface{}{
															"key":  "api-key",
															"name": "cloudability-api-key",
														},
													},
												},
												map[string]interface{}{
													"value": "build99",
													"name":  "CLOUDABILITY_CLUSTER_NAME",
												},
												map[string]interface{}{
													"name":  "CLOUDABILITY_UPLOAD_REGION",
													"value": "us-west-2",
												},
												map[string]interface{}{
													"name":  "CLOUDABILITY_POLL_INTERVAL",
													"value": "180",
												},
											},
											"image":           "cloudability/metrics-agent:latest",
											"imagePullPolicy": "Always",
											"livenessProbe": map[string]interface{}{
												"exec": map[string]interface{}{
													"command": []interface{}{
														"touch",
														"tmp/healthy",
													},
												},
												"initialDelaySeconds": 120,
												"periodSeconds":       600,
											},
											"name": "cloudability-metrics-agent",
											"resources": map[string]interface{}{
												"limits": map[string]interface{}{
													"cpu":    "1",
													"memory": "4Gi",
												},
												"requests": map[string]interface{}{
													"cpu":    ".5",
													"memory": "2Gi",
												},
											},
											"securityContext": map[string]interface{}{
												"capabilities": map[string]interface{}{
													"drop": []interface{}{
														"ALL",
													},
												},
												"runAsNonRoot": true,
												"seccompProfile": map[string]interface{}{
													"type": "RuntimeDefault",
												},
												"allowPrivilegeEscalation": false,
											},
										},
									},
									"serviceAccount": "cloudability",
								},
							},
						},
						"apiVersion": "apps/v1",
						"kind":       "Deployment",
					},
				},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			generator := NewCloudabilityAgentGenerator(&tc.ci)
			manifests, err := generator.Generate(context.TODO(), logrus.NewEntry(logrus.StandardLogger()))

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
