package util

import (
	"context"
	"fmt"
	"strings"

	kerrors "k8s.io/apimachinery/pkg/api/errors"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	imageapi "github.com/openshift/api/image/v1"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/metrics"
)

// ParseImageStreamTagReference creates a reference from an "is:tag" string.
func ParseImageStreamTagReference(s string) (api.ImageStreamTagReference, error) {
	var ret api.ImageStreamTagReference
	i := strings.LastIndex(s, ":")
	if i == -1 {
		return ret, fmt.Errorf("invalid ImageStreamTagReference: %s", s)
	}
	ret.Name = s[:i]
	ret.Tag = s[i+1:]
	if strings.Contains(ret.Name, ":") {
		return ret, fmt.Errorf("invalid ImageStreamTagReference: %s", s)
	}
	return ret, nil
}

// ResolvePullSpec if a tag of an imagestream is resolved
func ResolvePullSpec(is *imageapi.ImageStream, tag string, requireExact bool) (string, bool, imageapi.TagEventCondition) {
	var condition imageapi.TagEventCondition
	var pullSpec string
	var exists bool

	for _, tags := range is.Status.Tags {
		if tags.Tag != tag {
			continue
		}
		if conditions := tags.Conditions; len(conditions) > 0 {
			condition = conditions[0]
		}
		if len(tags.Items) == 0 {
			break
		}
		if image := tags.Items[0].Image; len(image) > 0 {
			if len(is.Status.PublicDockerImageRepository) > 0 {
				pullSpec = fmt.Sprintf("%s@%s", is.Status.PublicDockerImageRepository, image)
				exists = true
				break
			}
			if len(is.Status.DockerImageRepository) > 0 {
				pullSpec = fmt.Sprintf("%s@%s", is.Status.DockerImageRepository, image)
				exists = true
				break
			}
		}
		break
	}

	if !exists && !requireExact {
		if len(is.Status.PublicDockerImageRepository) > 0 {
			pullSpec = fmt.Sprintf("%s:%s", is.Status.PublicDockerImageRepository, tag)
			exists = true
		} else if len(is.Status.DockerImageRepository) > 0 {
			pullSpec = fmt.Sprintf("%s:%s", is.Status.DockerImageRepository, tag)
			exists = true
		}
	}

	return pullSpec, exists, condition
}

// CreateImageStreamWithMetrics creates an ImageStream and records the appropriate metrics event
func CreateImageStreamWithMetrics(ctx context.Context, client ctrlruntimeclient.Client, imageStream *imageapi.ImageStream, metricsAgent *metrics.MetricsAgent) (*imageapi.ImageStream, error) {
	created := true

	if err := client.Create(ctx, imageStream); err != nil {
		if !kerrors.IsAlreadyExists(err) {
			metricsAgent.Record(&metrics.ImageStreamEvent{
				Namespace:       imageStream.Namespace,
				ImageStreamName: imageStream.Name,
				FullName:        imageStream.Namespace + "/" + imageStream.Name,
				Success:         false,
				Error:           err.Error(),
			})
			return nil, fmt.Errorf("failed to create imagestream %s/%s: %w", imageStream.Namespace, imageStream.Name, err)
		}
		created = false // ImageStream already existed, we just retrieved it
		if err := client.Get(ctx, ctrlruntimeclient.ObjectKey{Namespace: imageStream.Namespace, Name: imageStream.Name}, imageStream); err != nil {
			metricsAgent.Record(&metrics.ImageStreamEvent{
				Namespace:       imageStream.Namespace,
				ImageStreamName: imageStream.Name,
				FullName:        imageStream.Namespace + "/" + imageStream.Name,
				Success:         false,
				Error:           err.Error(),
			})
			return nil, fmt.Errorf("failed to get existing imagestream %s/%s: %w", imageStream.Namespace, imageStream.Name, err)
		}
	}

	metricsAgent.Record(&metrics.ImageStreamEvent{
		Namespace:         imageStream.Namespace,
		ImageStreamName:   imageStream.Name,
		FullName:          imageStream.Namespace + "/" + imageStream.Name,
		Success:           true,
		AdditionalContext: map[string]any{"created": created},
	})
	return imageStream, nil
}
