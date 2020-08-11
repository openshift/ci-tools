package testimagesdistributor

import (
	"context"
	"fmt"
	"strings"

	imagev1 "github.com/openshift/api/image/v1"
	"github.com/openshift/ci-tools/pkg/util/imagestreamtagwrapper"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	crcontrollerutil "sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	"github.com/openshift/ci-tools/pkg/api"
	apihelper "github.com/openshift/ci-tools/pkg/api/helper"
	testimagestreamtagimportv1 "github.com/openshift/ci-tools/pkg/api/testimagestreamtagimport/v1"
	controllerutil "github.com/openshift/ci-tools/pkg/controller/util"
	"github.com/openshift/ci-tools/pkg/load/agents"
	"github.com/openshift/ci-tools/pkg/registry"
	"github.com/openshift/ci-tools/pkg/util/imagestreamtagmapper"
)

const ControllerName = "test_images_distributor"

func AddToManager(mgr manager.Manager,
	registryManager manager.Manager,
	buildClusterManagers map[string]manager.Manager,
	configAgent agents.ConfigAgent,
	pullSecretGetter func() []byte,
	resolver registry.Resolver,
	additionalImageStreamTags sets.String,
	additionalImageStreams sets.String,
	additionalImageStreamNamespaces sets.String,
) error {
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

	buildClusters := sets.String{}
	for buildClusterName, buildClusterManager := range buildClusterManagers {
		buildClusters.Insert(buildClusterName)
		r.buildClusterClients[buildClusterName] = imagestreamtagwrapper.MustNew(buildClusterManager.GetClient(), buildClusterManager.GetCache())
		// TODO: Watch buildCluster ImageStreams as well. For now we assume no one will tamper with them.
		if buildClusterName == "app.ci" {
			if err := c.Watch(
				source.NewKindWithCache(&testimagestreamtagimportv1.TestImageStreamTagImport{}, buildClusterManager.GetCache()),
				testImageStreamTagImportHandler(),
			); err != nil {
				return fmt.Errorf("failed to create watch for testimagestreamtagimports: %w", err)
			}
		}
	}

	objectFilter, err := testInputImageStreamTagFilterFactory(log, configAgent, resolver, additionalImageStreamTags, additionalImageStreams, additionalImageStreamNamespaces)
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

func testImageStreamTagImportHandler() handler.EventHandler {
	return &handler.EnqueueRequestsFromMapFunc{ToRequests: handler.ToRequestsFunc(
		func(mo handler.MapObject) []reconcile.Request {
			testimagestreamtagimport, ok := mo.Object.(*testimagestreamtagimportv1.TestImageStreamTagImport)
			if !ok {
				logrus.WithField("type", fmt.Sprintf("%T", mo.Object)).Error("Got object that was not an ImageStram")
				return nil
			}
			return []reconcile.Request{{NamespacedName: types.NamespacedName{
				Namespace: testimagestreamtagimport.Spec.ClusterName + clusterAndNamespaceDelimiter + testimagestreamtagimport.Spec.Namespace,
				Name:      testimagestreamtagimport.Spec.Name,
			}}}
		},
	)}
}

type objectFilter func(types.NamespacedName) bool

// registryClusterHandlerFactory produces a handler that:
// * Watches ImageStreams because ImageStreamTags do not support the watch verb
// * Extracts all ImageStramTags out of the Image
// * Filters out the ones that are not in use
// Note: We can not use a predicate because that is directly applied on the source and the source yields ImageStreams, not ImageStreamTags
// * Creates a reconcile.Request per cluster and ImageStreamTag
func registryClusterHandlerFactory(buildClusters sets.String, filter objectFilter) handler.EventHandler {
	return imagestreamtagmapper.New(func(in reconcile.Request) []reconcile.Request {
		if !filter(in.NamespacedName) {
			return nil
		}

		var requests []reconcile.Request
		// We have to squeeze both the target cluster name and the imageStreamTag name into a reconcile.Request
		// Internally, this gets put onto the workqueue as a single string in namespace/name notation and split
		// later on. This means that we can not use a slash as delimiter for the cluster and the namespace.
		for _, buildCluster := range buildClusters.List() {
			name := types.NamespacedName{
				Namespace: buildCluster + clusterAndNamespaceDelimiter + in.Namespace,
				Name:      in.Name,
			}
			requests = append(requests, reconcile.Request{NamespacedName: name})
		}
		return requests
	})
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
			log.Trace("Source imageStreamTag not found")
			return nil
		}
		return fmt.Errorf("failed to get imageStreamTag %s from registry cluster: %w", decoded.String(), err)
	}
	*log = *log.WithField("docker_image_reference", sourceImageStreamTag.Image.DockerImageReference)
	if !isImportAllowed(sourceImageStreamTag.Image.DockerImageReference) {
		log.Debug("Import not allowed, ignoring")
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

	if err := r.ensureCIOperatorRoleBinding(decoded.Namespace, client, log); err != nil {
		return fmt.Errorf("failed to ensure rolebinding: %w", err)
	}
	if err := r.ensureCIOperatorRole(decoded.Namespace, client, log); err != nil {
		return fmt.Errorf("failed to ensure role: %w", err)
	}
	if err := r.ensureImageStream(decoded, client, log); err != nil {
		return fmt.Errorf("failed to ensure imagestream: %w", err)
	}

	isCurrent, err := r.isImageStreamTagCurrent(decoded, client, sourceImageStreamTag)
	if err != nil {
		return fmt.Errorf("failed to check if imageStreamTag %s on cluster %s is current: %w", decoded.String(), cluster, err)
	}
	if isCurrent {
		log.Debug("ImageStreamTag is current")
		return nil
	}

	if err := r.ensureImagePullSecret(decoded.Namespace, client, log); err != nil {
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

func (r *reconciler) ensureImagePullSecret(namespace string, client ctrlruntimeclient.Client, log *logrus.Entry) error {
	secret, mutateFn := r.pullSecret(namespace)
	return upsertObject(r.ctx, client, secret, mutateFn, log)
}

const ciOperatorPullerRoleName = "ci-operator-image-puller"

func ciOperatorRole(namespace string) (*rbacv1.Role, crcontrollerutil.MutateFn) {
	r := &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      ciOperatorPullerRoleName,
		},
	}
	return r, func() error {
		r.Rules = []rbacv1.PolicyRule{
			{
				APIGroups: []string{"image.openshift.io"},
				Resources: []string{"imagestreamtags", "imagestreams", "imagestreams/layers"},
				Verbs:     []string{"get", "list", "watch"},
			},
		}
		return nil
	}
}

