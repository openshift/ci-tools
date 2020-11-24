package registrysyncer

import (
	"context"
	"fmt"
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
	imageStreamTags sets.String,
	imageStreams sets.String,
	imageStreamPrefixes sets.String,
	imageStreamNamespaces sets.String,
	deniedImageStreams sets.String,
) error {
	log := logrus.WithField("controller", ControllerName)
	r := &reconciler{
		log:                   log,
		registryClients:       map[string]ctrlruntimeclient.Client{},
		pullSecretGetter:      pullSecretGetter,
		imageStreamTags:       imageStreamTags,
		imageStreams:          imageStreams,
		imageStreamPrefixes:   imageStreamPrefixes,
		imageStreamNamespaces: imageStreamNamespaces,
		deniedImageStreams:    deniedImageStreams,
	}
	for clusterName, m := range managers {
		r.registryClients[clusterName] = imagestreamtagwrapper.MustNew(m.GetClient(), m.GetCache())
	}
	c, err := controller.New(ControllerName, mgr, controller.Options{
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

	for _, m := range managers {
		if err := c.Watch(
			source.NewKindWithCache(&imagev1.ImageStream{}, m.GetCache()),
			handlerFactory(testInputImageStreamTagFilterFactory(log, imageStreamTags, imageStreams, imageStreamPrefixes, imageStreamNamespaces, deniedImageStreams)),
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
	log                   *logrus.Entry
	registryClients       map[string]ctrlruntimeclient.Client
	pullSecretGetter      func() []byte
	imageStreamTags       sets.String
	imageStreams          sets.String
	imageStreamPrefixes   sets.String
	imageStreamNamespaces sets.String
	deniedImageStreams    sets.String
}

func (r *reconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	log := r.log.WithField("request", req.String())
	log.Info("Starting reconciliation")
	err := r.reconcile(ctx, req, log)
	if err != nil {
		log.WithError(err).Error("Reconciliation failed")
	} else {
		log.Info("Finished reconciliation")
	}
	return reconcile.Result{}, controllerutil.SwallowIfTerminal(err)
}

const (
	annotationDPTPRequester = "dptp.openshift.io/requester"
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
	imageStreamName, imageTag := imageStreamNameAndTag[0], imageStreamNameAndTag[1]
	isName := types.NamespacedName{Namespace: req.Namespace, Name: imageStreamName}
	sourceImageStream := &imagev1.ImageStream{}
	if err := r.registryClients[srcClusterName].Get(ctx, isName, sourceImageStream); err != nil {
		// received a request on the isTag, but the 'is' is no longer there
		return fmt.Errorf("failed to get imageStream %s from cluster %s: %w", isName.String(), srcClusterName, err)
	}

	*log = *log.WithField("docker_image_reference", sourceImageStreamTag.Image.DockerImageReference)

	for clusterName, client := range r.registryClients {
		*log = *log.WithField("clusterName", clusterName)
		if clusterName == srcClusterName {
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
		imageName, err := publicDomainForImage(srcClusterName, sourceImageStreamTag.Image.DockerImageReference)
		if err != nil {
			return fmt.Errorf("failed to get the public domain: %w", err)
		}

		imageStreamImport := &imagev1.ImageStreamImport{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: req.Namespace,
				Name:      imageStreamName,
			},
			Spec: imagev1.ImageStreamImportSpec{
				Import: true,
				Images: []imagev1.ImageImportSpec{{
					From: corev1.ObjectReference{
						Kind: "DockerImage",
						Name: imageName,
					},
					To: &corev1.LocalObjectReference{Name: imageTag},
					ReferencePolicy: imagev1.TagReferencePolicy{
						Type: imagev1.LocalTagReferencePolicy,
					},
				}},
			},
		}

		*log = *log.WithField("imageStreamImport.Namespace", imageStreamImport.Namespace).WithField("imageStreamImport.Name", imageStreamImport.Name)
		log.Debug("creating imageStreamImport")
		if err := createAndCheckStatus(ctx, client, imageStreamImport); err != nil {
			controllerutil.CountImportResult(ControllerName, clusterName, req.Namespace, imageStreamName, false)
			return fmt.Errorf("failed to create and check the status for imageStreamImport on cluster %s %v : %w", clusterName, imageStreamImport, err)
		}
		controllerutil.CountImportResult(ControllerName, clusterName, req.Namespace, imageStreamName, true)
		log.Debug("Imported successfully")
	}
	return nil
}

func createAndCheckStatus(ctx context.Context, client ctrlruntimeclient.Client, imageStreamImport *imagev1.ImageStreamImport) error {
	// ImageStreamImport is not an ordinary api but a virtual one that does the import synchronously
	if err := client.Create(ctx, imageStreamImport); err != nil {
		return fmt.Errorf("failed to import Image: %w", err)
	}

	// This should never be needed, but we shouldn't panic if the server screws up
	if imageStreamImport.Status.Images == nil {
		imageStreamImport.Status.Images = []imagev1.ImageImportStatus{{}}
	}
	if imageStreamImport.Status.Images[0].Image == nil {
		return fmt.Errorf("imageStreamImport did not succeed: reason: %s, message: %s", imageStreamImport.Status.Images[0].Status.Reason, imageStreamImport.Status.Images[0].Status.Message)
	}
	return nil
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

func testInputImageStreamTagFilterFactory(
	l *logrus.Entry,
	imageStreamTags sets.String,
	imageStreams sets.String,
	imageStreamPrefixes sets.String,
	imageStreamNamespaces sets.String,
	deniedImageStreams sets.String,
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
		if imageStreamTags.Has(nn.String()) {
			return true
		}
		if imageStreamNamespaces.Has(nn.Namespace) {
			return true
		}
		if imageStreams.Has(imageStreamName.String()) {
			return true
		}
		for _, prefix := range imageStreamPrefixes.List() {
			if strings.HasPrefix(imageStreamName.String(), prefix) {
				return true
			}
		}
		return false
	}
}

func imageStreamNameFromImageStreamTagName(nn types.NamespacedName) (types.NamespacedName, error) {
	colonSplit := strings.Split(nn.Name, ":")
	if n := len(colonSplit); n != 2 {
		return types.NamespacedName{}, fmt.Errorf("splitting %s by `:` didn't yield two but %d results", nn.Name, n)
	}
	return types.NamespacedName{Namespace: nn.Namespace, Name: colonSplit[0]}, nil
}

func publicDomainForImage(clusterName, potentiallyPrivate string) (string, error) {
	d, err := domainForClusterName(clusterName)
	if err != nil {
		return "", err
	}
	svcDomainAndPort := "image-registry.openshift-image-registry.svc:5000"
	if clusterName == "api.ci" {
		svcDomainAndPort = "docker-registry.default.svc:5000"
	}

	return strings.ReplaceAll(potentiallyPrivate, svcDomainAndPort, d), nil
}

func domainForClusterName(ClusterName string) (string, error) {
	switch ClusterName {
	case "api.ci":
		return api.DomainForService(api.ServiceRegistry), nil
	case "app.ci":
		return api.ServiceDomainAPPCIRegistry, nil
	}
	return "", fmt.Errorf("failed to get the domain for cluster %s", ClusterName)
}

func upsertObject(ctx context.Context, c ctrlruntimeclient.Client, obj ctrlruntimeclient.Object, mutateFn crcontrollerutil.MutateFn, log *logrus.Entry) error {
	// Create log here in case the operation fails and the obj is nil
	log = log.WithFields(logrus.Fields{"namespace": obj.GetNamespace(), "name": obj.GetName(), "type": fmt.Sprintf("%T", obj)})
	result, err := crcontrollerutil.CreateOrUpdate(ctx, c, obj, mutateFn)
	log = log.WithField("operation", result)
	if err != nil {
		log.WithError(err).Error("Upsert failed")
	} else if result != crcontrollerutil.OperationResultNone {
		log.Info("Upsert succeeded")
	}
	return err
}
