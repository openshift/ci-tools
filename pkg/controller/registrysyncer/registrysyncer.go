package registrysyncer

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/sirupsen/logrus"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	crcontrollerutil "sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	imagev1 "github.com/openshift/api/image/v1"

	"github.com/openshift/ci-tools/pkg/api"
	controllerutil "github.com/openshift/ci-tools/pkg/controller/util"
	"github.com/openshift/ci-tools/pkg/util/imagestreamtagmapper"
	"github.com/openshift/ci-tools/pkg/util/imagestreamtagwrapper"
)

const ControllerName = "registry_syncer"

func AddToManager(mgr manager.Manager,
	managers map[string]manager.Manager,
	pullSecretGetter func() []byte,
	imageStreamPrefixes sets.String,
	deniedImageStreams sets.String,
	ingnoreReleaseControllerImageStreams bool,
) error {
	log := logrus.WithField("controller", ControllerName)
	r := &reconciler{
		log:              log,
		registryClients:  map[string]ctrlruntimeclient.Client{},
		pullSecretGetter: pullSecretGetter,
	}
	for clusterName, m := range managers {
		r.registryClients[clusterName] = imagestreamtagwrapper.MustNew(m.GetClient(), m.GetCache())
	}
	c, err := controller.New(ControllerName, mgr, controller.Options{
		Reconciler: r,
		// When > 1, there will be IsConflict errors on updating the same ImageStream
		MaxConcurrentReconciles: 100,
	})
	if err != nil {
		return fmt.Errorf("failed to construct controller: %w", err)
	}

	for _, m := range managers {
		if err := c.Watch(
			source.NewKindWithCache(&imagev1.ImageStream{}, m.GetCache()),
			handlerFactory(testInputImageStreamTagFilterFactory(log, imageStreamPrefixes, deniedImageStreams, ingnoreReleaseControllerImageStreams)),
		); err != nil {
			return fmt.Errorf("failed to create watch for ImageStreams: %w", err)
		}
	}

	r.log.Info("Successfully added reconciler to manager")
	return nil
}

type objectFilter func(types.NamespacedName) bool

// handlerFactory produces a handler that:
// * Watches ImageStreams because ImageStreamTags do not support the watch verb
// * Extracts all ImageStramTags out of the Image
// * Filters out the ones that are not in use
// Note: We can not use a predicate because that is directly applied on the source and the source yields ImageStreams, not ImageStreamTags
// * Creates a reconcile.Request per cluster and ImageStreamTag
func handlerFactory(filter objectFilter) handler.EventHandler {
	return imagestreamtagmapper.New(func(in reconcile.Request) []reconcile.Request {
		if !filter(in.NamespacedName) {
			return nil
		}
		return []reconcile.Request{in}
	})
}

type reconciler struct {
	log              *logrus.Entry
	registryClients  map[string]ctrlruntimeclient.Client
	pullSecretGetter func() []byte
}

func (r *reconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	log := r.log.WithField("request", req.String())
	log.Info("Starting reconciliation")
	err := r.reconcile(ctx, req, log)
	// Ignore the logging for IsConflict errors because they are results of concurrent reconciling
	if err != nil && !apierrors.IsConflict(err) && !apierrors.IsAlreadyExists(err) {
		log.WithError(err).Error("Reconciliation failed")
	} else {
		log.Info("Finished reconciliation")
	}
	return reconcile.Result{}, controllerutil.SwallowIfTerminal(err)
}

const (
	annotationDPTPRequester = "dptp.openshift.io/requester"
	finalizerName           = "dptp.openshift.io/registry-syncer"
)

