package imagepusher

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

const ControllerName = "image_pusher"

func AddToManager(appCIMgr manager.Manager,
	apiCIMgr manager.Manager,
	imageStreams sets.String,
) error {
	log := logrus.WithField("controller", ControllerName)
	appCIClient := imagestreamtagwrapper.MustNew(appCIMgr.GetClient(), appCIMgr.GetCache())
	apiCIClient := imagestreamtagwrapper.MustNew(apiCIMgr.GetClient(), apiCIMgr.GetCache())
	r := &reconciler{
		log:         log,
		appCIClient: appCIClient,
		apiCIClient: apiCIClient,
	}
	c, err := controller.New(ControllerName, appCIMgr, controller.Options{
		Reconciler:              r,
		MaxConcurrentReconciles: 1,
	})
	if err != nil {
		return fmt.Errorf("failed to construct controller: %w", err)
	}

	if err := c.Watch(
		source.NewKindWithCache(&imagev1.ImageStream{}, appCIMgr.GetCache()),
		handlerFactory(imageStreamTagFilterFactory(log, imageStreams)),
	); err != nil {
		return fmt.Errorf("failed to create watch for ImageStreams: %w", err)
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
	log         *logrus.Entry
	appCIClient ctrlruntimeclient.Client
	apiCIClient ctrlruntimeclient.Client
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

func (r *reconciler) reconcile(ctx context.Context, req reconcile.Request, log *logrus.Entry) error {
	sourceImageStreamTag := &imagev1.ImageStreamTag{}
	if err := r.appCIClient.Get(ctx, req.NamespacedName, sourceImageStreamTag); err != nil {
		if apierrors.IsNotFound(err) {
			log.Debug("the source imageStreamTag not found")
			return nil
		}
		return fmt.Errorf("failed to get source imageStreamTag %s: %w", req.NamespacedName.String(), err)
	}

	imageStreamNameAndTag := strings.Split(req.Name, ":")
	if n := len(imageStreamNameAndTag); n != 2 {
		return fmt.Errorf("when splitting imagestreamtagname %s by : expected two results, got %d", req.Name, n)
	}
	imageStreamName, imageTag := imageStreamNameAndTag[0], imageStreamNameAndTag[1]
	isName := types.NamespacedName{Namespace: req.Namespace, Name: imageStreamName}
	sourceImageStream := &imagev1.ImageStream{}
	if err := r.appCIClient.Get(ctx, isName, sourceImageStream); err != nil {
		// received a request on the isTag, but the 'is' is no longer there
		return fmt.Errorf("failed to get source imageStream %s: %w", isName.String(), err)
	}

	*log = *log.WithField("docker_image_reference", sourceImageStreamTag.Image.DockerImageReference)

	isCurrent, err := r.isImageStreamTagCurrent(ctx, req.NamespacedName, r.apiCIClient, sourceImageStreamTag)
	if err != nil {
		return fmt.Errorf("failed to check if imageStreamTag on the target cluster is current: %w", err)
	}
	if isCurrent {
		log.Debug("ImageStreamTag is current")
		return nil
	}

	if err := r.apiCIClient.Get(ctx, types.NamespacedName{Name: req.Namespace}, &corev1.Namespace{}); err != nil {
		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to check if namespace %s exists on the target cluster: %w", req.Namespace, err)
		}
		if err := r.apiCIClient.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{
			Name:   req.Namespace,
			Labels: map[string]string{api.DPTPRequesterLabel: ControllerName},
		}}); err != nil && !apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("failed to create namespace %s on the target cluster: %w", req.Namespace, err)
		}
	}

	// create the imagesteram if it does not exist
	// No mutation function is here because we ensure only existence
	// We do not sync any tags or annotations with this controller
	key := types.NamespacedName{Name: sourceImageStream.Name, Namespace: sourceImageStream.Namespace}
	if err := r.apiCIClient.Get(ctx, key, &imagev1.ImageStream{}); err != nil {
		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to check if imagestream %s exists on the target cluster: %w", key.String(), err)
		}
		if err := r.apiCIClient.Create(ctx, &imagev1.ImageStream{ObjectMeta: metav1.ObjectMeta{
			Name:      sourceImageStream.Name,
			Namespace: sourceImageStream.Namespace,
			Labels:    map[string]string{api.DPTPRequesterLabel: ControllerName},
		}}); err != nil && !apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("failed to create imagestream %s on the target cluster: %w", key.String(), err)
		}
	}

	// There is some delay until it gets back to our cache, so block until we can retrieve
	// it successfully.
	key = types.NamespacedName{Name: sourceImageStream.Name, Namespace: sourceImageStream.Namespace}
	if err := wait.Poll(100*time.Millisecond, 10*time.Second, func() (bool, error) {
		if err := r.apiCIClient.Get(ctx, key, &imagev1.ImageStream{}); err != nil {
			if apierrors.IsNotFound(err) {
				return false, nil
			}
			return false, fmt.Errorf("failed to get imagestream on the target cluster: %w", err)
		}
		return true, nil
	}); err != nil {
		return fmt.Errorf("failed to wait for ensured imagestream %s to appear in cache on the target cluster: %w", key.String(), err)
	}

	if err := controllerutil.EnsureImagePullSecret(ctx, req.Namespace, r.apiCIClient, log); err != nil {
		return fmt.Errorf("failed to ensure imagePullSecret on the target cluster: %w", err)
	}
	dockerImageReference, err := api.PublicDomainForImage("app.ci", sourceImageStreamTag.Image.DockerImageReference)
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
					Name: dockerImageReference,
				},
				To: &corev1.LocalObjectReference{Name: imageTag},
				ReferencePolicy: imagev1.TagReferencePolicy{
					Type: imagev1.LocalTagReferencePolicy,
				},
			}},
		},
	}

	// ImageStreamImport is not an ordinary api but a virtual one that does the import synchronously
	if err := r.apiCIClient.Create(ctx, imageStreamImport); err != nil {
		controllerutil.CountImportResult(ControllerName, "api.ci", req.Namespace, imageStreamName, false)
		return fmt.Errorf("failed to import Image: %w", err)
	}

	// This should never be needed, but we shouldn't panic if the server screws up
	if imageStreamImport.Status.Images == nil {
		imageStreamImport.Status.Images = []imagev1.ImageImportStatus{{}}
	}
	if imageStreamImport.Status.Images[0].Image == nil {
		return fmt.Errorf("imageStreamImport did not succeed: reason: %s, message: %s", imageStreamImport.Status.Images[0].Status.Reason, imageStreamImport.Status.Images[0].Status.Message)
	}

	controllerutil.CountImportResult(ControllerName, "api.ci", req.Namespace, imageStreamName, true)

	log.Debug("Imported successfully")
	return nil
}

func (r *reconciler) isImageStreamTagCurrent(
	ctx context.Context,
	name types.NamespacedName,
	targetClient ctrlruntimeclient.Client,
	reference *imagev1.ImageStreamTag,
) (bool, error) {

	imageStreamTag := &imagev1.ImageStreamTag{}
	if err := targetClient.Get(ctx, name, imageStreamTag); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("failed to get imagestreamtag %s: %w", name.String(), err)
	}

	return imageStreamTag.Image.Name == reference.Image.Name, nil
}

func imageStreamTagFilterFactory(
	l *logrus.Entry,
	imageStreams sets.String,
) objectFilter {
	l = logrus.WithField("subcomponent", "test-input-image-stream-tag-filter")
	return func(nn types.NamespacedName) bool {
		imageStreamName, err := imageStreamNameFromImageStreamTagName(nn)
		if err != nil {
			l.WithField("name", nn.String()).WithError(err).Error("Failed to get imagestreamname for imagestreamtag")
			return false
		}
		if imageStreams.Has(imageStreamName.String()) {
			return true
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
