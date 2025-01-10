package onboard

import (
	"context"
	"fmt"

	"github.com/sirupsen/logrus"

	"github.com/openshift/ci-tools/pkg/clusterinit/clusterinstall"
	cinitmanifest "github.com/openshift/ci-tools/pkg/clusterinit/manifest"
	cinittypes "github.com/openshift/ci-tools/pkg/clusterinit/types"
)

type oauthTemplateGenerator struct {
	clusterInstall *clusterinstall.ClusterInstall
}

func (s *oauthTemplateGenerator) Name() string {
	return "oauth-template"
}

func (s *oauthTemplateGenerator) Skip() cinittypes.SkipStep {
	return s.clusterInstall.Onboard.OAuthTemplate.SkipStep
}

func (s *oauthTemplateGenerator) ExcludedManifests() cinittypes.ExcludeManifest {
	return s.clusterInstall.Onboard.OAuthTemplate.ExcludeManifest
}

func (s *oauthTemplateGenerator) Patches() []cinitmanifest.Patch {
	return s.clusterInstall.Onboard.OAuthTemplate.Patches
}

func (s *oauthTemplateGenerator) Generate(ctx context.Context, log *logrus.Entry) (map[string][]interface{}, error) {
	if *s.clusterInstall.Onboard.OSD || *s.clusterInstall.Onboard.Hosted || *s.clusterInstall.Onboard.Unmanaged {
		log.WithField("cluster", s.clusterInstall.ClusterName).Info("Not an OCP cluster, skipping")
		return nil, nil
	}

	template := generateOAuthTemplate(s.clusterInstall.ClusterName + "_id")
	outputPath := OAuthTemplatePath(s.clusterInstall.Onboard.ReleaseRepo, s.clusterInstall.ClusterName)

	return map[string][]interface{}{outputPath: {template}}, nil
}

func generateOAuthTemplate(clusterIdPlaceholder string) map[string]interface{} {
	return map[string]interface{}{
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
								"clientID": fmt.Sprintf("${%s}", clusterIdPlaceholder),
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
				"name":        clusterIdPlaceholder,
				"required":    true,
				"description": clusterIdPlaceholder,
			},
		},
		"apiVersion": "template.openshift.io/v1",
		"kind":       "Template",
	}
}

func NewOAuthTemplateGenerator(clusterInstall *clusterinstall.ClusterInstall) *oauthTemplateGenerator {
	return &oauthTemplateGenerator{clusterInstall: clusterInstall}
}
