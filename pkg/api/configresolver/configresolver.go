package configresolver

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/sirupsen/logrus"

	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	imagev1 "github.com/openshift/api/image/v1"

	"github.com/openshift/ci-tools/pkg/api"
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

// LocalIntegratedStream return the information of the given integrated stream
func LocalIntegratedStream(ctx context.Context, client ctrlruntimeclient.Client, ns, name string) (*IntegratedStream, error) {
	logrus.WithField("namespace", ns).WithField("name", name).Debug("Getting info for integrated stream")
	is := &imagev1.ImageStream{}
	if err := client.Get(ctx, ctrlruntimeclient.ObjectKey{Namespace: ns, Name: name}, is); err != nil {
		return nil, fmt.Errorf("failed to get image stream %s/%s: %w", ns, name, err)
	}
	var tags []string
	for _, tag := range is.Status.Tags {
		tags = append(tags, tag.Tag)
	}
	var releaseControllerConfigName string
	if raw, ok := is.ObjectMeta.Annotations[api.ReleaseConfigAnnotation]; ok {
		configName, err := ReleaseControllerAnnotationValueToConfigName(raw)
		if err != nil {
			return nil, fmt.Errorf("could not resolve release configuration on imagestream %s/%s: %w", ns, name, err)
		}
		releaseControllerConfigName = configName
	}
	return &IntegratedStream{Tags: tags, ReleaseControllerConfigName: releaseControllerConfigName}, nil
}
