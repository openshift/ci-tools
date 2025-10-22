package imagestreamtagwrapper

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"

	"github.com/docker/distribution/manifest/schema1"
	"github.com/docker/distribution/manifest/schema2"

	kapierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	cache "sigs.k8s.io/controller-runtime/pkg/cache"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	imagegroup "github.com/openshift/api/image"
	imagev1 "github.com/openshift/api/image/v1"
	"github.com/openshift/library-go/pkg/image/imageutil"
	"github.com/openshift/library-go/pkg/image/reference"
	imageapi "github.com/openshift/openshift-apiserver/pkg/image/apis/image"
	dockerapi10 "github.com/openshift/openshift-apiserver/pkg/image/apis/image/docker10"
)

// New returns a new imagestreamtagwrapper. Only use with a caching client
// as upstream, as it has to fetch multiple objects in order to construct
// an imagestreamtag, which is more expensive than just getting it directly
// when not using a cache.
func New(upstream ctrlruntimeclient.Client, cache cache.Cache) (ctrlruntimeclient.Client, error) {
	// Allocate the informers already so they are synced during startup not on first request
	if _, err := cache.GetInformer(context.TODO(), &imagev1.Image{}); err != nil {
		return nil, fmt.Errorf("failed to get informer for image: %w", err)
	}
	if _, err := cache.GetInformer(context.TODO(), &imagev1.ImageStream{}); err != nil {
		return nil, fmt.Errorf("failed to get informer for imagestream: %w", err)
	}
	return &imagestreamtagwrapper{Client: upstream}, nil
}

// MustNew panics when there was an error during initialisation
func MustNew(upstream ctrlruntimeclient.Client, cache cache.Cache) ctrlruntimeclient.Client {
	client, err := New(upstream, cache)
	if err != nil {
		panic(err.Error())
	}
	return client
}

type imagestreamtagwrapper struct {
	ctrlruntimeclient.Client
}

func (istw *imagestreamtagwrapper) Get(ctx context.Context, key ctrlruntimeclient.ObjectKey, obj ctrlruntimeclient.Object, opts ...ctrlruntimeclient.GetOption) error {
	if imageStreamTag, isImageStreamTag := obj.(*imagev1.ImageStreamTag); isImageStreamTag {
		return istw.assembleImageStreamTag(ctx, key, imageStreamTag)
	}
	return istw.Client.Get(ctx, key, obj)
}

// Essentially an inlined copy of the server-side logic at
// https://github.com/openshift/openshift-apiserver/blob/08cd9ea9ee6b97397761b84f428882ed9e6d9e67/pkg/image/apiserver/registry/imagestreamtag/rest.go#L118
func (istw *imagestreamtagwrapper) assembleImageStreamTag(ctx context.Context, key ctrlruntimeclient.ObjectKey, ist *imagev1.ImageStreamTag) error {
	name, tag, err := nameAndTag(key.Name)
	if err != nil {
		return err
	}
	imageStream := &imagev1.ImageStream{}
	if err := istw.Get(ctx, ctrlruntimeclient.ObjectKey{Namespace: key.Namespace, Name: name}, imageStream); err != nil {
		return err
	}

	image, err := istw.imageFor(ctx, tag, imageStream)
	if err != nil {
		if !kapierrors.IsNotFound(err) {
			return err
		}
		image = nil
	}

	return newISTag(tag, imageStream, image, false, ist)
}

// nameAndTag splits a string into its name component and tag component, and returns an error
// if the string is not in the right form.
func nameAndTag(id string) (name string, tag string, err error) {
	name, tag, err = imageutil.ParseImageStreamTagName(id)
	if err != nil {
		err = kapierrors.NewBadRequest("ImageTags must be retrieved with <name>:<tag>")
	}
	return
}

func (istw *imagestreamtagwrapper) imageFor(ctx context.Context, tag string, imageStream *imagev1.ImageStream) (*imagev1.Image, error) {
	event := latestTaggedImage(imageStream, tag)
	if event == nil || len(event.Image) == 0 {
		return nil, kapierrors.NewNotFound(imagegroup.Resource("imagetags"), imageutil.JoinImageStreamTag(imageStream.Name, tag))
	}

	image := &imagev1.Image{}
	return image, istw.Get(ctx, ctrlruntimeclient.ObjectKey{Name: event.Image}, image)
}

