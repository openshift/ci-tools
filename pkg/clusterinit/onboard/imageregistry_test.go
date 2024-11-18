package onboard

import (
	"context"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/sirupsen/logrus"

	"k8s.io/utils/ptr"

	"github.com/openshift/ci-tools/pkg/clusterinit/clusterinstall"
)

func TestImageRegistryManifests(t *testing.T) {
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
				Onboard: clusterinstall.Onboard{
					OSD:         ptr.To(false),
					ReleaseRepo: releaseRepo,
				},
			},
			wantManifests: map[string][]interface{}{
				"/release/repo/clusters/build-clusters/build99/openshift-image-registry/config-cluster.yaml": {
					map[string]interface{}{
						"apiVersion": "imageregistry.operator.openshift.io/v1",
						"kind":       "Config",
						"metadata": map[string]interface{}{
							"name": "cluster",
						},
						"spec": map[string]interface{}{
							"routes": []interface{}{
								map[string]interface{}{
									"hostname":   "registry.build99.ci.openshift.org",
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
				},
				"/release/repo/clusters/build-clusters/build99/openshift-image-registry/imagepruner-cluster.yaml": {
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
				},
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			generator := NewImageRegistryGenerator(&tt.ci)
			manifests, err := generator.Generate(context.TODO(), logrus.NewEntry(logrus.StandardLogger()))

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
