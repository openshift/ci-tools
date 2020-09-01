package secretbootstrap

import (
	"encoding/json"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
)

type AttributeType string

const (
	AttributeTypePassword AttributeType = "password"
)

type BitWardenContext struct {
	BWItem     string        `json:"bw_item"`
	Field      string        `json:"field,omitempty"`
	Attachment string        `json:"attachment,omitempty"`
	Attribute  AttributeType `json:"attribute,omitempty"`
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

// Config is what we version in our repository
type Config struct {
	ClusterGroups map[string][]string `json:"cluster_groups,omitempty"`
	Secrets       []SecretConfig      `json:"secret_configs"`
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
