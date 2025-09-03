package yaml

import (
	"errors"
	"fmt"
	"strings"

	jsonpatch "gopkg.in/evanphx/json-patch.v5"

	"sigs.k8s.io/yaml"
)

type ApplyPatchOption func(o *applyPatchOptions)

func IgnoreMissingKeyOnRemove() ApplyPatchOption {
	return func(o *applyPatchOptions) { o.ignoreMissingKeyErr = true }
}

type applyPatchOptions struct {
	ignoreMissingKeyErr bool
}

type Patch struct {
	bytes     []byte
	patchType int
}

const (
	jsonMergePatch int = iota
	jsonPatch
)

func ApplyPatch(yamlBytes []byte, patch Patch, opts ...ApplyPatchOption) ([]byte, error) {
	patchOpts := applyPatchOptions{}
	for _, applyOpt := range opts {
		applyOpt(&patchOpts)
	}

	jsonPatchBytes, err := yaml.YAMLToJSON(patch.bytes)
	if err != nil {
		return nil, fmt.Errorf("patch yaml to json: %w", err)
	}

	jsonBytes, err := yaml.YAMLToJSON(yamlBytes)
	if err != nil {
		return nil, fmt.Errorf("yaml to json: %w", err)
	}

	var jsonPatched []byte
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

		// Split the patch into operations in order to catch which one of those
		// may fail due to a missing key. This wouldn't be needed under normal
		// circumstances.
		operations := []jsonpatch.Operation(decoded)
		for _, op := range operations {
			p := jsonpatch.Patch([]jsonpatch.Operation{op})
			jsonPatched, err = p.Apply(jsonBytes)
			if err != nil {
				if patchOpts.ignoreMissingKeyErr && errors.Is(err, jsonpatch.ErrMissing) &&
					strings.Contains(err.Error(), "Unable to remove nonexistent key") {
					jsonPatched = jsonBytes
				} else {
					return nil, err
				}
			}
		}
	default:
		return nil, fmt.Errorf("unsupported patch type %d", patch.patchType)
	}

	yamlPatched, err := yaml.JSONToYAML(jsonPatched)
	if err != nil {
		return nil, fmt.Errorf("json to yaml: %w", err)
	}
	return yamlPatched, nil
}

func JsonMergePatch(p []byte) Patch {
	return Patch{bytes: p, patchType: jsonMergePatch}
}

func JsonPatch(p []byte) Patch {
	return Patch{bytes: p, patchType: jsonPatch}
}
