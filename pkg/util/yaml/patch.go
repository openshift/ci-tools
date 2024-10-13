package yaml

import (
	"fmt"

	jsonpatch "gopkg.in/evanphx/json-patch.v5"
	"sigs.k8s.io/yaml"
)

type Patch struct {
	bytes     []byte
	patchType int
}

const (
	jsonMergePatch int = iota
	jsonPatch
)

func ApplyPatch(yamlBytes []byte, patch Patch) ([]byte, error) {
	jsonPatchBytes, err := yaml.YAMLToJSON(patch.bytes)
	if err != nil {
		return nil, fmt.Errorf("patch yaml to json: %s", err)
	}

	jsonBytes, err := yaml.YAMLToJSON(yamlBytes)
	if err != nil {
		return nil, fmt.Errorf("yaml to json: %s", err)
	}

	jsonPatched := []byte{}
	switch patch.patchType {
	case jsonMergePatch:
		jsonPatched, err = jsonpatch.MergePatch(jsonBytes, jsonPatchBytes)
		if err != nil {
			return nil, err
		}
	case jsonPatch:
		decoded, err := jsonpatch.DecodePatch(jsonPatchBytes)
		if err != nil {
			return nil, err
		}
		jsonPatched, err = decoded.Apply(jsonBytes)
		if err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("unsupported patch type %d", patch.patchType)
	}

	yamlPatched, err := yaml.JSONToYAML(jsonPatched)
	if err != nil {
		return nil, fmt.Errorf("json to yaml: %s", err)
	}
	return yamlPatched, nil
}

func JsonMergePatch(p []byte) Patch {
	return Patch{bytes: p, patchType: jsonMergePatch}
}

func JsonPatch(p []byte) Patch {
	return Patch{bytes: p, patchType: jsonPatch}
}
