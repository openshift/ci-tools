package steps

import (
	"encoding/json"
	"fmt"

	imageapi "github.com/openshift/api/image/v1"
	imageclientset "github.com/openshift/client-go/image/clientset/versioned/typed/image/v1"
	coreapi "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func createImageStreamWithTag(
	isClient imageclientset.ImageStreamsGetter,
	istClient imageclientset.ImageStreamTagsGetter,
	fromNamespace, fromName, fromTag,
	toNamespace, toName, toTag string,
	dry bool,
) error {
	fromImage := "dry-fake"
	if !dry {
		from, err := istClient.ImageStreamTags(fromNamespace).Get(fmt.Sprintf("%s:%s", fromName, fromTag), meta.GetOptions{})
		if err != nil {
			return fmt.Errorf("could not resolve base image: %v", err)
		}
		fromImage = from.Image.Name
	}

	is := &imageapi.ImageStream{
		ObjectMeta: meta.ObjectMeta{
			Name:      toName,
			Namespace: toNamespace,
		},
	}

	ist := &imageapi.ImageStreamTag{
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
				Name:      fmt.Sprintf("%s@%s", fromName, fromImage),
				Namespace: fromNamespace,
			},
		},
	}

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

	_, err := isClient.ImageStreams(is.Namespace).Get(is.Name, meta.GetOptions{})
	if errors.IsNotFound(err) {
		_, err = isClient.ImageStreams(is.Namespace).Create(is)
	}
	if err != nil {
		return fmt.Errorf("could not retrieve target imagestream: %v", err)
	}

	if err = istClient.ImageStreamTags(ist.Namespace).Delete(ist.Name, nil); err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("could not remove output imagestreamtag: %v", err)
	}
	_, err = istClient.ImageStreamTags(ist.Namespace).Create(ist)
	if err != nil {
		return fmt.Errorf("could not create output imagestreamtag: %v", err)
	}
	return nil
}

func imagesStreamTagExists(istClient imageclientset.ImageStreamTagsGetter, namespace, name, tag string) (bool, error) {
	istName := fmt.Sprintf("%s:%s", name, tag)
	if _, err := istClient.ImageStreamTags(namespace).Get(istName, meta.GetOptions{}); err != nil {
		if errors.IsNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("could not retrieve output imagestreamtag %s: %v", istName, err)
	}
	return true, nil
}