func (r *reconciler) reconcile(ctx context.Context, req reconcile.Request, log *logrus.Entry) error {
	isTags := map[string]*imagev1.ImageStreamTag{}
	for clusterName, client := range r.registryClients {
		*log = *log.WithField("clusterName", clusterName)
		imageStreamTag := &imagev1.ImageStreamTag{}
		if err := client.Get(ctx, req.NamespacedName, imageStreamTag); err != nil {
			if apierrors.IsNotFound(err) {
				log.Debug("Source imageStreamTag not found")
				continue
			}
			return fmt.Errorf("failed to get imageStreamTag %s from cluster %s: %w", req.NamespacedName.String(), clusterName, err)
		}
		isTags[clusterName] = imageStreamTag
	}

	srcClusterName := findNewest(isTags)
	if srcClusterName == "" {
		// the isTag does NOT exist on both clusters
		// This case is not an error but expected to happen occasionally
		return nil
	}
	sourceImageStreamTag := isTags[srcClusterName]

	imageStreamNameAndTag := strings.Split(req.Name, ":")
	if n := len(imageStreamNameAndTag); n != 2 {
		return fmt.Errorf("when splitting imagestreamtagname %s by : expected two results, got %d", req.Name, n)
	}
	imageStreamName := imageStreamNameAndTag[0]
	isName := types.NamespacedName{Namespace: req.Namespace, Name: imageStreamName}
	sourceImageStream := &imagev1.ImageStream{}
	if err := r.registryClients[srcClusterName].Get(ctx, isName, sourceImageStream); err != nil {
		// received a request on the isTag, but the 'is' is no longer there
		return fmt.Errorf("failed to get imageStream %s from cluster %s: %w", isName.String(), srcClusterName, err)
	}

	if err := ensureFinalizer(ctx, sourceImageStream, r.registryClients[srcClusterName]); err != nil {
		return fmt.Errorf("failed to ensure finalizer to %s from cluster %s: %w", isName.String(), srcClusterName, err)
	}

	deleted, err := finalizeIfNeeded(ctx, sourceImageStream, r.registryClients, log)
	if err != nil {
		return fmt.Errorf("failed to finalize %s from cluster %s: %w", isName.String(), srcClusterName, err)
	}
	if deleted {
		// no sync if the srcIS is deleted
		return nil
	}

	clusterWithDeletedTag, ok, err := hasDueSoftDeleteAnnotation(isTags)
	if err != nil {
		return fmt.Errorf("failed to determine if the istags have due soft delete annaotion: %w", err)
	}

	// if found due soft-delete annotation on any isTag, delete the isTag on all clusters
	if ok {
		log.WithField("soft_delete_annotation_found", clusterWithDeletedTag).Debug("deleting imageStreamTags")
		for clusterName, isTag := range isTags {
			if err := r.registryClients[clusterName].Delete(ctx, isTag); err != nil && !apierrors.IsNotFound(err) {
				return fmt.Errorf("failed to delete imageStream %s from cluster %s: %w", isTag.String(), clusterName, err)
			}
		}
		// no sync if the isTag is deleted
		return nil
	}

	*log = *log.WithField("docker_image_reference", sourceImageStreamTag.Image.DockerImageReference)

	for clusterName, client := range r.registryClients {
		*log = *log.WithField("clusterName", clusterName)
		if clusterName == srcClusterName {
			continue
		}
		if dockerImageImportedFromTargetingCluster(clusterName, sourceImageStreamTag) {
			log.Debug("dockerImage imported from targeting cluster")
			continue
		}
		if err := client.Get(ctx, types.NamespacedName{Name: req.Namespace}, &corev1.Namespace{}); err != nil {
			if !apierrors.IsNotFound(err) {
				return fmt.Errorf("failed to check if namespace %s exists on cluster %s: %w", req.Namespace, clusterName, err)
			}
			if err := client.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{
				Name:        req.Namespace,
				Annotations: map[string]string{annotationDPTPRequester: ControllerName},
			}}); err != nil && !apierrors.IsAlreadyExists(err) {
				return fmt.Errorf("failed to create namespace %s on cluster %s: %w", req.Namespace, clusterName, err)
			}
		}

		if err := r.ensureImageStream(ctx, sourceImageStream, client, log); err != nil {
			return fmt.Errorf("failed to ensure imagestream on cluster %s: %w", clusterName, err)
		}

		// There is some delay until it gets back to our cache, so block until we can retrieve
		// it successfully.
		key := ctrlruntimeclient.ObjectKey{Name: sourceImageStream.Name, Namespace: sourceImageStream.Namespace}
		if err := wait.Poll(100*time.Millisecond, 5*time.Second, func() (bool, error) {
			if err := client.Get(ctx, key, &imagev1.ImageStream{}); err != nil {
				if apierrors.IsNotFound(err) {
					return false, nil
				}
				return false, fmt.Errorf("failed to get imagestream on cluster %s: %w", clusterName, err)
			}
			return true, nil
		}); err != nil {
			return fmt.Errorf("failed to wait for ensured imagestream to appear in cache on cluster %s: %w", clusterName, err)
		}

		isTag, found := isTags[clusterName]
		if found && isTag.Image.Name == sourceImageStreamTag.Image.Name {
			log.Debug("ImageStreamTag is current")
			return nil
		}

		if err := r.ensureImagePullSecret(ctx, req.Namespace, client, log); err != nil {
			return fmt.Errorf("failed to ensure imagePullSecret on cluster %s: %w", clusterName, err)
		}
		dockerImageReference, err := api.PublicDomainForImage(srcClusterName, sourceImageStreamTag.Image.DockerImageReference)
		if err != nil {
			return fmt.Errorf("failed to get the public domain: %w", err)
		}

		log.Debug("creating imageStreamTag")
		// TODO: (hongkliu) check the status of imports with another way
		if err := r.ensureImageStreamTag(ctx, sourceImageStreamTag, dockerImageReference, client, log); err != nil {
			controllerutil.CountImportResult(ControllerName, clusterName, req.Namespace, imageStreamName, false)
			return fmt.Errorf("failed to ensure ImageSteamTag: %w", err)
		}
		controllerutil.CountImportResult(ControllerName, clusterName, req.Namespace, imageStreamName, true)
		log.Debug("Imported successfully")
	}
	return nil
}

