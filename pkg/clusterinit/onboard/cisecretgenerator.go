package onboard

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sirupsen/logrus"

	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/yaml"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/api/secretgenerator"
	"github.com/openshift/ci-tools/pkg/clusterinit/clusterinstall"
)

const (
	serviceAccountWildcard = "$(service_account)"
	clusterWildcard        = "$(cluster)"
)

// SecretGenConfig is used here as using secretgenerator.Config results in 'special' unmarshalling
// where '$(*)' wildcards from the yaml are expanded in the output. Doing so for this purpose results in
// incorrect re-serialization
type SecretGenConfig []secretgenerator.SecretItem

// secretItemFilter applies a filter on a secretgenerator.SecretItem.
// Return true whenever the outcome is positive, that means the SecretItem
// should be consider for further processing, false otherwise.
type secretItemFilter struct {
	apply   func(si *secretgenerator.SecretItem) bool
	explain string
}

type ciSecretGeneratorStep struct {
	log            *logrus.Entry
	clusterInstall *clusterinstall.ClusterInstall
}

func byItemName(name string) secretItemFilter {
	return secretItemFilter{
		apply:   func(si *secretgenerator.SecretItem) bool { return si.ItemName == name },
		explain: fmt.Sprintf("item name: %s", name),
	}
}

func byFieldName(name string) secretItemFilter {
	return secretItemFilter{
		apply: func(si *secretgenerator.SecretItem) bool {
			for _, f := range si.Fields {
				if f.Name == name {
					return true
				}
			}
			return false
		},
		explain: fmt.Sprintf("field name: %s", name),
	}
}

func byParam(name, value string) secretItemFilter {
	return secretItemFilter{
		apply: func(si *secretgenerator.SecretItem) bool {
			for _, v := range si.Params[name] {
				if v == value {
					return true
				}
			}
			return false
		},
		explain: fmt.Sprintf("param: %s=%s", name, value),
	}
}

func explainFilters(filters ...secretItemFilter) string {
	explanations := make([]string, len(filters))
	for i, f := range filters {
		explanations[i] = f.explain
	}
	return strings.Join(explanations, " - ")
}

func (s *ciSecretGeneratorStep) Name() string { return "ci-secret-generator" }

func (s *ciSecretGeneratorStep) Run(ctx context.Context) error {
	s.log = s.log.WithField("step", "ci-secret-generator")

	filename := filepath.Join(s.clusterInstall.Onboard.ReleaseRepo, "core-services", "ci-secret-generator", "_config.yaml")
	data, err := os.ReadFile(filename)
	if err != nil {
		return err
	}
	var c SecretGenConfig
	if err = yaml.Unmarshal(data, &c); err != nil {
		return err
	}
	if err = s.updateSecretGeneratorConfig(&c); err != nil {
		return err
	}
	rawYaml, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	return os.WriteFile(filename, rawYaml, 0644)
}

func (s *ciSecretGeneratorStep) updateSecretGeneratorConfig(c *SecretGenConfig) error {
	filterByCluster := byParam("cluster", string(api.ClusterBuild01))

	serviceAccountConfigPath := ServiceAccountKubeconfigPath(serviceAccountWildcard, clusterWildcard)
	if err := s.appendToSecretItem(c, byItemName(BuildUFarm), filterByCluster, byFieldName(serviceAccountConfigPath)); err != nil {
		return err
	}

	token := fmt.Sprintf("token_%s_%s_reg_auth_value.txt", serviceAccountWildcard, clusterWildcard)
	filterByFieldName, filterBySA := byFieldName(token), byParam("service_account", "image-puller")
	if err := s.appendToSecretItem(c, byItemName(BuildUFarm), filterByCluster, filterByFieldName, filterBySA); err != nil {
		return err
	}

	if err := s.appendToSecretItem(c, byItemName("ci-chat-bot"), filterByCluster, byFieldName(serviceAccountConfigPath)); err != nil {
		return err
	}

	if !*s.clusterInstall.Onboard.Unmanaged {
		if err := s.appendToSecretItem(c, byItemName(PodScaler), filterByCluster, byFieldName(serviceAccountConfigPath)); err != nil {
			return err
		}
	}

	return nil
}

func (s *ciSecretGeneratorStep) appendToSecretItem(c *SecretGenConfig, filters ...secretItemFilter) error {
	si, err := findSecretItem(*c, filters...)
	if err != nil {
		return err
	}
	s.log.Infof("Appending to secret item: %s", explainFilters(filters...))
	si.Params["cluster"] = sets.List(sets.New(si.Params["cluster"]...).Insert(s.clusterInstall.ClusterName))
	return nil
}

func findSecretItem(c SecretGenConfig, filters ...secretItemFilter) (*secretgenerator.SecretItem, error) {
siLoop:
	for i, si := range c {
		for _, f := range filters {
			if !f.apply(&si) {
				continue siLoop
			}
		}
		return &c[i], nil
	}

	return nil, fmt.Errorf("couldn't find SecretItem: %s", explainFilters(filters...))
}

func NewCiSecretGeneratorStep(log *logrus.Entry, clusterInstall *clusterinstall.ClusterInstall) *ciSecretGeneratorStep {
	return &ciSecretGeneratorStep{
		log:            log,
		clusterInstall: clusterInstall,
	}
}
