package testimagesdistributor

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	imagev1 "github.com/openshift/api/image/v1"
	"github.com/openshift/ci-tools/pkg/util/imagestreamtagwrapper"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	"github.com/openshift/ci-tools/pkg/api"
	apihelper "github.com/openshift/ci-tools/pkg/api/helper"
	controllerutil "github.com/openshift/ci-tools/pkg/controller/util"
	"github.com/openshift/ci-tools/pkg/load/agents"
)

const ControllerName = "test_images_distributor"

func AddToManager(mgr manager.Manager,
	registryManager manager.Manager,
	buildClusterManagers map[string]manager.Manager,
	configAgent agents.ConfigAgent,
	pullSecretGetter func() []byte,
	dryRun bool) error {
	log := logrus.WithField("controller", ControllerName)

	successfulImportsCounter := prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: ControllerName,
		Name:      "imagestream_successful_import_count",
		Help:      "The number of imagestream imports the controller created succesfull",
	}, []string{"cluster", "namespace"})
	if err := metrics.Registry.Register(successfulImportsCounter); err != nil {
		return fmt.Errorf("failed to register successfulImportsCounter metric: %w", err)
	}
	failedImportsCounter := prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: ControllerName,
		Name:      "imagestream_failed_import_count",
		Help:      "The number of failed imagestream imports the controller create",
	}, []string{"cluster", "namespace"})
	if err := metrics.Registry.Register(failedImportsCounter); err != nil {
		return fmt.Errorf("failed to register failedImportsCounter metric: %w", err)
	}

	r := &reconciler{
		ctx:                      context.Background(),
		log:                      log,
		registryClient:           imagestreamtagwrapper.MustNew(registryManager.GetClient(), registryManager.GetCache()),
		buildClusterClients:      map[string]ctrlruntimeclient.Client{},
		pullSecretGetter:         pullSecretGetter,
		successfulImportsCounter: successfulImportsCounter,
		failedImportsCounter:     failedImportsCounter,
		dryRun:                   dryRun,
	}
	c, err := controller.New(ControllerName, mgr, controller.Options{
		Reconciler:              r,
		MaxConcurrentReconciles: 1,
	})
	if err != nil {
		return fmt.Errorf("failed to construct controller: %w", err)
	}

	buildClusters := sets.String{}
	for buildClusterName, buildClusterManager := range buildClusterManagers {
		buildClusters.Insert(buildClusterName)
		r.buildClusterClients[buildClusterName] = imagestreamtagwrapper.MustNew(buildClusterManager.GetClient(), buildClusterManager.GetCache())
		// TODO: Watch buildCluster ImageStreams as well. For now we assume no one will tamper with them.
	}

	objectFilter, err := testInputImageStreamTagFilterFactory(log, configAgent)
	if err != nil {
		return fmt.Errorf("failed to get filter for ImageStreamTags: %w", err)
	}
	if err := c.Watch(
		source.NewKindWithCache(&imagev1.ImageStream{}, registryManager.GetCache()),
		registryClusterHandlerFactory(buildClusters, objectFilter),
	); err != nil {
		return fmt.Errorf("failed to create watch for ImageStreams: %w", err)
	}

	r.log.Info("Successfully added reconciler to manager")
	return nil
}

type objectFilter func(types.NamespacedName) bool

// registryClusterHandlerFactory produces a handler that:
// * Watches ImageStreams because ImageStreamTags do not support the watch verb
// * Extracts all ImageStramTags out of the Image
// * Filters out the ones that are not in use
// Note: We can not use a predicate because that is directly applied on the source and the source yields ImageStreams, not ImageStreamTags
// * Creates a reconcile.Request per cluster and ImageStreamTag
func registryClusterHandlerFactory(buildClusters sets.String, filter objectFilter) handler.EventHandler {
	return &handler.EnqueueRequestsFromMapFunc{ToRequests: handler.ToRequestsFunc(
		func(mo handler.MapObject) []reconcile.Request {
			imageStream, ok := mo.Object.(*imagev1.ImageStream)
			if !ok {
				logrus.WithField("type", fmt.Sprintf("%T", mo.Object)).Error("Got object that was not an ImageStram")
				return nil
			}
			var requests []reconcile.Request
			for _, imageStreamTag := range imageStream.Spec.Tags {
				// Not sure why this happens but seems to be a thing
				if imageStreamTag.Name == "" {
					serialized, err := json.Marshal(imageStreamTag)
					logrus.WithField("imagestreamtag", string(serialized)).WithField("serialization error", err).Debug("got imagestreamtag with empty name")
					continue
				}
				name := types.NamespacedName{
					Namespace: mo.Meta.GetNamespace(),
					Name:      fmt.Sprintf("%s:%s", mo.Meta.GetName(), imageStreamTag.Name),
				}
				if !filter(name) {
					continue
				}
				// We have to squeeze both the target cluster name and the imageStreamTag name into a reconcile.Request
				// Internally, this gets put onto the workqueue as a single string in namespace/name notation and split
				// later on. This means that we can not use a slash as delimiter for the cluster and the namespace.
				for _, buildCluster := range buildClusters.List() {
					name := types.NamespacedName{
						Namespace: buildCluster + clusterAndNamespaceDelimiter + name.Namespace,
						Name:      name.Name,
					}
					requests = append(requests, reconcile.Request{NamespacedName: name})
				}
			}
			return requests
		},
	),
	}
}

