package onboard

import (
	"context"
	"fmt"
	"io/fs"
	"os"

	"github.com/sirupsen/logrus"

	"github.com/openshift/ci-tools/pkg/clustermgmt"
	"sigs.k8s.io/yaml"
)

type oauthTemplateStep struct {
	log            *logrus.Entry
	clusterInstall *clustermgmt.ClusterInstall
	writeTemplate  func(name string, data []byte, perm fs.FileMode) error
}

func (s *oauthTemplateStep) Name() string {
	return "oauth-template"
}

func (s *oauthTemplateStep) Run(_ context.Context) error {
	log := s.log.WithField("step", s.Name())

	if *s.clusterInstall.Onboard.OSD || *s.clusterInstall.Onboard.Hosted || *s.clusterInstall.Onboard.Unmanaged {
		log.WithField("cluster", s.clusterInstall.ClusterName).Info("Not an OCP cluster, skipping")
		return nil
	}

	clusterId := fmt.Sprintf("%s_id", s.clusterInstall.ClusterName)
	template := generateaouthTemplate(clusterId)
	rawTemplateOut, err := yaml.Marshal(template)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}

	outputPath := OAuthTemplatePath(s.clusterInstall.Onboard.ReleaseRepo, s.clusterInstall.ClusterName)
	if err := s.writeTemplate(outputPath, rawTemplateOut, 0644); err != nil {
		return fmt.Errorf("write template %s: %w", outputPath, err)
	}

	log.WithField("template", outputPath).Info("oauth template generated")
	return nil
}

func generateaouthTemplate(clusterIdPlaceholder string) map[string]interface{} {
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

func NewOAuthTemplateStep(log *logrus.Entry, clusterInstall *clustermgmt.ClusterInstall) *oauthTemplateStep {
	return &oauthTemplateStep{
		log:            log,
		clusterInstall: clusterInstall,
		writeTemplate:  os.WriteFile,
	}
}
