package quay_io_ci_images_distributor

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/sirupsen/logrus"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	imagev1 "github.com/openshift/api/image/v1"

	cioperatorapi "github.com/openshift/ci-tools/pkg/api"
	apihelper "github.com/openshift/ci-tools/pkg/api/helper"
	controllerutil "github.com/openshift/ci-tools/pkg/controller/util"
	"github.com/openshift/ci-tools/pkg/github"
	"github.com/openshift/ci-tools/pkg/load/agents"
	"github.com/openshift/ci-tools/pkg/util/imagestreamtagmapper"
	"github.com/openshift/ci-tools/pkg/util/imagestreamtagwrapper"
)

const ControllerName = "quay_io_ci_images_distributor"

const configIndexName = "quay-io-config-by-test-input-imagestreamtag"

type registryResolver interface {
	ResolveConfig(config cioperatorapi.ReleaseBuildConfiguration) (cioperatorapi.ReleaseBuildConfiguration, error)
}

func AddToManager(manager manager.Manager,
	configAgent agents.ConfigAgent,
	resolver registryResolver,
	additionalImageStreamTags, additionalImageStreams, additionalImageStreamNamespaces, ignoreImageStreamTags sets.Set[string],
	quayIOImageHelper QuayIOImageHelper,
	mirrorStore MirrorStore,
	registryConfig string,
	onlyValidManifestV2Images bool) error {
	log := logrus.WithField("controller", ControllerName)
	log.WithField("additionalImageStreamNamespaces", additionalImageStreamNamespaces).Info("Received args")
	client := imagestreamtagwrapper.MustNew(manager.GetClient(), manager.GetCache())
	ocImageInfoOptions := OCImageInfoOptions{
		RegistryConfig: registryConfig,
		// TODO: multi-arch support
		FilterByOS: "linux/amd64",
	}
	r := &reconciler{
		log:                             log,
		client:                          client,
		additionalImageStreamNamespaces: additionalImageStreamNamespaces,
		quayIOImageHelper:               quayIOImageHelper,
		ocImageInfoOptions:              ocImageInfoOptions,
		mirrorStore:                     mirrorStore,
		onlyValidManifestV2Images:       onlyValidManifestV2Images,
	}
	c, err := controller.New(ControllerName, manager, controller.Options{
		Reconciler: r,
		// We conflict on ImageStream level which means multiple request for imagestreamtags
		// of the same imagestream will conflict so stay at one worker in order to reduce the
		// number of errors we see. If we hit performance issues, we will probably need cluster
		// and/or imagestream level locking.
		MaxConcurrentReconciles: 1,
	})
	if err != nil {
		return fmt.Errorf("failed to construct controller: %w", err)
	}

	objectFilter, err := testInputImageStreamTagFilterFactory(log, configAgent, resolver, additionalImageStreamTags, additionalImageStreams, additionalImageStreamNamespaces, ignoreImageStreamTags)
	if err != nil {
		return fmt.Errorf("failed to get filter for ImageStreamTags: %w", err)
	}
	// watch imagestream and reconcile on the events filtered by objectFilter
	if err := c.Watch(
		source.Kind(manager.GetCache(),
			&imagev1.ImageStream{},
			imagestreamtagmapper.New(func(in reconcile.Request) []reconcile.Request {
				if !objectFilter(in.NamespacedName) {
					return nil
				}
				return []reconcile.Request{in}
			})),
	); err != nil {
		return fmt.Errorf("failed to create watch for ImageStreams: %w", err)
	}

	configChangeChannel, err := configAgent.SubscribeToIndexChanges(configIndexName)
	if err != nil {
		return fmt.Errorf("failed to subscribe to index changes for index %s: %w", configIndexName, err)
	}
	// besides the events created by the cluster
	// events can be generated from the changes on the ci-op's config on the disk
	if err := c.Watch(sourceForConfigChangeChannel(client, configChangeChannel, ignoreImageStreamTags)); err != nil {
		return fmt.Errorf("failed to subscribe for config change changes: %w", err)
	}

	r.log.Info("Successfully added reconciler to manager")
	return nil
}

type reconciler struct {
	log                             *logrus.Entry
	client                          ctrlruntimeclient.Client
	additionalImageStreamNamespaces sets.Set[string]
	quayIOImageHelper               QuayIOImageHelper
	ocImageInfoOptions              OCImageInfoOptions
	mirrorStore                     MirrorStore
	onlyValidManifestV2Images       bool
}

