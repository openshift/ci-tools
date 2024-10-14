package onboard

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path"

	"github.com/sirupsen/logrus"

	"sigs.k8s.io/yaml"

	"github.com/openshift/ci-tools/pkg/clusterinit/clusterinstall"
	citoolsyaml "github.com/openshift/ci-tools/pkg/util/yaml"
)

type cloudabilityAgentStep struct {
	log            *logrus.Entry
	clusterInstall *clusterinstall.ClusterInstall
	writeManifest  func(name string, data []byte, perm fs.FileMode) error
}

func (s *cloudabilityAgentStep) Name() string {
	return "cloudability-agent"
}

func (s *cloudabilityAgentStep) Run(ctx context.Context) error {
	var region string
	switch {
	case s.clusterInstall.Provision.AWS != nil:
		if s.clusterInstall.Infrastructure.Status.PlatformStatus != nil && s.clusterInstall.Infrastructure.Status.PlatformStatus.AWS != nil {
			region = s.clusterInstall.Infrastructure.Status.PlatformStatus.AWS.Region
		}
	default:
		return fmt.Errorf("cloud provider not supported")
	}

	if region == "" {
		return fmt.Errorf("region is empty")
	}

	manifests := s.manifests(s.clusterInstall.ClusterName, region)
	manifestBytes, err := citoolsyaml.MarshalMultidoc(yaml.Marshal, manifests...)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}

	manifestsPath := CloudabilityAgentManifestsPath(s.clusterInstall.Onboard.ReleaseRepo, s.clusterInstall.ClusterName)
	if err := s.writeManifest(path.Join(manifestsPath, "cloudability-agent.yaml"), manifestBytes, 0644); err != nil {
		return fmt.Errorf("write manifest: %w", err)
	}
	return nil
}

func (s *cloudabilityAgentStep) manifests(clusterName, region string) []interface{} {
	return []interface{}{
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
										"value": clusterName,
										"name":  "CLOUDABILITY_CLUSTER_NAME",
									},
									map[string]interface{}{
										"name":  "CLOUDABILITY_UPLOAD_REGION",
										"value": region,
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
	}
}

func NewCloudabilityAgentStep(log *logrus.Entry, clusterInstall *clusterinstall.ClusterInstall) *cloudabilityAgentStep {
	return &cloudabilityAgentStep{
		log:            log,
		clusterInstall: clusterInstall,
		writeManifest:  os.WriteFile,
	}
}
