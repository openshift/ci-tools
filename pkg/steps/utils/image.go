package utils

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/sirupsen/logrus"

	coreapi "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	imagev1 "github.com/openshift/api/image/v1"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/kubernetes"
	"github.com/openshift/ci-tools/pkg/util"
)

func ImageDigestFor(client ctrlruntimeclient.Client, namespace func() string, name, tag string) func() (string, error) {
	return func() (string, error) {
		is := &imagev1.ImageStream{}
		if err := client.Get(context.TODO(), ctrlruntimeclient.ObjectKey{Namespace: namespace(), Name: name}, is); err != nil {
			return "", fmt.Errorf("could not retrieve output imagestream: %w", err)
		}
		var registry string
		if len(is.Status.PublicDockerImageRepository) > 0 {
			registry = is.Status.PublicDockerImageRepository
		} else if len(is.Status.DockerImageRepository) > 0 {
			registry = is.Status.DockerImageRepository
		} else {
			return "", fmt.Errorf("image stream %s has no accessible image registry value", name)
		}
		ref, image := FindStatusTag(is, tag)
		if len(image) > 0 {
			return fmt.Sprintf("%s@%s", registry, image), nil
		}
		if ref == nil && findSpecTag(is, tag) == nil {
			return "", fmt.Errorf("image stream %q has no tag %q in spec or status", name, tag)
		}
		return fmt.Sprintf("%s:%s", registry, tag), nil
	}
}

func findSpecTag(is *imagev1.ImageStream, tag string) *coreapi.ObjectReference {
	for _, t := range is.Spec.Tags {
		if t.Name != tag {
			continue
		}
		return t.From
	}
	return nil
}

// FindStatusTag returns an object reference to a tag if
// it exists in the ImageStream's Spec
func FindStatusTag(is *imagev1.ImageStream, tag string) (*coreapi.ObjectReference, string) {
	for _, t := range is.Status.Tags {
		if t.Tag != tag {
			continue
		}
		if len(t.Items) == 0 {
			return nil, ""
		}
		if len(t.Items[0].Image) == 0 {
			return &coreapi.ObjectReference{
				Kind: "DockerImage",
				Name: t.Items[0].DockerImageReference,
			}, ""
		}
		return &coreapi.ObjectReference{
			Kind:      "ImageStreamImage",
			Namespace: is.Namespace,
			Name:      fmt.Sprintf("%s@%s", is.Name, t.Items[0].Image),
		}, t.Items[0].Image
	}
	return nil, ""
}

const DefaultImageImportTimeout = 45 * time.Minute

func getEvaluator(ctx context.Context, client ctrlruntimeclient.Client, ns, name string, tags sets.Set[string]) func(obj runtime.Object) (bool, error) {
	return func(obj runtime.Object) (bool, error) {
		switch stream := obj.(type) {
		case *imagev1.ImageStream:
			for i, tag := range stream.Spec.Tags {
				if tags.Len() > 0 && !tags.Has(tag.Name) {
					continue
				}
				_, exist, condition := util.ResolvePullSpec(stream, tag.Name, true)
				if !exist {
					logrus.WithField("conditionMessage", condition.Message).Debugf("Waiting to import tag[%d] on imagestream %s/%s:%s ...", i, stream.Namespace, stream.Name, tag.Name)
					if strings.Contains(condition.Message, "Internal error occurred") {
						if tag.From == nil {
							// should never happen
							return false, fmt.Errorf("failed to determine the source of the tag %s/%s:%s", stream.Namespace, stream.Name, tag.Name)
						}
						if tag.From.Kind != "DockerImage" {
							// should never happen
							return false, fmt.Errorf("failed to import tag %s/%s:%s from an unexpected tag source %v", stream.Namespace, stream.Name, tag.Name, *tag.From)
						}
						if tag.From.Name == "" {
							// should never happen
							return false, fmt.Errorf("failed to import tag %s/%s:%s from an empty source", stream.Namespace, stream.Name, tag.Name)
						}
						if _, err := ImportTagWithRetries(ctx, client, ns, name, tag.Name, tag.From.Name, api.ImageStreamImportRetries); err != nil {
							return false, fmt.Errorf("failed to reimport the tag %s/%s:%s: %w", stream.Namespace, stream.Name, tag.Name, err)
						}
					}
					return false, nil
				}
			}
			return true, nil
		default:
			return false, fmt.Errorf("imagestream %s/%s got an event that did not contain an imagestream: %v", ns, name, obj)
		}
	}
}

