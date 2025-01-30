package onboard

import (
	"context"
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/sirupsen/logrus"

	"github.com/openshift/ci-tools/pkg/clusterinit/clusterinstall"
	"github.com/openshift/ci-tools/pkg/clusterinit/types/aws"
)

func TestCloudCredentialManifests(t *testing.T) {
	releaseRepo := "/release/repo"
	for _, tc := range []struct {
		name          string
		ci            clusterinstall.ClusterInstall
		wantManifests map[string][]interface{}
		wantErr       error
	}{
		{
			name: "Generate aws cloud credentials",
			ci: clusterinstall.ClusterInstall{
				ClusterName: "build99",
				Provision:   clusterinstall.Provision{AWS: &aws.Provision{}},
				Onboard: clusterinstall.Onboard{
					ReleaseRepo:     releaseRepo,
					CloudCredential: clusterinstall.CloudCredential{AWS: &aws.CloudCredential{}},
				},
			},
			wantManifests: map[string][]interface{}{
				"/release/repo/clusters/build-clusters/build99/cloud-credential/cluster-init-credentials-request-aws.yaml": {
					map[string]interface{}{
						"apiVersion": "cloudcredential.openshift.io/v1",
						"kind":       "CredentialsRequest",
						"metadata": map[string]interface{}{
							"name":      "cluster-init",
							"namespace": "openshift-cloud-credential-operator",
						},
						"spec": map[string]interface{}{
							"providerSpec": map[string]interface{}{
								"apiVersion": "cloudcredential.openshift.io/v1",
								"kind":       "AWSProviderSpec",
								"statementEntries": []interface{}{
									map[string]interface{}{
										"resource": "*",
										"action": []interface{}{
											"ec2:DescribeSubnets",
											"ec2:DescribeSecurityGroups",
										},
										"effect": "Allow",
									},
								},
							},
							"secretRef": map[string]interface{}{
								"name":      "cluster-init-cloud-credentials",
								"namespace": "ci",
							},
							"serviceAccountNames": []interface{}{
								"cluster-init",
							},
						},
					},
				},
			},
		},
		{
			name: "Raise an error when no cloud has been set",
			ci: clusterinstall.ClusterInstall{
				ClusterName: "build99",
				Provision:   clusterinstall.Provision{AWS: &aws.Provision{}},
				Onboard: clusterinstall.Onboard{
					ReleaseRepo:     releaseRepo,
					CloudCredential: clusterinstall.CloudCredential{},
				},
			},
			wantErr: errors.New("cloud credential: no cloud provider has been set"),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			generator := NewCloudCredentialGenerator(&tc.ci)
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
