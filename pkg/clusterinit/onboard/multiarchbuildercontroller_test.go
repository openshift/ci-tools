package onboard

import (
	"context"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/sirupsen/logrus"

	"github.com/openshift/ci-tools/pkg/clusterinit/clusterinstall"
)

func TestMultiarchBuilderControllerManifests(t *testing.T) {
	releaseRepo := "/release/repo"
	for _, tt := range []struct {
		name          string
		ci            clusterinstall.ClusterInstall
		wantManifests map[string][]interface{}
		wantErr       error
	}{
		{
			name: "Write manifests successfully",
			ci: clusterinstall.ClusterInstall{
				ClusterName: "build99",
				Onboard:     clusterinstall.Onboard{ReleaseRepo: releaseRepo},
			},
			wantManifests: map[string][]interface{}{
				"/release/repo/clusters/build-clusters/build99/multi-arch-builder-controller/000_mabc-updater_rbac.yaml": {
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
				},
				"/release/repo/clusters/build-clusters/build99/multi-arch-builder-controller/100_deploy.yaml": {
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
												"secretName": "multi-arch-builder-controller-build99-registry-credentials",
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
				},
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			step := NewMultiarchBuilderControllerGenerator(&tt.ci)

			manifests, err := step.Generate(context.TODO(), logrus.NewEntry(logrus.StandardLogger()))

			if err != nil && tt.wantErr == nil {
				t.Fatalf("want err nil but got: %v", err)
			}
			if err == nil && tt.wantErr != nil {
				t.Fatalf("want err %v but nil", tt.wantErr)
			}
			if err != nil && tt.wantErr != nil {
				if tt.wantErr.Error() != err.Error() {
					t.Fatalf("expect error %q but got %q", tt.wantErr.Error(), err.Error())
				}
				return
			}

			if diff := cmp.Diff(tt.wantManifests, manifests); diff != "" {
				t.Errorf("manifests differs:\n%s", diff)
			}
		})
	}
}
