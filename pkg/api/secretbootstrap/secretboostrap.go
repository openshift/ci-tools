package secretbootstrap

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"

	"github.com/getlantern/deepcopy"

	corev1 "k8s.io/api/core/v1"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"sigs.k8s.io/yaml"

	"github.com/openshift/ci-tools/pkg/util/gzip"
	"github.com/openshift/ci-tools/pkg/validation"
)

type ItemContext struct {
	Item                 string                 `json:"item,omitempty"`
	Field                string                 `json:"field,omitempty"`
	DockerConfigJSONData []DockerConfigJSONData `json:"dockerconfigJSON,omitempty"`
	// If the secret should be base64 decoded before uploading to kube. Encoding
	// it is useful to be able to store binary data.
	Base64Decode bool `json:"base64_decode,omitempty"`
}

type DockerConfigJSONData struct {
	Item        string `json:"item"`
	RegistryURL string `json:"registry_url"`
	AuthField   string `json:"auth_field"`
	EmailField  string `json:"email_field,omitempty"`
}

type DockerConfigJSON struct {
	Auths map[string]DockerAuth `json:"auths"`
}

type DockerAuth struct {
	Auth  string `json:"auth"`
	Email string `json:"email,omitempty"`
}

type SecretContext struct {
	// A cluster to target. Mutually exclusive with 'ClusterGroups'
	Cluster string `json:"cluster,omitempty"`
	// A list of clusterGroups to target. Mutually exclusive with 'cluster'
	ClusterGroups []string          `json:"cluster_groups,omitempty"`
	Namespace     string            `json:"namespace,omitempty"`
	Name          string            `json:"name"`
	Type          corev1.SecretType `json:"type,omitempty"`
}

func (sc SecretContext) String() string {
	return sc.Namespace + "/" + sc.Name + " in cluster " + sc.Cluster
}

type SecretConfig struct {
	From map[string]ItemContext `json:"from"`
	To   []SecretContext        `json:"to"`
}

// LoadConfigFromFile renders a Config object loaded from the given file
func LoadConfigFromFile(file string, config *Config) error {
	bytes, err := gzip.ReadFileMaybeGZIP(file)
	if err != nil {
		return err
	}
	return yaml.UnmarshalStrict(bytes, config)
}

// SaveConfigToFile serializes a Config object to the given file
func SaveConfigToFile(file string, config *Config) error {
	bytes, err := yaml.Marshal(config)
	if err != nil {
		return err
	}
	return os.WriteFile(file, bytes, 0644)
}

// Config is what we version in our repository
type Config struct {
	VaultDPTPPrefix           string              `json:"vault_dptp_prefix,omitempty"`
	ClusterGroups             map[string][]string `json:"cluster_groups,omitempty"`
	Secrets                   []SecretConfig      `json:"secret_configs"`
	UserSecretsTargetClusters []string            `json:"user_secrets_target_clusters,omitempty"`
}

type configWithoutUnmarshaler Config

func (c *Config) UnmarshalJSON(d []byte) error {
	var target configWithoutUnmarshaler
	if err := json.Unmarshal(d, &target); err != nil {
		return err
	}

	*c = Config(target)
	return c.resolve()
}

func (c *Config) MarshalJSON() ([]byte, error) {
	target := &configWithoutUnmarshaler{
		VaultDPTPPrefix:           c.VaultDPTPPrefix,
		ClusterGroups:             c.ClusterGroups,
		UserSecretsTargetClusters: c.UserSecretsTargetClusters,
	}
	pre := c.VaultDPTPPrefix + "/"
	var secrets []SecretConfig
	for _, s := range c.Secrets {
		var secret SecretConfig
		if err := deepcopy.Copy(&secret, s); err != nil {
			return nil, err
		}
		stripVaultPrefix(&secret, pre)
		secret.groupClusters()
		secrets = append(secrets, secret)
	}

	target.Secrets = secrets
	return json.Marshal(target)
}