const clusterAndNamespaceDelimiter = "_"

func decodeRequest(req reconcile.Request) (string, types.NamespacedName, error) {
	clusterAndNamespace := strings.Split(req.Namespace, "_")
	if n := len(clusterAndNamespace); n != 2 {
		return "", types.NamespacedName{}, fmt.Errorf("didn't get two but %d segments when trying to extract cluster and namespace", n)
	}
	return clusterAndNamespace[0], types.NamespacedName{Namespace: clusterAndNamespace[1], Name: req.Name}, nil
}

type reconciler struct {
	ctx                      context.Context
	log                      *logrus.Entry
	registryClient           ctrlruntimeclient.Client
	buildClusterClients      map[string]ctrlruntimeclient.Client
	pullSecretGetter         func() []byte
	successfulImportsCounter *prometheus.CounterVec
	failedImportsCounter     *prometheus.CounterVec
	dryRun                   bool
}

func (r *reconciler) Reconcile(req reconcile.Request) (reconcile.Result, error) {
	log := r.log.WithField("request", req.String())
	log.Info("Starting reconciliation")
	err := r.reconcile(req, log)
	if err != nil {
		log.WithError(err).Error("Reconciliation failed")
	} else {
		log.Info("Finished reconciliation")
	}
	return reconcile.Result{}, controllerutil.SwallowIfTerminal(err)
}

func (r *reconciler) reconcile(req reconcile.Request, log *logrus.Entry) error {
	cluster, decoded, err := decodeRequest(req)
	if err != nil {
		return fmt.Errorf("failed to decode request %s: %w", req, err)
	}
	// Propagate the cluster field back up
	*log = *log.WithField("cluster", cluster)

	// Fail asap if we cannot reconcile this
	client, ok := r.buildClusterClients[cluster]
	if !ok {
		return controllerutil.TerminalError(fmt.Errorf("no client for cluster %s available", cluster))
	}

	sourceImageStreamTag := &imagev1.ImageStreamTag{}
	if err := r.registryClient.Get(r.ctx, decoded, sourceImageStreamTag); err != nil {
		if apierrors.IsNotFound(err) {
			log.Debug("Source imageStreamTag not found")
			return nil
		}
		return fmt.Errorf("failed to get imageStreamTag %s from registry cluster: %w", decoded.String(), err)
	}

	isCurrent, err := r.isImageStreamTagCurrent(decoded, client, sourceImageStreamTag)
	if err != nil {
		return fmt.Errorf("failed to check if imageStreamTag %s on cluster %s is current: %w", decoded.String(), cluster, err)
	}
	if isCurrent {
		log.Debug("ImageStreamTag is current")
		return nil
	}

	if err := client.Get(r.ctx, types.NamespacedName{Name: decoded.Namespace}, &corev1.Namespace{}); err != nil {
		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to check if namespace %s exists: %w", decoded.Namespace, err)
		}
		if err := client.Create(r.ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: decoded.Namespace}}); err != nil && !apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("failed to create namespace %s: %w", decoded.Namespace, err)
		}
	}

	if err := r.ensureImagePullSecret(decoded.Namespace, client); err != nil {
		return fmt.Errorf("failed to ensure imagePullSecret: %w", err)
	}

	split := strings.Split(decoded.Name, ":")
	if n := len(split); n != 2 {
		return fmt.Errorf("splitting imagestremtag name %s by : did not yield two but %d results", decoded.Name, n)
	}
	imageStreamName, imageTag := split[0], split[1]

	imageStreamImport := &imagev1.ImageStreamImport{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: decoded.Namespace,
			Name:      imageStreamName,
		},
		Spec: imagev1.ImageStreamImportSpec{
			Import: true,
			Images: []imagev1.ImageImportSpec{{
				From: corev1.ObjectReference{
					Kind: "DockerImage",
					Name: publicURLForImage(sourceImageStreamTag.Image.DockerImageReference),
				},
				To: &corev1.LocalObjectReference{Name: imageTag},
			}},
		},
	}

	if r.dryRun {
		serialized, err := json.Marshal(imageStreamImport)
		if err != nil {
			log.WithError(err).Error("failed to marshal ImageStreamImport")
		}
		log.WithField("imagestreamtagimport", string(serialized)).Info("Not creating imagestreamimport because dry-run is enabled")
		r.successfulImportsCounter.WithLabelValues(cluster, decoded.Namespace).Inc()
		return nil
	}
	// ImageStreamImport is not an ordinary api but a virtual one that does the import synchronously
	if err := client.Create(r.ctx, imageStreamImport); err != nil {
		r.failedImportsCounter.WithLabelValues(cluster, decoded.Namespace).Inc()
		return fmt.Errorf("failed to import Image: %w", err)
	}

	// This should never be needed, but we shouldn't panic if the server screws up
	if imageStreamImport.Status.Images == nil {
		imageStreamImport.Status.Images = []imagev1.ImageImportStatus{{}}
	}
	if imageStreamImport.Status.Images[0].Image == nil {
		return fmt.Errorf("imageStreamImport did not succeed: reason: %s, message: %s", imageStreamImport.Status.Images[0].Status.Reason, imageStreamImport.Status.Images[0].Status.Message)
	}

	r.successfulImportsCounter.WithLabelValues(cluster, decoded.Namespace).Inc()

	log.Debug("Imported successfully")
	return nil
}

