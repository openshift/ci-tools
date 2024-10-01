package cisecretgenerator

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sirupsen/logrus"

	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/yaml"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/api/secretgenerator"
	"github.com/openshift/ci-tools/pkg/clustermgmt/clusterinstall"
	"github.com/openshift/ci-tools/pkg/clustermgmt/onboard"
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

func UpdateSecretGenerator(log *logrus.Entry, ci *clusterinstall.ClusterInstall) error {
	log = log.WithField("step", "ci-secret-generator")

	filename := filepath.Join(ci.Onboard.ReleaseRepo, "core-services", "ci-secret-generator", "_config.yaml")
	data, err := os.ReadFile(filename)
	if err != nil {
		return err
	}
	var c SecretGenConfig
	if err = yaml.Unmarshal(data, &c); err != nil {
		return err
	}
	if err = updateSecretGeneratorConfig(log, ci, &c); err != nil {
		return err
	}
	rawYaml, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	return os.WriteFile(filename, rawYaml, 0644)
}

func updateSecretGeneratorConfig(log *logrus.Entry, ci *clusterinstall.ClusterInstall, c *SecretGenConfig) error {
	filterByCluster := byParam("cluster", string(api.ClusterBuild01))

	serviceAccountConfigPath := onboard.ServiceAccountKubeconfigPath(serviceAccountWildcard, clusterWildcard)
	if err := appendToSecretItem(log, ci, c, byItemName(onboard.BuildUFarm), filterByCluster, byFieldName(serviceAccountConfigPath)); err != nil {
		return err
	}

	token := fmt.Sprintf("token_%s_%s_reg_auth_value.txt", serviceAccountWildcard, clusterWildcard)
	filterByFieldName, filterBySA := byFieldName(token), byParam("service_account", "image-puller")
	if err := appendToSecretItem(log, ci, c, byItemName(onboard.BuildUFarm), filterByCluster, filterByFieldName, filterBySA); err != nil {
		return err
	}

	if err := appendToSecretItem(log, ci, c, byItemName("ci-chat-bot"), filterByCluster, byFieldName(serviceAccountConfigPath)); err != nil {
		return err
	}

	if !*ci.Onboard.Unmanaged {
		if err := appendToSecretItem(log, ci, c, byItemName(onboard.PodScaler), filterByCluster, byFieldName(serviceAccountConfigPath)); err != nil {
			return err
		}
	}

	return nil
}

func appendToSecretItem(log *logrus.Entry, ci *clusterinstall.ClusterInstall, c *SecretGenConfig, filters ...secretItemFilter) error {
	si, err := findSecretItem(*c, filters...)
	if err != nil {
		return err
	}
	log.Infof("Appending to secret item: %s", explainFilters(filters...))
	si.Params["cluster"] = sets.List(sets.New(si.Params["cluster"]...).Insert(ci.ClusterName))
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
