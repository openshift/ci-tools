package secretbootstrap

import (
	"encoding/json"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"sigs.k8s.io/yaml"

	gsmvalidation "github.com/openshift/ci-tools/pkg/gsm-validation"
	"github.com/openshift/ci-tools/pkg/util/gzip"
)

// GSMConfig is the top-level configuration for GSM-based secrets
type GSMConfig struct {
	ClusterGroups map[string][]string       `json:"cluster_groups,omitempty"`
	Components    map[string][]GSMSecretRef `json:"components,omitempty"`
	Bundles       []Bundle                  `json:"bundles"`
}

// Bundle defines a logical group of GSM secrets
type Bundle struct {
	Name          string `json:"name"`
	Components    []string
	DockerConfig  *DockerConfigSpec `json:"dockerconfig,omitempty"`
	GSMSecrets    []GSMSecretRef    `json:"gsm_secrets,omitempty"`
	SyncToCluster bool              `json:"sync_to_cluster,omitempty"`
	Targets       []TargetSpec      `json:"targets,omitempty"`
}

type GSMSecretRef struct {
	Collection string        `json:"collection"`
	Secrets    []SecretEntry `json:"secrets"` // Encoded names as they appear in GSM
}

// SecretEntry can be either a simple string or an object with 'as' field
type SecretEntry struct {
	Name string `json:"name,omitempty"`
	As   string `json:"as,omitempty"`
}

// UnmarshalJSON allows SecretEntry to accept both:
//   - "secret-name"                   (simple string)
//   - {name: "token", as: "renamed"}  (object)
func (s *SecretEntry) UnmarshalJSON(data []byte) error {
	// Try parsing as a simple string first
	var str string
	if err := json.Unmarshal(data, &str); err == nil {
		s.Name = str
		return nil
	}

	// Otherwise, parse as an object with name/as fields
	type Alias SecretEntry
	aux := &struct{ *Alias }{Alias: (*Alias)(s)}
	return json.Unmarshal(data, aux)
}

// DockerConfigSpec defines how to construct a DockerConfig json from GSM secrets
type DockerConfigSpec struct {
	As         string             `json:"as"`
	Registries []RegistryAuthData `json:"registries"`
}

// RegistryAuthData specifies registry credentials
type RegistryAuthData struct {
	Collection  string `json:"collection"`
	RegistryURL string `json:"registry_url"`
	AuthField   string `json:"auth_field"`
	EmailField  string `json:"email_field,omitempty"`
}

// TargetSpec defines where a bundle should be synced
type TargetSpec struct {
	ClusterGroups []string          `json:"cluster_groups,omitempty"`
	Cluster       string            `json:"cluster,omitempty"`
	Namespace     string            `json:"namespace"`
	Type          corev1.SecretType `json:"type,omitempty"`
}

// LoadGSMConfigFromFile loads a GSMConfig from a YAML file
func LoadGSMConfigFromFile(file string, config *GSMConfig) error {
	bytes, err := gzip.ReadFileMaybeGZIP(file)
	if err != nil {
		return err
	}
	return yaml.UnmarshalStrict(bytes, config)
}

func (c *GSMConfig) UnmarshalJSON(d []byte) error {
	type Alias GSMConfig
	aux := (*Alias)(c)
	if err := json.Unmarshal(d, aux); err != nil {
		return err
	}
	return c.resolve()
}