func hasDueSoftDeleteAnnotation(streams map[string]*imagev1.ImageStreamTag) (string, bool, error) {
	for cluster, stream := range streams {
		if stream == nil || stream.Annotations == nil {
			continue
		}
		if value, ok := stream.Annotations[api.ReleaseAnnotationSoftDelete]; ok {
			t, err := time.Parse(time.RFC3339, value)
			if err != nil {
				return "", false, err
			}
			if time.Now().After(t) {
				return cluster, true, nil
			}
		}
	}
	return "", false, nil
}

func finalizeIfNeeded(ctx context.Context, sourceImageStream *imagev1.ImageStream, clients map[string]ctrlruntimeclient.Client, log *logrus.Entry) (bool, error) {
	foundDeleteTimestamp := false
	for _, client := range clients {
		stream := &imagev1.ImageStream{}
		if err := client.Get(ctx, ctrlruntimeclient.ObjectKey{Name: sourceImageStream.Name, Namespace: sourceImageStream.Namespace}, stream); err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}
			return false, err
		}
		if stream.DeletionTimestamp != nil {
			foundDeleteTimestamp = true
			break
		}
	}
	if !foundDeleteTimestamp {
		return false, nil
	}

	for _, client := range clients {
		stream := &imagev1.ImageStream{}
		if err := client.Get(ctx, ctrlruntimeclient.ObjectKey{Name: sourceImageStream.Name, Namespace: sourceImageStream.Namespace}, stream); err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}
			return false, err
		}
		if err := ensureRemoveFinalizer(ctx, stream, client, log); err != nil {
			return false, err
		}
		// populate deleting to all clusters if it has not been deleted
		if stream.DeletionTimestamp == nil {
			log.Debug("deleting imagestream")
			if err := client.Delete(ctx, stream); err != nil && !apierrors.IsNotFound(err) {
				return false, err
			}
		}
	}
	return true, nil
}