func (r *reconciler) ensureCIOperatorRole(namespace string, client ctrlruntimeclient.Client, log *logrus.Entry) error {
	role, mutateFn := ciOperatorRole(namespace)
	return upsertObject(r.ctx, client, role, mutateFn, log)
}

func ciOperatorRoleBinding(namespace string) (*rbacv1.RoleBinding, crcontrollerutil.MutateFn) {
	rb := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      "ci-operator-image-puller",
		},
	}
	return rb, func() error {
		rb.Subjects = []rbacv1.Subject{{
			Kind:      rbacv1.ServiceAccountKind,
			Name:      "ci-operator",
			Namespace: "ci",
		}}
		rb.RoleRef = rbacv1.RoleRef{
			APIGroup: rbacv1.GroupName,
			Kind:     "Role",
			// system:image-puller is not enough, as we need get for imagestreamtags
			Name: ciOperatorPullerRoleName,
		}
		return nil
	}
}

func (r *reconciler) ensureCIOperatorRoleBinding(namespace string, client ctrlruntimeclient.Client, log *logrus.Entry) error {
	roleBinding, mutateFn := ciOperatorRoleBinding(namespace)
	return upsertObject(r.ctx, client, roleBinding, mutateFn, log)
}

func imagestream(namespace, name string) (*imagev1.ImageStream, crcontrollerutil.MutateFn) {
	stream := &imagev1.ImageStream{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
		},
	}
	return stream, func() error {
		stream.Spec.LookupPolicy.Local = true
		return nil
	}
}