// WaitForImportingISTag waits for the tags on the image stream are imported
func WaitForImportingISTag(ctx context.Context, client ctrlruntimeclient.WithWatch, ns, name string, into *imagev1.ImageStream, tags sets.Set[string], timeout time.Duration) error {
	obj := into
	if obj == nil {
		obj = &imagev1.ImageStream{}
	}
	return kubernetes.WaitForConditionOnObject(ctx, client, ctrlruntimeclient.ObjectKey{Namespace: ns, Name: name}, &imagev1.ImageStreamList{}, obj, getEvaluator(ctx, client, ns, name, tags), timeout)
}

// ImportTagWithRetries imports image with retries
func ImportTagWithRetries(ctx context.Context, client ctrlruntimeclient.Client, ns, name, tag, sourcePullSpec string, retries int) (string, error) {
	if sourcePullSpec == "" {
		return "", fmt.Errorf("sourcePullSpec cannot be empty")
	}
	var pullSpec string
	step := 0
	logger := logrus.WithField("tag", fmt.Sprintf(" %s/%s:%s", ns, name, tag)).WithField("sourcePullSpec", sourcePullSpec)
	if err := wait.ExponentialBackoff(wait.Backoff{Steps: retries, Duration: 1 * time.Second, Factor: 2}, func() (bool, error) {
		logger.WithField("step", step).Debug("Retrying importing tag ...")
		streamImport := &imagev1.ImageStreamImport{
			ObjectMeta: meta.ObjectMeta{
				Namespace: ns,
				Name:      name,
			},
			Spec: imagev1.ImageStreamImportSpec{
				Import: true,
				Images: []imagev1.ImageImportSpec{
					{
						To: &coreapi.LocalObjectReference{
							Name: tag,
						},
						From: coreapi.ObjectReference{
							Kind: "DockerImage",
							Name: sourcePullSpec,
						},
						ImportPolicy:    imagev1.TagImportPolicy{ImportMode: imagev1.ImportModePreserveOriginal},
						ReferencePolicy: imagev1.TagReferencePolicy{Type: imagev1.LocalTagReferencePolicy},
					},
				},
			},
		}
		step = step + 1
		if err := client.Create(ctx, streamImport); err != nil {
			if kerrors.IsConflict(err) {
				logger.WithField("step", step-1).Debug("Unable to create image stream import up to conflicts")
				return false, nil
			}
			if kerrors.IsForbidden(err) {
				logger.WithField("step", step-1).Debug("Unable to create image stream import up to permissions")
				return false, nil
			}
			return false, err
		}
		if len(streamImport.Status.Images) == 0 {
			logger.WithField("step", step-1).Debug("Imports' status has no images")
			return false, nil
		}
		image := streamImport.Status.Images[0]
		if image.Image == nil {
			logger.WithField("step", step-1).Debug("Imports' status' image is nil")
			return false, nil
		}
		pullSpec = image.Image.DockerImageReference
		logrus.Debugf("Imported tag %s/%s:%s at import (%d)", ns, name, tag, step-1)
		return true, nil
	}); err != nil {
		if err == wait.ErrorInterrupted(err) {
			var conditionMsg string
			imagestream := imagev1.ImageStream{}
			if err := client.Get(ctx, ctrlruntimeclient.ObjectKey{Namespace: ns, Name: name}, &imagestream); err != nil {
				logger.WithError(err).Debug("Failed to get image stream for the tag")
			} else {
				for _, t := range imagestream.Status.Tags {
					if t.Tag == tag {
						if len(t.Conditions) > 0 {
							conditionMsg = t.Conditions[0].Message
						}
						break
					}
				}
			}
			if conditionMsg == "" {
				return "", fmt.Errorf("unable to import tag %s/%s:%s even after (%d) imports: %w", ns, name, tag, step, err)
			} else {
				return "", fmt.Errorf("unable to import tag %s/%s:%s with message %s on the image stream even after (%d) imports: %w", ns, name, tag, conditionMsg, step, err)
			}
		}
		return "", fmt.Errorf("unable to import tag %s/%s:%s at import (%d): %w", ns, name, tag, step-1, err)
	}
	return pullSpec, nil
}
