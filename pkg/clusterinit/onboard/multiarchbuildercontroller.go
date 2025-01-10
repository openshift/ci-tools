package onboard

import (
	"context"
	"fmt"
	"path"

	"github.com/sirupsen/logrus"

	"github.com/openshift/ci-tools/pkg/clusterinit/clusterinstall"
	cinitmanifest "github.com/openshift/ci-tools/pkg/clusterinit/manifest"
	cinittypes "github.com/openshift/ci-tools/pkg/clusterinit/types"
)

type multiarchBuilderControllerGenerator struct {
	clusterInstall *clusterinstall.ClusterInstall
}

func (s *multiarchBuilderControllerGenerator) Name() string {
	return "multiarch-builder-controller"
}

func (s *multiarchBuilderControllerGenerator) Skip() cinittypes.SkipStep {
	return s.clusterInstall.Onboard.MultiarchBuilderController.SkipStep
}

func (s *multiarchBuilderControllerGenerator) ExcludedManifests() cinittypes.ExcludeManifest {
	return s.clusterInstall.Onboard.MultiarchBuilderController.ExcludeManifest
}

func (s *multiarchBuilderControllerGenerator) Patches() []cinitmanifest.Patch {
	return s.clusterInstall.Onboard.MultiarchBuilderController.Patches
}

func (s *multiarchBuilderControllerGenerator) Generate(ctx context.Context, log *logrus.Entry) (map[string][]interface{}, error) {
	manifestsPath := MultiarchBuilderControllerManifestsPath(s.clusterInstall.Onboard.ReleaseRepo, s.clusterInstall.ClusterName)
	pathToManifests := make(map[string][]interface{})

	manifests := s.rbacManifests()
	pathToManifests[path.Join(manifestsPath, "000_mabc-updater_rbac.yaml")] = manifests

	manifests = s.deploymentManifests(s.clusterInstall.ClusterName)
	pathToManifests[path.Join(manifestsPath, "100_deploy.yaml")] = manifests

	return pathToManifests, nil
}

func (s *multiarchBuilderControllerGenerator) deploymentManifests(clusterName string) []interface{} {
	secretName := fmt.Sprintf("multi-arch-builder-controller-%s-registry-credentials", clusterName)
	return []interface{}{
		map[string]interface{}{
			"apiVersion": "apps/v1",
			"kind":       "Deployment",
			"metadata": map[string]interface{}{
				"name":      "multi-arch-builder-controller",
				"namespace": "ci",
			},
			"spec": map[string]interface{}{
				"template": map[string]interface{}{
					"metadata": map[string]interface{}{
						"labels": map[string]interface{}{
							"app": "multi-arch-builder-controller",
						},
					},
					"spec": map[string]interface{}{
						"serviceAccountName": "multi-arch-builder-controller",
						"volumes": []interface{}{
							map[string]interface{}{
								"name": "docker-config",
								"secret": map[string]interface{}{
									"secretName": secretName,
								},
							},
						},
						"containers": []interface{}{
							map[string]interface{}{
								"args": []interface{}{
									"--dry-run=false",
								},
								"image":           "quay-proxy.ci.openshift.org/openshift/ci:ci_multi-arch-builder-controller_latest",
								"imagePullPolicy": "Always",
								"name":            "multi-arch-builder-controller",
								"resources": map[string]interface{}{
									"limits": map[string]interface{}{
										"cpu":    "100m",
										"memory": "128Mi",
									},
									"requests": map[string]interface{}{
										"cpu":    "100m",
										"memory": "128Mi",
									},
								},
								"volumeMounts": []interface{}{
									map[string]interface{}{
										"mountPath": "/.docker/config.json",
										"name":      "docker-config",
										"readOnly":  true,
										"subPath":   ".dockerconfigjson",
									},
								},
							},
						},
						"nodeSelector": map[string]interface{}{
							"kubernetes.io/arch": "amd64",
						},
					},
				},
				"replicas": 1,
				"selector": map[string]interface{}{
					"matchLabels": map[string]interface{}{
						"app": "multi-arch-builder-controller",
					},
				},
			},
		},
	}
}

func (s *multiarchBuilderControllerGenerator) rbacManifests() []interface{} {
	return []interface{}{
		map[string]interface{}{
			"metadata": map[string]interface{}{
				"name": "mabc-updater",
			},
			"rules": []interface{}{
				map[string]interface{}{
					"apiGroups": []interface{}{
						"ci.openshift.io",
					},
					"resources": []interface{}{
						"multiarchbuildconfigs",
					},
					"verbs": []interface{}{
						"*",
					},
				},
			},
			"apiVersion": "rbac.authorization.k8s.io/v1",
			"kind":       "ClusterRole",
		},
		map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "ServiceAccount",
			"metadata": map[string]interface{}{
				"name":      "mabc-updater",
				"namespace": "ci",
			},
		},
		map[string]interface{}{
			"roleRef": map[string]interface{}{
				"apiGroup": "rbac.authorization.k8s.io",
				"kind":     "ClusterRole",
				"name":     "mabc-updater",
			},
			"subjects": []interface{}{
				map[string]interface{}{
					"kind":      "ServiceAccount",
					"name":      "mabc-updater",
					"namespace": "ci",
				},
			},
			"apiVersion": "rbac.authorization.k8s.io/v1",
			"kind":       "ClusterRoleBinding",
			"metadata": map[string]interface{}{
				"name":      "self-provisioner-mabc-updater",
				"namespace": "ci",
			},
		},
	}
}

func NewMultiarchBuilderControllerGenerator(clusterInstall *clusterinstall.ClusterInstall) *multiarchBuilderControllerGenerator {
	return &multiarchBuilderControllerGenerator{clusterInstall: clusterInstall}
}
