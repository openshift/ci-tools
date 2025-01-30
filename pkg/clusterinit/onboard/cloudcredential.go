package onboard

import (
	"context"
	"errors"
	"fmt"
	"path"

	"github.com/sirupsen/logrus"

	"github.com/openshift/ci-tools/pkg/clusterinit/clusterinstall"
	cinitmanifest "github.com/openshift/ci-tools/pkg/clusterinit/manifest"
	"github.com/openshift/ci-tools/pkg/clusterinit/types"
)

type cloudCredentialGenerator struct {
	clusterInstall *clusterinstall.ClusterInstall
}

func (g *cloudCredentialGenerator) Name() string {
	return "cloud-credential"
}

func (g *cloudCredentialGenerator) Skip() types.SkipStep {
	return g.clusterInstall.Onboard.CloudCredential.SkipStep
}

func (g *cloudCredentialGenerator) ExcludedManifests() types.ExcludeManifest {
	return g.clusterInstall.Onboard.CloudCredential.ExcludeManifest
}

func (g *cloudCredentialGenerator) Patches() []cinitmanifest.Patch {
	return g.clusterInstall.Onboard.CloudCredential.Patches
}

func (g *cloudCredentialGenerator) Generate(ctx context.Context, log *logrus.Entry) (map[string][]interface{}, error) {
	pathToManifests := make(map[string][]interface{})
	basePath := CloudCredentialManifestsPath(g.clusterInstall.Onboard.ReleaseRepo, g.clusterInstall.ClusterName)

	manifests, filename, err := g.manifests()
	if err != nil {
		return nil, fmt.Errorf("cloud credential: %w", err)
	}

	pathToManifests[path.Join(basePath, filename)] = manifests
	return pathToManifests, nil
}

func (g *cloudCredentialGenerator) manifests() ([]interface{}, string, error) {
	if g.clusterInstall.Onboard.CloudCredential.AWS != nil {
		return []interface{}{
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
		}, "cluster-init-credentials-request-aws.yaml", nil
	}
	return nil, "", errors.New("no cloud provider has been set")
}

func NewCloudCredentialGenerator(clusterInstall *clusterinstall.ClusterInstall) *cloudCredentialGenerator {
	return &cloudCredentialGenerator{clusterInstall: clusterInstall}
}
