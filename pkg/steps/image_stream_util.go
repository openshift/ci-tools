package steps

import (
	"encoding/json"
	"fmt"

	imageapi "github.com/openshift/api/image/v1"
	imageclientset "github.com/openshift/client-go/image/clientset/versioned/typed/image/v1"
	coreapi "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
)

func createImageStreamWithTag(
	isClient imageclientset.ImageStreamsGetter,
	istClient imageclientset.ImageStreamTagsGetter,
	is *imageapi.ImageStream,
	ist *imageapi.ImageStreamTag,
	dry bool,
) error {
	if dry {
		isJSON, err := json.MarshalIndent(is, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to marshal imagestream: %v", err)
		}
		fmt.Printf("%s\n", isJSON)

		istJSON, err := json.MarshalIndent(ist, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to marshal imagestreamtag: %v", err)
		}
		fmt.Printf("%s\n", istJSON)
		return nil
	}

	_, err := isClient.ImageStreams(is.Namespace).Create(is)
	if err != nil && !errors.IsAlreadyExists(err) {
		return fmt.Errorf("could not create target imagestream: %v", err)
	}

	// Create if not exists, update if it does
	if _, err := istClient.ImageStreamTags(ist.Namespace).Create(ist); err != nil {
		if errors.IsAlreadyExists(err) {
			err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
				existingIst, err := istClient.ImageStreamTags(ist.Namespace).Get(ist.Name, meta.GetOptions{})
				if err != nil {
					return err
				}
				// We don't care about the existing imagestreamtag's state, we just
				// want it to look like the new one, so we only copy the
				// ResourceVersion so we can update it.
				ist.ResourceVersion = existingIst.ResourceVersion
				if _, err = istClient.ImageStreamTags(ist.Namespace).Update(ist); err != nil {
					return err
				}
				return nil
			})
			if err != nil {
				return fmt.Errorf("could not update output imagestreamtag: %v", err)
			}
		} else {
			return fmt.Errorf("could not create output imagestreamtag: %v", err)
		}
	}
	return nil
}

func newImageStream(namespace, name string) *imageapi.ImageStream {
	return &imageapi.ImageStream{
		ObjectMeta: meta.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}
}

func newImageStreamTag(
	fromNamespace, fromName, fromTag,
	toNamespace, toName, toTag string,
) *imageapi.ImageStreamTag {
	return &imageapi.ImageStreamTag{
		ObjectMeta: meta.ObjectMeta{
			Name:      fmt.Sprintf("%s:%s", toName, toTag),
			Namespace: toNamespace,
		},
		Tag: &imageapi.TagReference{
			ReferencePolicy: imageapi.TagReferencePolicy{
				Type: imageapi.LocalTagReferencePolicy,
			},
			From: &coreapi.ObjectReference{
				Kind:      "ImageStreamImage",
				Name:      fmt.Sprintf("%s@%s", fromName, fromTag),
				Namespace: fromNamespace,
			},
		},
	}
}
