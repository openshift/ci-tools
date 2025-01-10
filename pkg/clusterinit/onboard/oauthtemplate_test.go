package onboard

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/sirupsen/logrus"

	"k8s.io/utils/ptr"

	"github.com/openshift/ci-tools/pkg/clusterinit/clusterinstall"
)

func TestUpdateOAuthTemplate(t *testing.T) {
	releaseRepo := "/release/repo"
	clusterName := "build99"
	for _, tc := range []struct {
		name           string
		clusterInstall clusterinstall.ClusterInstall
		oauthTemplate  string
		wantManifests  map[string][]interface{}
		wantErr        error
	}{
		{
			name: "Modify template successfully",
			clusterInstall: clusterinstall.ClusterInstall{
				ClusterName: clusterName,
				Onboard: clusterinstall.Onboard{
					OSD:         ptr.To(false),
					Hosted:      ptr.To(false),
					Unmanaged:   ptr.To(false),
					ReleaseRepo: releaseRepo,
				},
			},
			wantManifests: map[string][]interface{}{
				"/release/repo/clusters/build-clusters/build99/assets/admin_cluster_oauth_template.yaml": {
					map[string]interface{}{
						"objects": []interface{}{
							map[string]interface{}{
								"apiVersion": "config.openshift.io/v1",
								"kind":       "OAuth",
								"metadata": map[string]interface{}{
									"name": "cluster",
								},
								"spec": map[string]interface{}{
									"identityProviders": []interface{}{
										map[string]interface{}{
											"mappingMethod": "claim",
											"name":          "RedHat_Internal_SSO",
											"openID": map[string]interface{}{
												"claims": map[string]interface{}{
													"email": []interface{}{
														"email",
													},
													"name": []interface{}{
														"name",
													},
													"preferredUsername": []interface{}{
														"preferred_username",
														"email",
													},
												},
												"clientID": fmt.Sprintf("${%s}", "build99_id"),
												"clientSecret": map[string]interface{}{
													"name": "dex-rh-sso",
												},
												"extraScopes": []interface{}{
													"email",
													"profile",
												},
												"issuer": "https://idp.ci.openshift.org",
											},
											"type": "OpenID",
										},
									},
									"tokenConfig": map[string]interface{}{
										"accessTokenMaxAgeSeconds": 2419200,
									},
								},
							},
						},
						"parameters": []interface{}{
							map[string]interface{}{
								"name":        "build99_id",
								"required":    true,
								"description": "build99_id",
							},
						},
						"apiVersion": "template.openshift.io/v1",
						"kind":       "Template",
					},
				},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			step := NewOAuthTemplateGenerator(&tc.clusterInstall)
			manifests, err := step.Generate(context.TODO(), logrus.NewEntry(logrus.StandardLogger()))

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
				t.Errorf("templates differs:\n%s", diff)
			}
		})
	}
}