func (r *reconciler) isImageStreamTagCurrent(
	name types.NamespacedName,
	targetClient ctrlruntimeclient.Client,
	reference *imagev1.ImageStreamTag,
) (bool, error) {

	imageStreamTag := &imagev1.ImageStreamTag{}
	if err := targetClient.Get(r.ctx, name, imageStreamTag); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("failed to get imagestreamtag %s: %w", name.String(), err)
	}

	return imageStreamTag.Image.Name == reference.Image.Name, nil
}

const pullSecretName = "registry-cluster-pull-secret"

func (r *reconciler) ensureImagePullSecret(namespace string, client ctrlruntimeclient.Client) error {
	referenceSecret := r.generateReferencePullSecret(namespace)
	name := types.NamespacedName{Namespace: namespace, Name: pullSecretName}
	secret := &corev1.Secret{}
	if err := client.Get(r.ctx, name, secret); err != nil {
		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to get secret %s: %w", name.String(), err)
		}
		// Tolerate IsAlreadyExist, another routine might have created it or our cache is stale
		if err := client.Create(r.ctx, referenceSecret); err != nil && !apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("failed to create secret %s/%s: %v", referenceSecret.Namespace, referenceSecret.Name, err)
		}
	}

	// Make it comparable by resetting the ObjectMeta fields the apiserver sets
	resourceVersion := secret.ObjectMeta.ResourceVersion
	secret.ObjectMeta = referenceSecret.ObjectMeta
	if !apiequality.Semantic.DeepEqual(secret, referenceSecret) {
		referenceSecret.ResourceVersion = resourceVersion
		if err := client.Update(r.ctx, referenceSecret); err != nil {
			return fmt.Errorf("failed to update secret %s/%s: %w", referenceSecret.Namespace, referenceSecret.Name, err)
		}
	}

	return nil
}

func (r *reconciler) generateReferencePullSecret(namespace string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      pullSecretName,
		},
		Data: map[string][]byte{
			corev1.DockerConfigJsonKey: r.pullSecretGetter(),
		},
		Type: corev1.SecretTypeDockerConfigJson,
	}
}

func testInputImageStreamTagFilterFactory(l *logrus.Entry, ca agents.ConfigAgent) (objectFilter, error) {
	const indexName = "config-by-test-input-imagestreamtag"
	if err := ca.AddIndex(indexName, indexConfigsByTestInputImageStramTag); err != nil {
		return nil, fmt.Errorf("failed to add %s index to configAgent: %w", indexName, err)
	}
	l = logrus.WithField("subcomponent", "test-input-image-stream-tag-filter")
	return func(nn types.NamespacedName) bool {
		result, err := ca.GetFromIndex(indexName, nn.String())
		// Today, GetFromIndex only errors if the index does not exist, so this should
		// never happen.
		if err != nil {
			l.WithField("name", nn.String()).WithError(err).Error("Failed to get configs from index")
			return false
		}
		return len(result) > 0
	}, nil
}

func indexConfigsByTestInputImageStramTag(cfg api.ReleaseBuildConfiguration) []string {
	m := apihelper.TestInputImageStreamTagsFromConfig(cfg)
	var result []string
	for key := range m {
		result = append(result, key)
	}
	return result
}

func publicURLForImage(potentiallyPrivate string) string {
	return strings.ReplaceAll(potentiallyPrivate, "docker-registry.default.svc:5000", "registry.svc.ci.openshift.org")
}
