package utils

import (
	"context"
	"fmt"

	coreapi "k8s.io/api/core/v1"
	v1 "k8s.io/test-infra/prow/apis/prowjobs/v1"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	imagev1 "github.com/openshift/api/image/v1"
	"github.com/openshift/ci-tools/pkg/api"
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

func ImageForRef(ref v1.Refs, baseTag string) string {
	return fmt.Sprintf("%s-%s.%s", baseTag, ref.Org, ref.Repo)
}

func ExtraRefForMetadata(refs []v1.Refs, metadata api.Metadata) v1.Refs {
	for _, extraRef := range refs {
		if extraRef.Repo == metadata.Repo && extraRef.Org == metadata.Org {
			return extraRef
		}
	}
	// in case no refs match the repo based on metadata, we need a fallback
	return refs[0]
}
