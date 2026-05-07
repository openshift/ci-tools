package util

import (
	"context"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
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

func findSpecTagReference(is *imageapi.ImageStream, tag string) (imageapi.TagReference, bool) {
	for _, specTag := range is.Spec.Tags {
		if specTag.Name == tag {
			return specTag, true
		}
	}
	return imageapi.TagReference{}, false
}

// digestOnlyResolutionAllowed follows ImportRelease tag policy semantics.
func digestOnlyResolutionAllowed(specTag imageapi.TagReference, hasSpecTag bool, policy imageapi.TagReferencePolicyType) bool {
	if policy == imageapi.LocalTagReferencePolicy {
		return false
	}
	if policy == imageapi.SourceTagReferencePolicy {
		return true
	}
	if policy != "" || !hasSpecTag {
		return false
	}
	if specTag.Reference {
		return true
	}
	return specTag.ImportPolicy.ImportMode == imageapi.ImportModePreserveOriginal
}

func sourceImportNotFailed(conditions []imageapi.TagEventCondition) bool {
	for _, c := range conditions {
		if c.Type == imageapi.ImportSuccess && c.Status == corev1.ConditionFalse {
			return false
		}
	}
	return true
}

func tagImportExplicitSuccess(conditions []imageapi.TagEventCondition) bool {
	for _, c := range conditions {
		if c.Type == imageapi.ImportSuccess && c.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

func digestPullSpecFromSpecTag(specTag imageapi.TagReference, hasSpecTag bool) string {
	if !hasSpecTag || specTag.From == nil || specTag.From.Kind != "DockerImage" {
		return ""
	}
	name := strings.TrimSpace(specTag.From.Name)
	if name == "" {
		return ""
	}
	if strings.Contains(name, "@sha256:") || strings.Contains(name, "@sha512:") {
		return name
	}
	return ""
}

// ResolvePullSpec reports whether a tag can be pulled and the pullspec to use.
func ResolvePullSpec(is *imageapi.ImageStream, tag string, requireExact bool) (string, bool, imageapi.TagEventCondition) {
	specTag, hasSpecTag := findSpecTagReference(is, tag)
	var specRefPolicy imageapi.TagReferencePolicyType
	if hasSpecTag {
		specRefPolicy = specTag.ReferencePolicy.Type
	}

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
			if requireExact && digestOnlyResolutionAllowed(specTag, hasSpecTag, specRefPolicy) &&
				tagImportExplicitSuccess(tags.Conditions) && sourceImportNotFailed(tags.Conditions) {
				if pull := digestPullSpecFromSpecTag(specTag, hasSpecTag); pull != "" {
					pullSpec = pull
					exists = true
					break
				}
			}
			break
		}
		item := tags.Items[0]
		if len(item.Image) > 0 {
			if len(is.Status.PublicDockerImageRepository) > 0 {
				pullSpec = fmt.Sprintf("%s@%s", is.Status.PublicDockerImageRepository, item.Image)
				exists = true
				break
			}
			if len(is.Status.DockerImageRepository) > 0 {
				pullSpec = fmt.Sprintf("%s@%s", is.Status.DockerImageRepository, item.Image)
				exists = true
				break
			}
		}
		if requireExact && digestOnlyResolutionAllowed(specTag, hasSpecTag, specRefPolicy) &&
			item.Image == "" && sourceImportNotFailed(tags.Conditions) {
			ref := item.DockerImageReference
			if !strings.Contains(ref, "@sha256:") && !strings.Contains(ref, "@sha512:") {
				ref = digestPullSpecFromSpecTag(specTag, hasSpecTag)
			}
			if strings.Contains(ref, "@sha256:") || strings.Contains(ref, "@sha512:") {
				pullSpec = ref
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
