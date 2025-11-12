package secretbootstrap

import (
	"encoding/json"

	"github.com/openshift/ci-tools/pkg/util/gzip"
	"sigs.k8s.io/yaml"
)

// GSMConfig is the top-level configuration for GSM-based secrets
type GSMConfig struct {
	ClusterGroups         map[string][]string         `json:"cluster_groups,omitempty"`
	Components            map[string][]GSMSecretRef   `json:"components,omitempty"`
	DockerconfigTemplates map[string]DockerConfigSpec `json:"dockerconfig_templates,omitempty"`
	Bundles               []Bundle                    `json:"bundles"`
}

// Bundle defines a logical group of GSM secrets
type Bundle struct {
	Name          string            `json:"name"`
	SyncToCluster bool              `json:"sync_to_cluster,omitempty"`
	GSMSecrets    []GSMSecretRef    `json:"gsm_secrets,omitempty"`
	DockerConfig  *DockerConfigSpec `json:"dockerconfig,omitempty"`
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
	As                   string             `json:"as"`
	Extends              string             `json:"extends,omitempty"`
	Registries           []RegistryAuthData `json:"registries"`
	AdditionalRegistries []RegistryAuthData `json:"additional_registries,omitempty"`
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
	ClusterGroups []string `json:"cluster_groups,omitempty"`
	Cluster       string   `json:"cluster,omitempty"`
	Namespace     string   `json:"namespace"`
}

// LoadGSMConfigFromFile loads a GSMConfig from a YAML file
func LoadGSMConfigFromFile(file string, config *GSMConfig) error {
	bytes, err := gzip.ReadFileMaybeGZIP(file)
	if err != nil {
		return err
	}
	return yaml.UnmarshalStrict(bytes, config)
}