func latestTaggedImage(stream *imagev1.ImageStream, tag string) *imagev1.TagEvent {
	if len(tag) == 0 {
		tag = imagev1.DefaultImageTag
	}
	// find the most recent tag event with an image reference
	for _, namedTagEvent := range stream.Status.Tags {
		if namedTagEvent.Tag == tag {
			if len(namedTagEvent.Items) == 0 {
				return nil
			} else {
				return namedTagEvent.Items[0].DeepCopy()
			}
		}
	}

	return nil
}

func tagByNameFromTagEventList(tag string, list []imagev1.NamedTagEventList) imagev1.NamedTagEventList {
	for _, item := range list {
		if item.Tag == tag {
			return item
		}
	}
	return imagev1.NamedTagEventList{}
}

func tagReferenceByNameFromList(tag string, list []imagev1.TagReference) (imagev1.TagReference, bool) {
	for _, item := range list {
		if item.Name == tag {
			return item, true
		}
	}
	return imagev1.TagReference{}, false
}

// newISTag initializes an image stream tag from an image stream and image. The allowEmptyEvent will create a tag even
// in the event that the status tag does does not exist yet (no image has successfully been tagged) or the image is nil.
func newISTag(tag string, imageStream *imagev1.ImageStream, image *imagev1.Image, allowEmptyEvent bool, ist *imagev1.ImageStreamTag) error {
	istagName := imageutil.JoinImageStreamTag(imageStream.Name, tag)

	event := latestTaggedImage(imageStream, tag)
	if event == nil || len(event.Image) == 0 {
		if !allowEmptyEvent {
			return kapierrors.NewNotFound(imagegroup.Resource("imagestreamtags"), istagName)
		}
		event = &imagev1.TagEvent{
			Created: imageStream.CreationTimestamp,
		}
	}

	*ist = imagev1.ImageStreamTag{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:         imageStream.Namespace,
			Name:              istagName,
			CreationTimestamp: event.Created,
			Annotations:       map[string]string{},
			Labels:            imageStream.Labels,
			ResourceVersion:   imageStream.ResourceVersion,
			SelfLink:          fmt.Sprintf("/apis/image.openshift.io/v1/namespaces/%s/imagestreamtags/%s", imageStream.Namespace, url.QueryEscape(istagName)),
			UID:               imageStream.UID,
		},
		Generation: event.Generation,
		Conditions: tagByNameFromTagEventList(tag, imageStream.Status.Tags).Conditions,

		LookupPolicy: imageStream.Spec.LookupPolicy,
	}

	if imageStream.Spec.Tags != nil {
		if tagRef, ok := tagReferenceByNameFromList(tag, imageStream.Spec.Tags); ok {
			// copy the spec tag
			ist.Tag = &tagRef
			if from := ist.Tag.From; from != nil {
				copied := *from
				ist.Tag.From = &copied
			}
			if gen := ist.Tag.Generation; gen != nil {
				copied := *gen
				ist.Tag.Generation = &copied
			}

			// if the imageStream has Spec.Tags[tag].Annotations[k] = v, copy it to the image's annotations
			// and add them to the istag's annotations
			if image != nil && image.Annotations == nil {
				image.Annotations = make(map[string]string)
			}
			for k, v := range tagRef.Annotations {
				ist.Annotations[k] = v
				if image != nil {
					image.Annotations[k] = v
				}
			}
		}
	}

	if image != nil {
		if err := internalImageWithMetadata(image); err != nil {
			return err
		}
		image.APIVersion = ""
		image.Kind = ""
		image.SelfLink = ""
		image.DockerImageManifest = ""
		image.DockerImageConfig = ""
		ist.Image = *image
	} else {
		ist.Image = imagev1.Image{}
		ist.Image.Name = event.Image
	}

	ist.Image.DockerImageReference = resolveReferenceForTagEvent(imageStream, tag, event)
	return nil
}