func (r *reconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	log := r.log.WithField("request", req.String())
	err := r.reconcile(ctx, req, log)
	if err != nil && !apierrors.IsAlreadyExists(err) {
		log.WithError(err).Error("Reconciliation failed")
	} else {
		log.Info("Finished reconciliation")
	}
	return reconcile.Result{}, controllerutil.SwallowIfTerminal(err)
}

func (r *reconciler) reconcile(ctx context.Context, req reconcile.Request, log *logrus.Entry) error {
	*log = *log.WithField("namespace", req.Namespace).WithField("name", req.Name)
	log.Info("Starting reconciliation")
	colonSplit := strings.Split(req.Name, ":")
	if n := len(colonSplit); n != 2 {
		return fmt.Errorf("splitting %s by `:` didn't yield two but %d results", req.Name, n)
	}
	tagRef := cioperatorapi.ImageStreamTagReference{Namespace: req.Namespace, Name: colonSplit[0], Tag: colonSplit[1]}
	quayImage := cioperatorapi.QuayImage(tagRef)
	imageInfo, err := r.quayIOImageHelper.ImageInfo(quayImage, r.ocImageInfoOptions)
	if err != nil {
		return fmt.Errorf("failed to get digest for image stream tag %s/%s for target %s in quay.io: %w", req.Namespace, req.Name, quayImage, err)
	}
	sourceImageStreamTag := &imagev1.ImageStreamTag{}
	if err := r.client.Get(ctx, req.NamespacedName, sourceImageStreamTag); err != nil {
		if apierrors.IsNotFound(err) {
			log.Debug("Source imageStreamTag not found")
			return nil
		}
		return fmt.Errorf("failed to get imageStreamTag %s from registry cluster: %w", req.String(), err)
	}

	// sync only when the target image does not exist because "Digests are not preserved with schema version 1 images."
	if strings.HasSuffix(sourceImageStreamTag.Image.DockerImageManifestMediaType, "manifest.v1+prettyjws") && imageInfo.Digest != "" {
		log.WithField("currentQuayDigest", imageInfo.Digest).Info("Skip mirroring image with manifest v1")
		return nil
	}

	if r.onlyValidManifestV2Images && invalidManifestV2(sourceImageStreamTag) {
		log.Info("Skip mirroring image with invalid manifest v2")
		return nil
	}

	imageName := sourceImageStreamTag.Image.ObjectMeta.Name
	colonSplit = strings.Split(imageName, ":")
	if n := len(colonSplit); n != 2 {
		//should never happen
		return fmt.Errorf("splitting %s by `:` didn't yield two but %d results", imageName, n)
	}
	if colonSplit[0] != "sha256" {
		//should never happen
		return fmt.Errorf("image name has no prefix `sha256:`: %s", imageName)
	}

	sourceImage := fmt.Sprintf("%s/%s/%s@%s", cioperatorapi.DomainForService(cioperatorapi.ServiceRegistry), tagRef.Namespace, tagRef.Name, sourceImageStreamTag.Image.ObjectMeta.Name)
	sourceImageInfo, err := r.quayIOImageHelper.ImageInfo(sourceImage, r.ocImageInfoOptions)
	if err != nil {
		return fmt.Errorf("failed to get digest for image stream tag %s/%s for source %s in app.ci: %w", req.Namespace, req.Name, sourceImage, err)
	}

	// If source image doesn't exist in app.ci, skip mirroring
	// This can happen if the ImageStreamTag was deleted but the event still triggered reconciliation
	if sourceImageInfo.Digest == "" {
		log.WithField("source", sourceImage).Debug("Source image does not exist in app.ci, skipping mirror")
		return nil
	}

	if imageInfo.Digest != sourceImageInfo.Digest {
		// Calculate stale only if image exists in quay.io (has a digest)
		// If image doesn't exist, imageInfo.Digest will be empty and Config.Created will be zero time
		var stale bool
		if imageInfo.Digest != "" {
			stale = imageInfo.Config.Created.Add(24 * time.Hour).Before(sourceImageStreamTag.Image.ObjectMeta.CreationTimestamp.Time)
		}
		// TODO Use stale to handle errors from mirroring
		log.WithField("currentQuayDigest", imageInfo.Digest).WithField("currentAppCIDigest", sourceImageInfo.Digest).WithField("stale", stale).WithField("source", sourceImage).WithField("target", quayImage).Info("Mirroring")

		if err := r.mirrorStore.Put(MirrorTask{
			SourceTagRef:      tagRef,
			Source:            sourceImage,
			Destination:       quayImage,
			CurrentQuayDigest: imageInfo.Digest,
			Stale:             stale,
			Owner:             ControllerName,
		}); err != nil {
			return fmt.Errorf("failed to put the mirror into store: %w", err)
		}
	} else {
		log.WithField("currentQuayDigest", imageInfo.Digest).WithField("target", quayImage).Debug("Image is up to date")
	}
	return nil
}

