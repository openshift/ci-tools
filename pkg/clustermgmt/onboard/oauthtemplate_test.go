package onboard

import (
	"context"
	"io/fs"
	"path"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/sirupsen/logrus"

	"k8s.io/utils/ptr"

	"github.com/openshift/ci-tools/pkg/clustermgmt/clusterinstall"
)

func TestUpdateOAuthTemplate(t *testing.T) {
	releaseRepo := "/release/repo"
	clusterName := "build99"
	for _, tc := range []struct {
		name              string
		clusterInstall    clusterinstall.ClusterInstall
		oauthTemplate     string
		wantOAuthTemplate string
		wantErr           error
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
			oauthTemplate: `apiVersion: template.openshift.io/v1
kind: Template
objects:
- apiVersion: config.openshift.io/v1
  kind: OAuth
  metadata:
    name: cluster
  spec:
    tokenConfig:
      accessTokenMaxAgeSeconds: 2419200 # 28d
    identityProviders:
      - name: RedHat_Internal_SSO
        mappingMethod: claim
        type: OpenID
        openID:
          clientID: "${build11_id}"
          clientSecret:
            name: dex-rh-sso
          extraScopes:
          - email
          - profile
          claims:
            preferredUsername:
            - preferred_username
            - email
            name:
            - name
            email:
            - email
          issuer: https://idp.ci.openshift.org
parameters:
- description: build11_id
  name: build11_id
  required: true
`,
			wantOAuthTemplate: `apiVersion: template.openshift.io/v1
kind: Template
objects:
- apiVersion: config.openshift.io/v1
  kind: OAuth
  metadata:
    name: cluster
  spec:
    identityProviders:
    - mappingMethod: claim
      name: RedHat_Internal_SSO
      openID:
        claims:
          email:
          - email
          name:
          - name
          preferredUsername:
          - preferred_username
          - email
        clientID: ${build99_id}
        clientSecret:
          name: dex-rh-sso
        extraScopes:
        - email
        - profile
        issuer: https://idp.ci.openshift.org
      type: OpenID
    tokenConfig:
      accessTokenMaxAgeSeconds: 2419200
parameters:
- description: build99_id
  name: build99_id
  required: true
`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			step := NewOAuthTemplateStep(logrus.NewEntry(logrus.StandardLogger()), &tc.clusterInstall)
			var (
				oauthTemplate          string
				oauthTemplateWritePath string
			)
			step.writeTemplate = func(name string, data []byte, perm fs.FileMode) error {
				oauthTemplateWritePath = name
				oauthTemplate = string(data)
				return nil
			}

			err := step.Run(context.TODO())

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

			wantOAuthTemplateWritePath := path.Join(releaseRepo, "clusters/build-clusters/build99/assets/admin_cluster_oauth_template.yaml")
			if oauthTemplateWritePath != wantOAuthTemplateWritePath {
				t.Errorf("want manifests path (write) %q but got %q", wantOAuthTemplateWritePath, oauthTemplateWritePath)
			}

			if diff := cmp.Diff(tc.wantOAuthTemplate, oauthTemplate); diff != "" {
				t.Errorf("templates differs:\n%s", diff)
			}
		})
	}
}