func (r *reconciler) ensureImageStream(imagestreamTagName types.NamespacedName, client ctrlruntimeclient.Client, log *logrus.Entry) error {
	colonSplit := strings.Split(imagestreamTagName.Name, ":")
	if n := len(colonSplit); n != 2 {
		return fmt.Errorf("when splitting imagestreamtagname %s by : expected two results, got %d", imagestreamTagName.Name, n)
	}
	namespace, name := imagestreamTagName.Namespace, colonSplit[0]
	stream, mutateFn := imagestream(namespace, name)
	return upsertObject(r.ctx, client, stream, mutateFn, log)
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

func testInputImageStreamTagFilterFactory(l *logrus.Entry, ca agents.ConfigAgent, resolver registry.Resolver, additionalImageStreamTags, additionalImageStreams, additionalImageStreamNamespaces sets.String) (objectFilter, error) {
	const indexName = "config-by-test-input-imagestreamtag"
	if err := ca.AddIndex(indexName, indexConfigsByTestInputImageStramTag(resolver)); err != nil {
		return nil, fmt.Errorf("failed to add %s index to configAgent: %w", indexName, err)
	}
	l = logrus.WithField("subcomponent", "test-input-image-stream-tag-filter")
	return func(nn types.NamespacedName) bool {
		if additionalImageStreamTags.Has(nn.String()) {
			return true
		}
		if additionalImageStreamNamespaces.Has(nn.Namespace) {
			return true
		}
		imageStreamTagResult, err := ca.GetFromIndex(indexName, nn.String())
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
		imageStreamResult, err := ca.GetFromIndex(indexName, indexKeyForImageStream(imageStreamName.Namespace, imageStreamName.Name))
		if err != nil {
			l.WithField("name", imageStreamName.String()).WithError(err).Error("Failed to get imagestream configs from index")
			return false
		}
		return len(imageStreamResult) > 0
	}, nil
}

func imageStreamNameFromImageStreamTagName(nn types.NamespacedName) (types.NamespacedName, error) {
	colonSplit := strings.Split(nn.Name, ":")
	if n := len(colonSplit); n != 2 {
		return types.NamespacedName{}, fmt.Errorf("splitting %s by `:` didn't yield two but %d results", nn.Name, n)
	}
	return types.NamespacedName{Namespace: nn.Namespace, Name: colonSplit[0]}, nil
}

func indexConfigsByTestInputImageStramTag(resolver registry.Resolver) agents.IndexFn {
	return func(cfg api.ReleaseBuildConfiguration) []string {

		log := logrus.WithFields(logrus.Fields{"org": cfg.Metadata.Org, "repo": cfg.Metadata.Repo, "branch": cfg.Metadata.Branch})
		for idx, testStep := range cfg.Tests {
			if testStep.MultiStageTestConfiguration != nil {
				resolved, err := resolver.Resolve(testStep.As, *testStep.MultiStageTestConfiguration)
				if err != nil {
					log.WithError(err).Error("Failed to resolve MultiStageTestConfiguration")
				}
				cfg.Tests[idx].MultiStageTestConfigurationLiteral = &resolved
				// We always need to set to nil or we will get another error later.
				cfg.Tests[idx].MultiStageTestConfiguration = nil
			}
		}
		for idx, rawStep := range cfg.RawSteps {
			if rawStep.TestStepConfiguration != nil && rawStep.TestStepConfiguration.MultiStageTestConfiguration != nil {
				resolved, err := resolver.Resolve(rawStep.TestStepConfiguration.As, *rawStep.TestStepConfiguration.MultiStageTestConfiguration)
				if err != nil {
					log.WithError(err).Error("Failed to resolve MultiStageTestConfiguration")
				}
				// We always need to set to nil or we will get another error later.
				cfg.RawSteps[idx].TestStepConfiguration.MultiStageTestConfigurationLiteral = &resolved
				cfg.RawSteps[idx].TestStepConfiguration.MultiStageTestConfiguration = nil
			}

		}
		m, err := apihelper.TestInputImageStreamTagsFromResolvedConfig(cfg)
		if err != nil {
			// Should never happen as we set it to nil above
			log.WithError(err).Error("Got error from TestInputImageStreamTagsFromResolvedConfig. This is a software bug.")
		}
		var result []string
		for key := range m {
			result = append(result, key)
		}

		if cfg.ReleaseTagConfiguration != nil {
			result = append(result, indexKeyForImageStream(cfg.ReleaseTagConfiguration.Namespace, cfg.ReleaseTagConfiguration.Name))
		}
		return result
	}
}

func indexKeyForImageStream(namespace, name string) string {
	return "imagestream_" + namespace + name
}

func publicURLForImage(potentiallyPrivate string) string {
	return strings.ReplaceAll(potentiallyPrivate, "docker-registry.default.svc:5000", api.DomainForService(api.ServiceRegistry))
}

func upsertObject(ctx context.Context, c ctrlruntimeclient.Client, obj crcontrollerutil.Object, mutateFn crcontrollerutil.MutateFn, log *logrus.Entry) error {
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

var allowedRegistries = sets.NewString("registry.svc.ci.openshift.org",
	"docker-registry.default.svc:5000",
	"registry.access.redhat.com",
	"quay.io",
	"gcr.io",
	"docker.io",
)

func isImportAllowed(pullSpec string) bool {
	i := strings.Index(pullSpec, "/")
	if i == -1 {
		return false
	}
	return allowedRegistries.Has(pullSpec[:i])
}
