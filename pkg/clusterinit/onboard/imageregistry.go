package onboard

import (
	"context"
	"fmt"
	"path"

	"github.com/sirupsen/logrus"

	"github.com/openshift/ci-tools/pkg/clusterinit/clusterinstall"
	cinitmanifest "github.com/openshift/ci-tools/pkg/clusterinit/manifest"
	"github.com/openshift/ci-tools/pkg/clusterinit/types"
)

type imageRegistryGenerator struct {
	clusterInstall *clusterinstall.ClusterInstall
}

func (s *imageRegistryGenerator) Name() string {
	return "image-registry"
}

func (s *imageRegistryGenerator) Skip() types.SkipStep {
	return s.clusterInstall.Onboard.ImageRegistry.SkipStep
}

func (s *imageRegistryGenerator) ExcludedManifests() types.ExcludeManifest {
	return s.clusterInstall.Onboard.ImageRegistry.ExcludeManifest
}

func (s *imageRegistryGenerator) Patches() []cinitmanifest.Patch {
	return s.clusterInstall.Onboard.ImageRegistry.Patches
}

func (s *imageRegistryGenerator) Generate(ctx context.Context, log *logrus.Entry) (map[string][]interface{}, error) {
	pathToManifests := make(map[string][]interface{})
	basePath := ImageRegistryManifestsPath(s.clusterInstall.Onboard.ReleaseRepo, s.clusterInstall.ClusterName)

	manifests := s.configClusterManifests()
	pathToManifests[path.Join(basePath, "config-cluster.yaml")] = manifests

	manifests = s.imagePrunerManifests()
	pathToManifests[path.Join(basePath, "imagepruner-cluster.yaml")] = manifests

	return pathToManifests, nil
}

func (s *imageRegistryGenerator) imagePrunerManifests() []interface{} {
	return []interface{}{
		map[string]interface{}{
			"spec": map[string]interface{}{
				"successfulJobsHistoryLimit": 3,
				"suspend":                    false,
				"failedJobsHistoryLimit":     3,
				"keepTagRevisions":           3,
				"schedule":                   "",
			},
			"apiVersion": "imageregistry.operator.openshift.io/v1",
			"kind":       "ImagePruner",
			"metadata": map[string]interface{}{
				"name": "cluster",
			},
		},
	}
}

func (s *imageRegistryGenerator) configClusterManifests() []interface{} {
	if *s.clusterInstall.Onboard.OSD {
		clusterName := s.clusterInstall.ClusterName
		return []interface{}{
			map[string]interface{}{
				"spec": map[string]interface{}{
					"managementState": "Managed",
					"replicas":        2,
					"routes": []interface{}{
						map[string]interface{}{
							"secretName": "public-route-tls",
							"hostname":   fmt.Sprintf("registry.%s.ci.openshift.org", clusterName),
							"name":       fmt.Sprintf("registry-%s-ci-openshift-org", clusterName),
						},
					},
				},
				"apiVersion": "imageregistry.operator.openshift.io/v1",
				"kind":       "Config",
				"metadata": map[string]interface{}{
					"name": "cluster",
				},
			},
		}
	}
	return []interface{}{
		map[string]interface{}{
			"apiVersion": "imageregistry.operator.openshift.io/v1",
			"kind":       "Config",
			"metadata": map[string]interface{}{
				"name": "cluster",
			},
			"spec": map[string]interface{}{
				"routes": []interface{}{
					map[string]interface{}{
						"hostname":   fmt.Sprintf("registry.%s.ci.openshift.org", s.clusterInstall.ClusterName),
						"name":       "public-routes",
						"secretName": "public-route-tls",
					},
				},
				"tolerations": []interface{}{
					map[string]interface{}{
						"effect":   "NoSchedule",
						"key":      "node-role.kubernetes.io/infra",
						"operator": "Exists",
					},
				},
				"affinity": map[string]interface{}{
					"podAntiAffinity": map[string]interface{}{
						"preferredDuringSchedulingIgnoredDuringExecution": []interface{}{
							map[string]interface{}{
								"podAffinityTerm": map[string]interface{}{
									"labelSelector": map[string]interface{}{
										"matchExpressions": []interface{}{
											map[string]interface{}{
												"key":      "docker-registry",
												"operator": "In",
												"values": []interface{}{
													"default",
												},
											},
										},
									},
									"topologyKey": "kubernetes.io/hostname",
								},
								"weight": 100,
							},
						},
					},
				},
				"managementState": "Managed",
				"nodeSelector": map[string]interface{}{
					"node-role.kubernetes.io/infra": "",
				},
				"replicas": 5,
			},
		},
	}
}

func NewImageRegistryGenerator(clusterInstall *clusterinstall.ClusterInstall) *imageRegistryGenerator {
	return &imageRegistryGenerator{clusterInstall: clusterInstall}
}