func invalidManifestV2(tag *imagev1.ImageStreamTag) bool {
	if tag == nil {
		return false
	}
	if !strings.HasSuffix(tag.Image.DockerImageManifestMediaType, "manifest.v2+json") {
		return false
	}
	for _, layer := range tag.Image.DockerImageLayers {
		if !strings.HasSuffix(layer.MediaType, ".gzip") {
			logrus.WithField("namespace", tag.Namespace).WithField("name", tag.Name).
				WithField("dockerImageManifestMediaType", tag.Image.DockerImageManifestMediaType).
				WithField("mediaType", layer.MediaType).Debug("Found layers with MediaType")
			return true
		}
	}
	return false
}

type objectFilter func(types.NamespacedName) bool

// testInputImageStreamTagFilterFactory filters out events of imagestreamTags that are either allowed by the additional args
// or used as input for a test
func testInputImageStreamTagFilterFactory(
	l *logrus.Entry,
	ca agents.ConfigAgent,
	resolver registryResolver,
	additionalImageStreamTags,
	additionalImageStreams,
	additionalImageStreamNamespaces,
	ignoreImageStreamTags sets.Set[string],
) (objectFilter, error) {
	if err := ca.AddIndex(configIndexName, indexConfigsByTestInputImageStreamTag(resolver)); err != nil {
		return nil, fmt.Errorf("failed to add %s index to configAgent: %w", configIndexName, err)
	}
	l = logrus.WithField("subcomponent", "test-input-image-stream-tag-filter")
	return func(nn types.NamespacedName) bool {
		if ignoreImageStreamTags.Has(nn.String()) {
			logrus.WithField("tag", nn.String()).Debug("Ignored events of image stream tag")
			return false
		}
		if additionalImageStreamTags.Has(nn.String()) {
			return true
		}
		if additionalImageStreamNamespaces.Has(nn.Namespace) {
			return true
		}
		imageStreamTagResult, err := ca.GetFromIndex(configIndexName, nn.String())
		if err != nil {
			l.WithField("name", nn.String()).WithError(err).Error("Failed to get imagestreamtag configs from index")
			return false
		}
		if len(imageStreamTagResult) > 0 {
			return true
		}
		imageStreamName, err := imageStreamNameFromImageStreamTagName(nn)
		if err != nil {
			l.WithField("name", nn.String()).WithError(err).Error("Failed to get imagestreamname for imagestreamtag")
			return false
		}
		if additionalImageStreams.Has(imageStreamName.String()) {
			return true
		}
		imageStreamResult, err := ca.GetFromIndex(configIndexName, indexKeyForImageStream(imageStreamName.Namespace, imageStreamName.Name))
		if err != nil {
			l.WithField("name", imageStreamName.String()).WithError(err).Error("Failed to get imagestream configs from index")
			return false
		}
		if len(imageStreamResult) > 0 {
			return true
		}
		return false
	}, nil
}

func imageStreamNameFromImageStreamTagName(nn types.NamespacedName) (types.NamespacedName, error) {
	colonSplit := strings.Split(nn.Name, ":")
	if n := len(colonSplit); n != 2 {
		return types.NamespacedName{}, fmt.Errorf("splitting %s by `:` didn't yield two but %d results", nn.Name, n)
	}
	return types.NamespacedName{Namespace: nn.Namespace, Name: colonSplit[0]}, nil
}

func repoFileGetterWithIgnore(metadata cioperatorapi.Metadata) func(_, _, _ string, _ ...github.Opt) github.FileGetter {
	// We ignore openshift-priv because it is a private org and thus would lead to 404 without providing authentication information
	// Moreover, the .ci-operator.yaml file there is a duplication of the one from the public repo
	if metadata.Org == "openshift-priv" {
		return nil
	}
	return github.FileGetterFactory
}

