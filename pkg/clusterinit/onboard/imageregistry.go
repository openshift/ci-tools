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

type imageRegistryStep struct {
	log            *logrus.Entry
	clusterInstall *clusterinstall.ClusterInstall
	writeManifest  func(name string, data []byte, perm fs.FileMode) error
	mkdirAll       func(path string, perm fs.FileMode) error
}

func (s *imageRegistryStep) Name() string {
	return "image-registry"
}

func (s *imageRegistryStep) Run(ctx context.Context) error {
	log := s.log.WithField("step", s.Name())

	if s.clusterInstall.Onboard.ImageRegistry.Skip {
		log.Info("image-registry is not enabled, skipping")
		return nil
	}

	manifestsPath := ImageRegistryManifestsPath(s.clusterInstall.Onboard.ReleaseRepo, s.clusterInstall.ClusterName)
	if err := s.mkdirAll(manifestsPath, 0755); err != nil && !os.IsExist(err) {
		return fmt.Errorf("mkdir %s: %w", manifestsPath, err)
	}

	manifests := s.configClusterManifests()
	manifestBytes, err := citoolsyaml.MarshalMultidoc(yaml.Marshal, manifests...)
	if err != nil {
		return fmt.Errorf("marshal config/cluster manifests: %w", err)
	}

	if err := s.writeManifest(path.Join(manifestsPath, "config-cluster.yaml"), manifestBytes, 0644); err != nil {
		return fmt.Errorf("write rbac manifests: %w", err)
	}

	manifests = s.imagePrunerManifests()
	manifestBytes, err = citoolsyaml.MarshalMultidoc(yaml.Marshal, manifests...)
	if err != nil {
		return fmt.Errorf("marshal image pruner manifests: %w", err)
	}

	if err := s.writeManifest(path.Join(manifestsPath, "imagepruner-cluster.yaml"), manifestBytes, 0644); err != nil {
		return fmt.Errorf("write image pruner manifests: %w", err)
	}
	return nil
}

func (s *imageRegistryStep) imagePrunerManifests() []interface{} {
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

func (s *imageRegistryStep) configClusterManifests() []interface{} {
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

func NewImageRegistryStepStep(log *logrus.Entry, clusterInstall *clusterinstall.ClusterInstall) *imageRegistryStep {
	return &imageRegistryStep{
		log:            log,
		clusterInstall: clusterInstall,
		writeManifest:  os.WriteFile,
		mkdirAll:       os.MkdirAll,
	}
}