func ensureRemoveFinalizer(ctx context.Context, stream *imagev1.ImageStream, client ctrlruntimeclient.Client, log *logrus.Entry) error {
	finalizerSet := sets.NewString(stream.Finalizers...)
	if !finalizerSet.Has(finalizerName) {
		return nil
	}
	stream.Finalizers = finalizerSet.Delete(finalizerName).List()
	log.Debug("removing finalizer")
	return client.Update(ctx, stream)
}

func ensureFinalizer(ctx context.Context, stream *imagev1.ImageStream, client ctrlruntimeclient.Client) error {
	if sets.NewString(stream.Finalizers...).Has(finalizerName) {
		return nil
	}
	stream.Finalizers = append(stream.Finalizers, finalizerName)
	return client.Update(ctx, stream)
}

func dockerImageImportedFromTargetingCluster(cluster string, tag *imagev1.ImageStreamTag) bool {
	if tag == nil || tag.Tag == nil || tag.Tag.From == nil || tag.Tag.From.Kind != "DockerImage" {
		return false
	}
	from := tag.Tag.From.Name
	return strings.HasPrefix(from, api.ServiceDomainAPICIRegistry) && cluster == "api.ci" || strings.HasPrefix(from, api.ServiceDomainAPPCIRegistry) && cluster == "app.ci"
}

func (r *reconciler) ensureImageStreamTag(ctx context.Context, imageStreamTag *imagev1.ImageStreamTag, dockerImageReference string, client ctrlruntimeclient.Client, log *logrus.Entry) error {
	isTag, mutateFn := imagestreamtag(imageStreamTag, dockerImageReference)
	return upsertObject(ctx, client, isTag, mutateFn, log)
}

func imagestreamtag(sourceISTag *imagev1.ImageStreamTag, dockerImageReference string) (*imagev1.ImageStreamTag, crcontrollerutil.MutateFn) {
	imageStreamTag := &imagev1.ImageStreamTag{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: sourceISTag.Namespace,
			Name:      sourceISTag.Name,
		},
	}
	return imageStreamTag, func() error {
		copiedISTag := sourceISTag.DeepCopy()
		imageStreamTag.Annotations = copiedISTag.Annotations
		imageStreamTag.Tag = &imagev1.TagReference{
			From: &corev1.ObjectReference{
				Kind: "DockerImage",
				Name: dockerImageReference,
			},
		}
		return nil
	}
}

func findNewest(isTags map[string]*imagev1.ImageStreamTag) string {
	result := ""
	var t metav1.Time
	for clusterName, isTag := range isTags {
		if t.Before(&isTag.Image.CreationTimestamp) {
			t = isTag.Image.CreationTimestamp
			result = clusterName
		}
	}
	return result
}

const pullSecretName = "registry-cluster-pull-secret"

func (r *reconciler) ensureImagePullSecret(ctx context.Context, namespace string, client ctrlruntimeclient.Client, log *logrus.Entry) error {
	secret, mutateFn := r.pullSecret(namespace)
	return upsertObject(ctx, client, secret, mutateFn, log)
}

// https://issues.redhat.com/browse/DPTP-1656?focusedCommentId=15345756&page=com.atlassian.jira.plugin.system.issuetabpanels%3Acomment-tabpanel#comment-15345756
// E.g., ci-operator uses the release controller configuration to determine
// the version of OpenShift we create from the ImageStream, so we need
// to copy the annotation if it exists
const releaseConfigAnnotationPrefix = "release.openshift.io"

func imagestream(imageStream *imagev1.ImageStream) (*imagev1.ImageStream, crcontrollerutil.MutateFn) {
	stream := &imagev1.ImageStream{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: imageStream.Namespace,
			Name:      imageStream.Name,
		},
	}
	return stream, func() error {
		for k, v := range imageStream.Annotations {
			if strings.HasPrefix(k, releaseConfigAnnotationPrefix) {
				if stream.Annotations == nil {
					stream.Annotations = map[string]string{}
				}
				stream.Annotations[k] = v
			}
		}
		stream.Spec.LookupPolicy.Local = imageStream.Spec.LookupPolicy.Local
		return nil
	}
}