// ResolveReferenceForTagEvent applies the tag reference rules for a stream, tag, and tag event for
// that tag.
func resolveReferenceForTagEvent(stream *imagev1.ImageStream, tag string, latest *imagev1.TagEvent) string {
	// retrieve spec policy - if not found, we use the latest spec
	ref, ok := tagReferenceByNameFromList(tag, stream.Spec.Tags)
	if !ok {
		return latest.DockerImageReference
	}

	switch ref.ReferencePolicy.Type {
	// the local reference policy attempts to use image pull through on the integrated
	// registry if possible
	case imagev1.LocalTagReferencePolicy:
		local := stream.Status.DockerImageRepository
		if len(local) == 0 || len(latest.Image) == 0 {
			// fallback to the originating reference if no local container image registry defined or we
			// lack an image ID
			return latest.DockerImageReference
		}

		ref, err := reference.Parse(local)
		if err != nil {
			// fallback to the originating reference if the reported local repository spec is not valid
			return latest.DockerImageReference
		}

		// create a local pullthrough URL
		ref.Tag = ""
		ref.ID = latest.Image
		return ref.Exact()

	// the default policy is to use the originating image
	default:
		return latest.DockerImageReference
	}
}

// InternalImageWithMetadata mutates the given image. It parses raw DockerImageManifest data stored in the image and
// fills its DockerImageMetadata and other fields.
func internalImageWithMetadata(image *imagev1.Image) error {
	if len(image.DockerImageManifest) == 0 {
		return nil
	}

	reorderImageLayers(image)

	if len(image.DockerImageLayers) > 0 && image.DockerImageMetadata.Size() > 0 && len(image.DockerImageManifestMediaType) > 0 {
		return nil
	}

	manifest := dockerapi10.DockerImageManifest{}
	if err := json.Unmarshal([]byte(image.DockerImageManifest), &manifest); err != nil {
		return err
	}

	err := fillImageLayers(image, manifest)
	if err != nil {
		return err
	}

	imageDockerImageMetadata := imageapi.DockerImage{}
	switch manifest.SchemaVersion {
	case 1:
		image.DockerImageManifestMediaType = schema1.MediaTypeManifest

		if len(manifest.History) == 0 {
			// It should never have an empty history, but just in case.
			return fmt.Errorf("the image %s (%s) has a schema 1 manifest, but it doesn't have history", image.Name, image.DockerImageReference)
		}

		v1Metadata := dockerapi10.DockerV1CompatibilityImage{}
		if err := json.Unmarshal([]byte(manifest.History[0].DockerV1Compatibility), &v1Metadata); err != nil {
			return err
		}

		if err := imageapi.Convert_compatibility_to_api_DockerImage(&v1Metadata, &imageDockerImageMetadata); err != nil {
			return err
		}
	case 2:
		image.DockerImageManifestMediaType = schema2.MediaTypeManifest

		if len(image.DockerImageConfig) == 0 {
			return fmt.Errorf("dockerImageConfig must not be empty for manifest schema 2")
		}

		config := dockerapi10.DockerImageConfig{}
		if err := json.Unmarshal([]byte(image.DockerImageConfig), &config); err != nil {
			return fmt.Errorf("failed to parse dockerImageConfig: %w", err)
		}

		if err := imageapi.Convert_imageconfig_to_api_DockerImage(&config, &imageDockerImageMetadata); err != nil {
			return err
		}
		imageDockerImageMetadata.ID = manifest.Config.Digest

	default:
		return fmt.Errorf("unrecognized container image manifest schema %d for %q (%s)", manifest.SchemaVersion, image.Name, image.DockerImageReference)
	}

	layerSet := sets.New[string]()
	if manifest.SchemaVersion == 2 {
		layerSet.Insert(manifest.Config.Digest)
		imageDockerImageMetadata.Size = int64(len(image.DockerImageConfig))
	} else {
		imageDockerImageMetadata.Size = 0
	}
	for _, layer := range image.DockerImageLayers {
		if layerSet.Has(layer.Name) {
			continue
		}
		layerSet.Insert(layer.Name)
		imageDockerImageMetadata.Size += layer.LayerSize
	}
	raw, err := json.Marshal(imageDockerImageMetadata)
	if err != nil {
		return err
	}
	image.DockerImageMetadata = runtime.RawExtension{Raw: raw}

	return nil
}