func indexConfigsByTestInputImageStreamTag(resolver registryResolver) agents.IndexFn {
	return func(cfg cioperatorapi.ReleaseBuildConfiguration) []string {

		log := logrus.WithFields(logrus.Fields{"org": cfg.Metadata.Org, "repo": cfg.Metadata.Repo, "branch": cfg.Metadata.Branch})
		cfg, err := resolver.ResolveConfig(cfg)
		if err != nil {
			log.WithError(err).Error("Failed to resolve MultiStageTestConfiguration")
			return nil
		}
		m, err := apihelper.TestInputImageStreamTagsFromResolvedConfig(cfg, repoFileGetterWithIgnore(cfg.Metadata))
		if err != nil {
			// Should never happen as we set it to nil above
			log.WithError(err).Error("Got error from TestInputImageStreamTagsFromResolvedConfig. This is a software bug.")
		}
		var result []string
		for key := range m {
			result = append(result, key)
		}
		for _, r := range apihelper.TestInputImageStreamsFromResolvedConfig(cfg) {
			result = append(result, indexKeyForImageStream(r.Namespace, r.Name))
		}
		return result
	}
}

func indexKeyForImageStream(namespace, name string) string {
	return "imagestream_" + namespace + "/" + name
}

func sourceForConfigChangeChannel(registryClient ctrlruntimeclient.Client, changes <-chan agents.IndexDelta, ignoreImageStreamTags sets.Set[string]) source.Source {
	sourceChannel := make(chan event.TypedGenericEvent[*imagev1.ImageStreamTag])
	channelSource := source.Channel[*imagev1.ImageStreamTag](sourceChannel, &handler.TypedEnqueueRequestForObject[*imagev1.ImageStreamTag]{})

	go func() {
		for delta := range changes {
			// We only care about new additions
			if len(delta.Added) == 0 {
				continue
			}
			slashSplit := strings.Split(delta.IndexKey, "/")
			if len(slashSplit) != 2 {
				logrus.Errorf("BUG: got an index delta event with a key that is not a valid namespace/name identifier: %s", delta.IndexKey)
				continue
			}
			namespace, name := slashSplit[0], slashSplit[1]
			var result []types.NamespacedName

			// Index holds both imagestreams and imagestreamtags, the former denoted by an imagestream_ prefix.
			// This is needed because ReleaseTagConfigurations reference a whole imagestream rather than
			// individual imagestreamtags.
			if strings.HasPrefix(delta.IndexKey, "imagestream_") {
				namespace = strings.TrimPrefix(namespace, "imagestream_")
				var imagestream imagev1.ImageStream
				if err := registryClient.Get(context.Background(), types.NamespacedName{Namespace: namespace, Name: name}, &imagestream); err != nil {
					// Not found means user referenced an nonexistent stream.
					if !apierrors.IsNotFound(err) {
						logrus.WithError(err).WithField("name", namespace+"/"+name).Error("Failed to get imagestream")
					}
					continue
				}
				for _, tag := range imagestream.Status.Tags {
					result = append(result, types.NamespacedName{Namespace: namespace, Name: name + ":" + tag.Tag})
				}

			} else {
				result = []types.NamespacedName{{Namespace: namespace, Name: name}}
			}
			for _, item := range result {
				if ignoreImageStreamTags.Has(item.String()) {
					logrus.WithField("tag", item.String()).Debug("Ignored tag for config change")
					continue
				}
				sourceChannel <- event.TypedGenericEvent[*imagev1.ImageStreamTag]{Object: &imagev1.ImageStreamTag{ObjectMeta: metav1.ObjectMeta{
					Namespace: item.Namespace,
					Name:      item.Name,
				}}}
			}
		}
	}()

	return channelSource
}

func LoadConfigFromReleaseRepo(configPath string) ([]byte, error) {
	file, err := repoFileGetterWithIgnore(cioperatorapi.Metadata{})("openshift", "release", "main")(configPath)
	if err != nil {
		return []byte{}, fmt.Errorf("failed to get the file %s in repo openshift/release for branch main: %w", configPath, err)
	}
	return file, nil
}

type ImageInfo struct {
	Name string `json:"name"`
	// Digest is the digest of the image, e.g., sha256:b24f782bee7dfddcc36b962f663aeabb16d6fa56a64a7cd0639ebfb1e5fa73f4
	Digest string `json:"digest"`
	Config Config `json:"config"`
}

type Config struct {
	Architecture string `json:"architecture"`
	// "created": "2023-09-14T15:13:32.640956126Z",
	Created time.Time `json:"created"`
}

type QuayIOImageHelper interface {
	ImageInfo(image string, options OCImageInfoOptions) (ImageInfo, error)
	ImageMirror(pairs []string, options OCImageMirrorOptions) error
}
