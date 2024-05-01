package configresolver

import (
	"encoding/json"
	"fmt"
)

type IntegratedStream struct {
	Tags                        []string `json:"tags,omitempty"`
	ReleaseControllerConfigName string   `json:"releaseControllerConfigName"`
}

type releaseConfig struct {
	Name string `json:"name"`
}

// ReleaseControllerConfigNameToAnnotationValue converts a config name to the annotation value
func ReleaseControllerConfigNameToAnnotationValue(configName string) (string, error) {
	rc := releaseConfig{Name: configName}
	bytes, err := json.Marshal(rc)
	if err != nil {
		return "", fmt.Errorf("failed to marshal release configuration: %w", err)
	}
	return string(bytes), nil
}

// ReleaseControllerAnnotationValueToConfigName converts a annotation value to the config name
func ReleaseControllerAnnotationValueToConfigName(annotationValue string) (string, error) {
	var rc releaseConfig
	if err := json.Unmarshal([]byte(annotationValue), &rc); err != nil {
		return "", fmt.Errorf("failed to unmarshal release configuration: %w", err)
	}
	return rc.Name, nil
}