func reorderImageLayers(image *imagev1.Image) {
	if len(image.DockerImageLayers) == 0 {
		return
	}

	layersOrder, ok := image.Annotations[imagev1.DockerImageLayersOrderAnnotation]
	if !ok {
		switch image.DockerImageManifestMediaType {
		case schema1.MediaTypeManifest, schema1.MediaTypeSignedManifest:
			layersOrder = imagev1.DockerImageLayersOrderAscending
		case schema2.MediaTypeManifest:
			layersOrder = imagev1.DockerImageLayersOrderDescending
		default:
			return
		}
	}

	if layersOrder == imagev1.DockerImageLayersOrderDescending {
		// reverse order of the layers (lowest = 0, highest = i)
		for i, j := 0, len(image.DockerImageLayers)-1; i < j; i, j = i+1, j-1 {
			image.DockerImageLayers[i], image.DockerImageLayers[j] = image.DockerImageLayers[j], image.DockerImageLayers[i]
		}
	}

	if image.Annotations == nil {
		image.Annotations = map[string]string{}
	}

	image.Annotations[imagev1.DockerImageLayersOrderAnnotation] = imagev1.DockerImageLayersOrderAscending
}

func fillImageLayers(image *imagev1.Image, manifest dockerapi10.DockerImageManifest) error {
	if len(image.DockerImageLayers) != 0 {
		// DockerImageLayers is already filled by the registry.
		return nil
	}

	switch manifest.SchemaVersion {
	case 1:
		if len(manifest.History) != len(manifest.FSLayers) {
			return fmt.Errorf("the image %s (%s) has mismatched history and fslayer cardinality (%d != %d)", image.Name, image.DockerImageReference, len(manifest.History), len(manifest.FSLayers))
		}

		image.DockerImageLayers = make([]imagev1.ImageLayer, len(manifest.FSLayers))
		for i, obj := range manifest.History {
			layer := manifest.FSLayers[i]

			var size dockerapi10.DockerV1CompatibilityImageSize
			if err := json.Unmarshal([]byte(obj.DockerV1Compatibility), &size); err != nil {
				size.Size = 0
			}

			// reverse order of the layers: in schema1 manifests the
			// first layer is the youngest (base layers are at the
			// end), but we want to store layers in the Image resource
			// in order from the oldest to the youngest.
			revidx := (len(manifest.History) - 1) - i // n-1, n-2, ..., 1, 0

			image.DockerImageLayers[revidx].Name = layer.DockerBlobSum
			image.DockerImageLayers[revidx].LayerSize = size.Size
			image.DockerImageLayers[revidx].MediaType = schema1.MediaTypeManifestLayer
		}
	case 2:
		// The layer list is ordered starting from the base image (opposite order of schema1).
		// So, we do not need to change the order of layers.
		image.DockerImageLayers = make([]imagev1.ImageLayer, len(manifest.FSLayers))
		for i, layer := range manifest.Layers {
			image.DockerImageLayers[i].Name = layer.Digest
			image.DockerImageLayers[i].LayerSize = layer.Size
			image.DockerImageLayers[i].MediaType = layer.MediaType
		}
	default:
		return fmt.Errorf("unrecognized container image manifest schema %d for %q (%s)", manifest.SchemaVersion, image.Name, image.DockerImageReference)
	}

	if image.Annotations == nil {
		image.Annotations = map[string]string{}
	}
	image.Annotations[imagev1.DockerImageLayersOrderAnnotation] = imagev1.DockerImageLayersOrderAscending

	return nil
}

func (istw *imagestreamtagwrapper) List(ctx context.Context, list ctrlruntimeclient.ObjectList, opts ...ctrlruntimeclient.ListOption) error {
	if _, isImageStreamTagList := list.(*imagev1.ImageStreamTagList); isImageStreamTagList {
		return errors.New("list for imageStramTags is not implemented")
	}
	return istw.Client.List(ctx, list, opts...)
}
