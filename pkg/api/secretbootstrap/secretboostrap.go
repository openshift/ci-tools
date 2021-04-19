package secretbootstrap

import (
	"encoding/json"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"sigs.k8s.io/yaml"

	"github.com/openshift/ci-tools/pkg/util/gzip"
)

type AttributeType string

const (
	AttributeTypePassword AttributeType = "password"
)

type BitWardenContext struct {
	BWItem               string                 `json:"bw_item"`
	Field                string                 `json:"field,omitempty"`
	Attachment           string                 `json:"attachment,omitempty"`
	Attribute            AttributeType          `json:"attribute,omitempty"`
	DockerConfigJSONData []DockerConfigJSONData `json:"dockerconfigJSON,omitempty"`
	// If the secret should be base64 decoded before uploading to kube. Encoding
	// it is useful to be able to store binary data.
	Base64Decode bool `json:"base64_decode,omitempty"`
}

type DockerConfigJSONData struct {
	BWItem                    string `json:"bw_item"`
	RegistryURL               string `json:"registry_url"`
	RegistryURLBitwardenField string `json:"registry_url_bw_field"`
	AuthBitwardenAttachment   string `json:"auth_bw_attachment"`
	EmailBitwardenField       string `json:"email_bw_field,omitempty"`
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
	Cluster string `json:"cluster"`
	// A list of clusterGroups to target. Mutually exclusive with 'cluster'
	ClusterGroups []string          `json:"cluster_groups,omitempty"`
	Namespace     string            `json:"namespace"`
	Name          string            `json:"name"`
	Type          corev1.SecretType `json:"type,omitempty"`
}

func (sc SecretContext) String() string {
	return sc.Namespace + "/" + sc.Name + " in cluster " + sc.Cluster
}

type SecretConfig struct {
	From map[string]BitWardenContext `json:"from"`
	To   []SecretContext             `json:"to"`
}

//LoadConfigFromFile renders a Config object loaded from the given file
func LoadConfigFromFile(file string, config *Config) error {
	bytes, err := gzip.ReadFileMaybeGZIP(file)
	if err != nil {
		return err
	}
	err = yaml.UnmarshalStrict(bytes, config)
	if err != nil {
		return err
	}
	return nil
}

// Config is what we version in our repository
type Config struct {
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

func (c *Config) Validate() error {
	var errs []error
	for i, secretConfig := range c.Secrets {
		var foundKey bool
		for key, bwContext := range secretConfig.From {
			switch bwContext.Attribute {
			case AttributeTypePassword, "":
			default:
				errs = append(errs, fmt.Errorf("config[%d].from[%s].attribute: only the '%s' is supported, not %s", i, key, AttributeTypePassword, bwContext.Attribute))
			}
			if key == corev1.DockerConfigJsonKey {
				foundKey = true
			}
		}
		k := -1
		for j, secretContext := range secretConfig.To {
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
						Cluster:   cluster,
						Namespace: to.Namespace,
						Name:      to.Name,
						Type:      to.Type,
					})
				}
			}
		}

		c.Secrets[idx].To = newTo
	}

	return utilerrors.NewAggregate(errs)
}