func (s *SecretConfig) groupClusters() {
	var secrets []SecretContext
	for _, to := range s.To {
		if len(to.ClusterGroups) > 0 {
			sc := SecretContext{
				ClusterGroups: to.ClusterGroups,
				Namespace:     to.Namespace,
				Name:          to.Name,
				Type:          to.Type,
			}
			present := false
			for _, context := range secrets {
				if reflect.DeepEqual(context, sc) {
					present = true
					break
				}
			}
			if !present {
				secrets = append(secrets, sc)
			}
		} else {
			// This cluster was not from a group, so nothing special needs to be done
			secrets = append(secrets, to)
		}
	}

	s.To = secrets
}

func stripVaultPrefix(s *SecretConfig, pre string) {
	for key, from := range s.From {
		from.Item = strings.TrimPrefix(from.Item, pre)
		for i, dcj := range from.DockerConfigJSONData {
			from.DockerConfigJSONData[i].Item = strings.TrimPrefix(dcj.Item, pre)
		}
		s.From[key] = from
	}
}

func (c *Config) Validate() error {
	var errs []error
	for i, secretConfig := range c.Secrets {
		var foundKey bool
		for key := range secretConfig.From {
			if key == corev1.DockerConfigJsonKey {
				foundKey = true
			}
		}
		k := -1
		for j, secretContext := range secretConfig.To {
			if err := validation.ValidateSecretInStep(secretContext.Namespace, secretContext.Name); err != nil {
				errs = append(errs, fmt.Errorf("secret[%d] in secretConfig[%d] cannot be used in a step: %w", j, i, err))
			}
			if secretContext.Type == corev1.SecretTypeDockerConfigJson {
				k = j
			}
		}
		if !foundKey && k > -1 {
			errs = append(errs, fmt.Errorf("secret[%d] in secretConfig[%d] with kubernetes.io/dockerconfigjson type have no key named .dockerconfigjson", k, i))
		}
	}
	return utilerrors.NewAggregate(errs)
}

func (c *Config) resolve() error {
	var errs []error

	for idx, secret := range c.Secrets {
		var newTo []SecretContext
		for jdx, to := range secret.To {
			if to.Cluster != "" && len(to.ClusterGroups) != 0 {
				errs = append(errs, fmt.Errorf("item secrets.%d.to.%d has both cluster and cluster_groups set, those are mutually exclusive", idx, jdx))
				continue
			}
			if to.Cluster != "" {
				newTo = append(newTo, to)
				continue
			}
			for _, clusterGroupName := range to.ClusterGroups {
				clusters, groupExists := c.ClusterGroups[clusterGroupName]
				if !groupExists {
					errs = append(errs, fmt.Errorf("item secrets.%d.to.%d references inexistent cluster_group %s", idx, jdx, clusterGroupName))
					continue
				}
				for _, cluster := range clusters {
					newTo = append(newTo, SecretContext{
						ClusterGroups: to.ClusterGroups,
						Cluster:       cluster,
						Namespace:     to.Namespace,
						Name:          to.Name,
						Type:          to.Type,
					})
				}
			}
		}

		c.Secrets[idx].To = newTo

		if c.VaultDPTPPrefix != "" {
			for fromKey, fromValue := range secret.From {
				if fromValue.Item != "" {
					fromValue.Item = c.VaultDPTPPrefix + "/" + fromValue.Item
				}
				for dockerCFGIdx, dockerCFGVal := range fromValue.DockerConfigJSONData {
					if dockerCFGVal.Item != "" {
						dockerCFGVal.Item = c.VaultDPTPPrefix + "/" + dockerCFGVal.Item
						fromValue.DockerConfigJSONData[dockerCFGIdx] = dockerCFGVal
					}
				}

				secret.From[fromKey] = fromValue
			}
		}

	}

	return utilerrors.NewAggregate(errs)
}

const OSDGlobalPullSecretGroupName = "osd_global_pull_secret"
const OpenShiftConfigPullSecretGroupName = "openshift_config_pull_secret"

// OSDGlobalPullSecretGroup returns the list of the OSD cluster names where we need to partially manage the global pull secret
func (c *Config) OSDGlobalPullSecretGroup() []string {
	return c.ClusterGroups[OSDGlobalPullSecretGroupName]
}
