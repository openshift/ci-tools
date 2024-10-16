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

type multiarchBuilderControllerStep struct {
	log            *logrus.Entry
	clusterInstall *clusterinstall.ClusterInstall
	writeManifest  func(name string, data []byte, perm fs.FileMode) error
	mkdirAll       func(path string, perm fs.FileMode) error
}

func (s *multiarchBuilderControllerStep) Name() string {
	return "multiarch-builder-controller"
}

func (s *multiarchBuilderControllerStep) Run(ctx context.Context) error {
	log := s.log.WithField("step", s.Name())

	if s.clusterInstall.Onboard.MultiarchBuilderController.Skip {
		log.Info("multiarch is not enabled, skipping")
		return nil
	}

	manifests := s.rbacManifests()
	manifestBytes, err := citoolsyaml.MarshalMultidoc(yaml.Marshal, manifests...)
	if err != nil {
		return fmt.Errorf("marshal rbac manifests: %w", err)
	}

	manifestsPath := MultiarchBuilderControllerManifestsPath(s.clusterInstall.Onboard.ReleaseRepo, s.clusterInstall.ClusterName)
	if err := s.mkdirAll(manifestsPath, 0755); err != nil && !os.IsExist(err) {
		return fmt.Errorf("mkdir %s: %w", manifestsPath, err)
	}

	if err := s.writeManifest(path.Join(manifestsPath, "000_mabc-updater_rbac.yaml"), manifestBytes, 0644); err != nil {
		return fmt.Errorf("write rbac manifests: %w", err)
	}

	manifests = s.deploymentManifests(s.clusterInstall.ClusterName)
	manifestBytes, err = citoolsyaml.MarshalMultidoc(yaml.Marshal, manifests...)
	if err != nil {
		return fmt.Errorf("marshal deployment manifests: %w", err)
	}

	manifestsPath = MultiarchBuilderControllerManifestsPath(s.clusterInstall.Onboard.ReleaseRepo, s.clusterInstall.ClusterName)
	if err := s.writeManifest(path.Join(manifestsPath, "100_deploy.yaml"), manifestBytes, 0644); err != nil {
		return fmt.Errorf("write deployment manifests: %w", err)
	}
	return nil
}

func (s *multiarchBuilderControllerStep) deploymentManifests(clusterName string) []interface{} {
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
								"image":           "multi-arch-builder-controller:latest",
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

func (s *multiarchBuilderControllerStep) rbacManifests() []interface{} {
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

func NewMultiarchBuilderControllerStep(log *logrus.Entry, clusterInstall *clusterinstall.ClusterInstall) *multiarchBuilderControllerStep {
	return &multiarchBuilderControllerStep{
		log:            log,
		clusterInstall: clusterInstall,
		writeManifest:  os.WriteFile,
		mkdirAll:       os.MkdirAll,
	}
}