func (c *GSMConfig) resolve() error {
	var errs []error

	// Expand cluster_groups to concrete cluster names
	for bundleIdx := range c.Bundles {
		bundle := &c.Bundles[bundleIdx]
		var newBundleTargets []TargetSpec

		for targetIdx, target := range bundle.Targets {
			if target.Cluster != "" && len(target.ClusterGroups) != 0 {
				errs = append(errs, fmt.Errorf("bundle %d target %d has both cluster and cluster_groups set, those are mutually exclusive", bundleIdx, targetIdx))
				continue
			}

			// Default Type to Opaque if not set
			if target.Type == "" {
				target.Type = corev1.SecretTypeOpaque
			}

			if target.Cluster != "" {
				newBundleTargets = append(newBundleTargets, target)
				continue
			}

			// Expand cluster groups - inherit Type from the cluster_group definition
			for _, clusterGroupName := range target.ClusterGroups {
				clusters, groupExists := c.ClusterGroups[clusterGroupName]
				if !groupExists {
					errs = append(errs, fmt.Errorf("bundle %d target %d references non-existent cluster_group %s", bundleIdx, targetIdx, clusterGroupName))
					continue
				}

				for _, cluster := range clusters {
					newBundleTargets = append(newBundleTargets, TargetSpec{
						ClusterGroups: target.ClusterGroups, // Keep for serialization
						Cluster:       cluster,
						Namespace:     target.Namespace,
						Type:          target.Type, // Inherit Type (already defaulted to Opaque above)
					})
				}
			}
		}

		bundle.Targets = newBundleTargets
	}

	// Resolve component references into gsm_secrets
	for bundleIdx := range c.Bundles {
		bundle := &c.Bundles[bundleIdx]

		if len(bundle.Components) == 0 {
			continue
		}

		for _, componentName := range bundle.Components {
			componentSecrets, exists := c.Components[componentName]
			if !exists {
				errs = append(errs, fmt.Errorf("bundle %s references non-existent component: %s", bundle.Name, componentName))
				continue
			}

			// Append component's GSMSecretRefs to bundle's GSMSecrets
			bundle.GSMSecrets = append(bundle.GSMSecrets, componentSecrets...)
		}

		// Clear components after resolution
		bundle.Components = nil
	}

	// Expand ${CLUSTER} variable substitution
	var expandedBundles []Bundle
	for _, bundle := range c.Bundles {
		// Check if bundle contains ${CLUSTER} in any secret names
		hasClusterVar := false
		for _, gsmSecretRef := range bundle.GSMSecrets {
			for _, secret := range gsmSecretRef.Secrets {
				if strings.Contains(secret.Name, "${CLUSTER}") {
					hasClusterVar = true
					break
				}
			}
			if hasClusterVar {
				break
			}
		}

		if !hasClusterVar {
			// No ${CLUSTER} substitution needed, keep bundle as-is
			expandedBundles = append(expandedBundles, bundle)
			continue
		}

		// Collect unique clusters from targets
		clusterTargets := make(map[string][]TargetSpec)
		for _, target := range bundle.Targets {
			clusterTargets[target.Cluster] = append(clusterTargets[target.Cluster], target)
		}

		// Create one bundle per unique cluster
		for cluster, targets := range clusterTargets {
			expandedBundle := Bundle{
				Name:          bundle.Name,
				Components:    nil, // Already resolved in phase 2
				DockerConfig:  bundle.DockerConfig,
				SyncToCluster: bundle.SyncToCluster,
				Targets:       targets,
			}

			// Substitute ${CLUSTER} in GSMSecrets
			for _, gsmSecretRef := range bundle.GSMSecrets {
				expandedSecretRef := GSMSecretRef{
					Collection: gsmSecretRef.Collection,
					Secrets:    make([]SecretEntry, len(gsmSecretRef.Secrets)),
				}
				for i, secret := range gsmSecretRef.Secrets {
					expandedSecretRef.Secrets[i] = SecretEntry{
						Name: strings.ReplaceAll(secret.Name, "${CLUSTER}", cluster),
						As:   secret.As,
					}
				}
				expandedBundle.GSMSecrets = append(expandedBundle.GSMSecrets, expandedSecretRef)
			}
			expandedBundles = append(expandedBundles, expandedBundle)
		}
	}
	c.Bundles = expandedBundles

	return utilerrors.NewAggregate(errs)
}

type bundleKey struct {
	name      string
	cluster   string
	namespace string
}

// Validate checks the GSMConfig for internal consistency
func (c *GSMConfig) Validate() error {
	var errs []error

	// Validate components
	// TODO make this a separate func
	componentNames := make(map[string]bool)

	for componentName, gsmSecretRefs := range c.Components {
		if componentName == "" {
			errs = append(errs, fmt.Errorf("component has empty name"))
			continue
		}

		if componentNames[componentName] {
			errs = append(errs, fmt.Errorf("duplicate component name: %s", componentName))
		}
		componentNames[componentName] = true

		if len(gsmSecretRefs) == 0 {
			errs = append(errs, fmt.Errorf("component %s has no GSM secret references", componentName))
			continue
		}

		for j, gsmSecretRef := range gsmSecretRefs {
			if gsmSecretRef.Collection == "" {
				errs = append(errs, fmt.Errorf("component %s[%d] has empty collection", componentName, j))
			}
			if len(gsmSecretRef.Secrets) == 0 {
				errs = append(errs, fmt.Errorf("component %s[%d] has no secrets", componentName, j))
			}
			for k, secret := range gsmSecretRef.Secrets {
				if secret.Name == "" {
					errs = append(errs, fmt.Errorf("component %s[%d].secrets[%d] has empty name", componentName, j, k))
				}
			}
		}
	}

	// Check for duplicate bundles by name+cluster+namespace combination
	seenBundles := make(map[bundleKey]bool)

	for i, bundle := range c.Bundles {
		if bundle.Name == "" {
			errs = append(errs, fmt.Errorf("bundle[%d] has empty name", i))
			continue
		}

		// Check each target for duplicates
		for _, target := range bundle.Targets {
			key := bundleKey{
				name:      bundle.Name,
				cluster:   target.Cluster,
				namespace: target.Namespace,
			}
			if seenBundles[key] {
				errs = append(errs, fmt.Errorf(
					"duplicate bundle: name=%s cluster=%s namespace=%s",
					bundle.Name, target.Cluster, target.Namespace))
			}
			seenBundles[key] = true
		}

		if err := validateBundle(&bundle, i); err != nil {
			errs = append(errs, err)
		}
	}

	return utilerrors.NewAggregate(errs)
}

