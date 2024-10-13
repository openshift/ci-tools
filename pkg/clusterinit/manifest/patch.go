package manifest

import (
	"bytes"
	"fmt"
	"regexp"

	"github.com/openshift/ci-tools/pkg/util/yaml"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	kyaml "sigs.k8s.io/yaml"
)

type PatchType string

const (
	JsonMerge PatchType = "json-merge"
	JsonPatch PatchType = "json-patch"
)

type Patch struct {
	Type    PatchType   `json:"type,omitempty"`
	Matches []Match     `json:"matches,omitempty"`
	Inline  interface{} `json:"inline,omitempty"`
}

type Match struct {
	Kind      string            `json:"kind,omitempty"`
	Name      string            `json:"name,omitempty"`
	Namespace string            `json:"namespace,omitempty"`
	Labels    map[string]string `json:"labels,omitempty"`
}

func shouldApplyPatch(manifest map[string]interface{}, patch Patch) (bool, error) {
	if len(patch.Matches) == 0 {
		return true, nil
	}

	u := unstructured.Unstructured{Object: manifest}
	match := func(m *Match) (bool, error) {
		if m.Kind != "" {
			if m.Kind != u.GetKind() {
				return false, nil
			}
		}
		if m.Namespace != "" {
			if matched, err := regexp.Match(m.Namespace, []byte(u.GetNamespace())); err != nil {
				return false, fmt.Errorf("match namespace: %w", err)
			} else if !matched {
				return matched, nil
			}
		}
		if m.Name != "" {
			if matched, err := regexp.Match(m.Name, []byte(u.GetName())); err != nil {
				return false, fmt.Errorf("match name: %w", err)
			} else if !matched {
				return matched, nil
			}
		}
		if len(m.Labels) > 0 {
			labels := u.GetLabels()
			for k, v := range labels {
				ll, ok := labels[k]
				if !ok || ll != v {
					return false, nil
				}
			}
		}
		return true, nil
	}

	for i := range patch.Matches {
		match, err := match(&patch.Matches[i])
		if err != nil {
			return false, err
		}
		if match {
			return match, nil
		}
	}

	return false, nil
}

func applyPatch(manifest []byte, patch Patch) ([]byte, error) {
	t := patch.Type
	if t == "" || t == JsonMerge {
		patchBytes, err := kyaml.Marshal(patch.Inline)
		if err != nil {
			return nil, fmt.Errorf("marshal patch: %w", err)
		}
		return yaml.ApplyPatch(manifest, yaml.JsonMergePatch(patchBytes))
	}
	if t == JsonPatch {
		patchBytes, err := kyaml.Marshal(patch.Inline)
		if err != nil {
			return nil, fmt.Errorf("marshal patch: %w", err)
		}
		return yaml.ApplyPatch(manifest, yaml.JsonPatch(patchBytes))
	}
	return nil, fmt.Errorf("unsupported patch type %s", patch.Type)
}

func Marshal(manifests []interface{}, patches []Patch) ([]byte, error) {
	manifestsBytes := make([][]byte, 0, len(manifests))
	for _, manifest := range manifests {
		manifestBytes, err := kyaml.Marshal(manifest)
		if err != nil {
			return nil, fmt.Errorf("marshal: %w", err)
		}
		manifestMap, ok := manifest.(map[string]interface{})
		if !ok {
			manifestsBytes = append(manifestsBytes, manifestBytes)
			continue
		}

		for _, patch := range patches {
			apply, err := shouldApplyPatch(manifestMap, patch)
			if err != nil {
				return nil, fmt.Errorf("should apply patch: %w", err)
			}
			if !apply {
				continue
			}
			manifestBytesPatched, err := applyPatch(manifestBytes, patch)
			if err != nil {
				return nil, fmt.Errorf("apply patch: %w", err)
			}
			manifestBytes = manifestBytesPatched
		}
		manifestsBytes = append(manifestsBytes, manifestBytes)
	}

	return bytes.Join(manifestsBytes, []byte("---\n")), nil
}