func (r *reconciler) ensureImageStream(ctx context.Context, imageStream *imagev1.ImageStream, client ctrlruntimeclient.Client, log *logrus.Entry) error {
	stream, mutateFn := imagestream(imageStream)
	return upsertObject(ctx, client, stream, mutateFn, log)
}

func (r *reconciler) pullSecret(namespace string) (*corev1.Secret, crcontrollerutil.MutateFn) {
	s := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      pullSecretName,
		},
	}
	return s, func() error {
		s.Data = map[string][]byte{
			corev1.DockerConfigJsonKey: r.pullSecretGetter(),
		}
		s.Type = corev1.SecretTypeDockerConfigJson
		return nil
	}
}

var (
	deniedNamespacePrefixes = sets.NewString("kube", "openshift", "default", "redhat", "ci-op", "ci-ln")
	// releaseControllerImageStreamsRegEx defines the imagestreams to sync even if flag ingnoreReleaseControllerImageStreams is set
	releaseControllerImageStreamsRegEx = regexp.MustCompile(`^(4\.\d+(|\.\d+)(|-(f|r)c\.\d+)|[^4]\S+)$`)
	releaseControllerNamespaces        = sets.NewString("ocp", "ocp-priv", "ocp-ppc64le", "ocp-ppc64le-priv", "ocp-s390x", "ocp-s390x-priv")
)

func ignoredReleaseControllerImageStreams(namespace, name string) bool {
	return releaseControllerNamespaces.Has(namespace) && !releaseControllerImageStreamsRegEx.Match([]byte(name))
}

func testInputImageStreamTagFilterFactory(
	l *logrus.Entry,
	imageStreamPrefixes sets.String,
	deniedImageStreams sets.String,
	ingnoreReleaseControllerImageStreams bool,
) objectFilter {
	l = logrus.WithField("subcomponent", "test-input-image-stream-tag-filter")
	return func(nn types.NamespacedName) bool {
		imageStreamName, err := imageStreamNameFromImageStreamTagName(nn)
		if err != nil {
			l.WithField("name", nn.String()).WithError(err).Error("Failed to get imagestreamname for imagestreamtag")
			return false
		}
		if deniedImageStreams.Has(imageStreamName.String()) {
			return false
		}
		for _, deniedNamespacePrefix := range deniedNamespacePrefixes.List() {
			if strings.HasPrefix(imageStreamName.Namespace, deniedNamespacePrefix) {
				for _, prefix := range imageStreamPrefixes.List() {
					if strings.HasPrefix(imageStreamName.String(), prefix) {
						return true
					}
				}
				return false
			}
		}
		if ingnoreReleaseControllerImageStreams && ignoredReleaseControllerImageStreams(imageStreamName.Namespace, imageStreamName.Name) {
			return false
		}
		return true
	}
}

func imageStreamNameFromImageStreamTagName(nn types.NamespacedName) (types.NamespacedName, error) {
	colonSplit := strings.Split(nn.Name, ":")
	if n := len(colonSplit); n != 2 {
		return types.NamespacedName{}, fmt.Errorf("splitting %s by `:` didn't yield two but %d results", nn.Name, n)
	}
	return types.NamespacedName{Namespace: nn.Namespace, Name: colonSplit[0]}, nil
}

func upsertObject(ctx context.Context, c ctrlruntimeclient.Client, obj ctrlruntimeclient.Object, mutateFn crcontrollerutil.MutateFn, log *logrus.Entry) error {
	// Create log here in case the operation fails and the obj is nil
	log = log.WithFields(logrus.Fields{"namespace": obj.GetNamespace(), "name": obj.GetName(), "type": fmt.Sprintf("%T", obj)})
	result, err := crcontrollerutil.CreateOrUpdate(ctx, c, obj, mutateFn)
	log = log.WithField("operation", result)
	if err != nil && !apierrors.IsConflict(err) && !apierrors.IsAlreadyExists(err) {
		log.WithError(err).Error("Upsert failed")
	} else if result != crcontrollerutil.OperationResultNone {
		log.Info("Upsert succeeded")
	}
	return err
}