func validateBundle(bundle *Bundle, idx int) error {
	var errs []error

	if bundle.SyncToCluster && len(bundle.Targets) == 0 {
		errs = append(errs, fmt.Errorf("bundle %s has sync_to_cluster: true but no targets", bundle.Name))
	}
	if !bundle.SyncToCluster && len(bundle.Targets) > 0 {
		errs = append(errs, fmt.Errorf("bundle %s has sync_to_cluster: false but has targets (targets only apply when sync_to_cluster: true)", bundle.Name))
	}

	for j, target := range bundle.Targets {
		if target.Namespace == "" {
			errs = append(errs, fmt.Errorf("bundle %s target[%d] has empty namespace", bundle.Name, j))
		}
		// At this point, target.Cluster should always be set to a concrete cluster name.
		// If it's empty, cluster_groups resolution failed or target was misconfigured.
		if target.Cluster == "" {
			errs = append(errs, fmt.Errorf("bundle %s target[%d] has empty cluster (should have been resolved by this point)", bundle.Name, j))
		}
	}

	for j, gsmSecret := range bundle.GSMSecrets {
		if gsmSecret.Collection == "" {
			errs = append(errs, fmt.Errorf("bundle %s gsm_secrets[%d] has empty collection", bundle.Name, j))
		}
		if gsmvalidation.ValidateCollectionName(gsmSecret.Collection) == false {
			errs = append(errs, fmt.Errorf("bundle %s gsm_secrets[%d] has invalid collection string", bundle.Name, j))
		}
		if len(gsmSecret.Secrets) == 0 {
			errs = append(errs, fmt.Errorf("bundle %s gsm_secrets[%d] has no secrets", bundle.Name, j))
		}
		for k, secret := range gsmSecret.Secrets {
			if secret.Name == "" {
				errs = append(errs, fmt.Errorf("bundle %s gsm_secrets[%d].secrets[%d] has empty name", bundle.Name, j, k))
			}
			if gsmvalidation.ValidateSecretName(secret.Name) == false {
				errs = append(errs, fmt.Errorf("bundle %s gsm_secrets[%d].secrets[%d] has invalid name", bundle.Name, j, k))
			}
		}
	}

	if bundle.DockerConfig != nil {
		if err := validateDockerConfig(bundle.DockerConfig, idx, bundle.Name); err != nil {
			errs = append(errs, err)
		}
	}

	if len(bundle.GSMSecrets) == 0 && bundle.DockerConfig == nil && len(bundle.Components) == 0 {
		errs = append(errs, fmt.Errorf("bundle %s has neither gsm_secrets, dockerconfig, nor components", bundle.Name))
	}

	return utilerrors.NewAggregate(errs)
}

func validateDockerConfig(dc *DockerConfigSpec, bundleIdx int, bundleName string) error {
	var errs []error

	if dc.As == "" {
		errs = append(errs, fmt.Errorf("bundle[%d] %s dockerconfig has empty 'as' field", bundleIdx, bundleName))
	}

	if len(dc.Registries) == 0 {
		errs = append(errs, fmt.Errorf("bundle[%d] %s dockerconfig has no registries", bundleIdx, bundleName))
	}

	for i, reg := range dc.Registries {
		if reg.Collection == "" {
			errs = append(errs, fmt.Errorf("bundle[%d] %s dockerconfig registry[%d] has empty collection", bundleIdx,
				bundleName, i))
		}
		if reg.RegistryURL == "" {
			errs = append(errs, fmt.Errorf("bundle[%d] %s dockerconfig registry[%d] has empty registry_url", bundleIdx,
				bundleName, i))
		}
		if reg.AuthField == "" {
			errs = append(errs, fmt.Errorf("bundle[%d] %s dockerconfig registry[%d] has empty auth_field", bundleIdx,
				bundleName, i))
		}
	}

	return utilerrors.NewAggregate(errs)
}
