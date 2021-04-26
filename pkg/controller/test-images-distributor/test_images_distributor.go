package testimagesdistributor

import (
	"context"
	"fmt"
	"strings"

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
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	imagev1 "github.com/openshift/api/image/v1"

	"github.com/openshift/ci-tools/pkg/api"
	apihelper "github.com/openshift/ci-tools/pkg/api/helper"
	testimagestreamtagimportv1 "github.com/openshift/ci-tools/pkg/api/testimagestreamtagimport/v1"
	controllerutil "github.com/openshift/ci-tools/pkg/controller/util"
	"github.com/openshift/ci-tools/pkg/load/agents"
	"github.com/openshift/ci-tools/pkg/util/imagestreamtagmapper"
	"github.com/openshift/ci-tools/pkg/util/imagestreamtagwrapper"
)

const ControllerName = "test_images_distributor"

func AddToManager(mgr manager.Manager,
	registryClusterName string,
	registryManager manager.Manager,
	buildClusterManagers map[string]manager.Manager,
	configAgent agents.ConfigAgent,
	resolver agents.RegistryAgent,
	additionalImageStreamTags sets.String,
	additionalImageStreams sets.String,
	additionalImageStreamNamespaces sets.String,
	forbiddenRegistries sets.String,
) error {
	log := logrus.WithField("controller", ControllerName)

	r := &reconciler{
		log:                 log,
		registryClusterName: registryClusterName,
		registryClient:      imagestreamtagwrapper.MustNew(registryManager.GetClient(), registryManager.GetCache()),
		buildClusterClients: map[string]ctrlruntimeclient.Client{},
		forbiddenRegistries: forbiddenRegistries,
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
		if buildClusterName == "api.ci" {
			log.Debug("distribution to api.ci is disabled")
			continue
		}
		buildClusters.Insert(buildClusterName)
		r.buildClusterClients[buildClusterName] = imagestreamtagwrapper.MustNew(buildClusterManager.GetClient(), buildClusterManager.GetCache())
	}

	// TODO: Watch buildCluster ImageStreams as well. For now we assume no one will tamper with them.
	if err := c.Watch(
		source.NewKindWithCache(&testimagestreamtagimportv1.TestImageStreamTagImport{}, mgr.GetCache()),
		testImageStreamTagImportHandler(),
	); err != nil {
		return fmt.Errorf("failed to create watch for testimagestreamtagimports: %w", err)
	}

	var appCIClient ctrlruntimeclient.Client

	if client, ok := r.buildClusterClients["app.ci"]; ok {
		appCIClient = client
	} else {
		//when app.ci is registryCluster
		appCIClient = imagestreamtagwrapper.MustNew(mgr.GetClient(), mgr.GetCache())
	}

	objectFilter, err := testInputImageStreamTagFilterFactory(log, configAgent, appCIClient, resolver, additionalImageStreamTags, additionalImageStreams, additionalImageStreamNamespaces)
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
	return handler.EnqueueRequestsFromMapFunc(func(o ctrlruntimeclient.Object) []reconcile.Request {
		testimagestreamtagimport, ok := o.(*testimagestreamtagimportv1.TestImageStreamTagImport)
		if !ok {
			logrus.WithField("type", fmt.Sprintf("%T", o)).Error("Got object that was not an ImageStram")
			return nil
		}
		return []reconcile.Request{{NamespacedName: types.NamespacedName{
			Namespace: testimagestreamtagimport.Spec.ClusterName + clusterAndNamespaceDelimiter + testimagestreamtagimport.Spec.Namespace,
			Name:      testimagestreamtagimport.Spec.Name,
		}}}
	})
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
	log                 *logrus.Entry
	registryClusterName string
	registryClient      ctrlruntimeclient.Client
	buildClusterClients map[string]ctrlruntimeclient.Client
	forbiddenRegistries sets.String
}

func (r *reconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	log := r.log.WithField("request", req.String())
	log.Info("Starting reconciliation")
	err := r.reconcile(ctx, req, log)
	if err != nil && !apierrors.IsConflict(err) {
		log.WithError(err).Error("Reconciliation failed")
	} else {
		log.Info("Finished reconciliation")
	}
	return reconcile.Result{}, controllerutil.SwallowIfTerminal(err)
}

func (r *reconciler) reconcile(ctx context.Context, req reconcile.Request, log *logrus.Entry) error {
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
	if err := r.registryClient.Get(ctx, decoded, sourceImageStreamTag); err != nil {
		if apierrors.IsNotFound(err) {
			log.Debug("Source imageStreamTag not found")
			return nil
		}
		return fmt.Errorf("failed to get imageStreamTag %s from registry cluster: %w", decoded.String(), err)
	}

	imageStreamNameAndTag := strings.Split(decoded.Name, ":")
	if n := len(imageStreamNameAndTag); n != 2 {
		return fmt.Errorf("when splitting imagestreamtagname %s by : expected two results, got %d", decoded.Name, n)
	}
	imageStreamName, imageTag := imageStreamNameAndTag[0], imageStreamNameAndTag[1]
	isName := types.NamespacedName{Namespace: decoded.Namespace, Name: imageStreamName}
	sourceImageStream := &imagev1.ImageStream{}
	if err := r.registryClient.Get(ctx, isName, sourceImageStream); err != nil {
		return fmt.Errorf("failed to get imageStream %s from registry cluster: %w", isName.String(), err)
	}

	*log = *log.WithField("docker_image_reference", sourceImageStreamTag.Image.DockerImageReference)
	if isImportForbidden(sourceImageStreamTag.Image.DockerImageReference, r.forbiddenRegistries) {
		log.Debugf("Import from any cluster in %s is forbidden, ignoring", r.forbiddenRegistries)
		return nil
	}

	if err := client.Get(ctx, types.NamespacedName{Name: decoded.Namespace}, &corev1.Namespace{}); err != nil {
		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to check if namespace %s exists: %w", decoded.Namespace, err)
		}
		if err := client.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: decoded.Namespace}}); err != nil && !apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("failed to create namespace %s: %w", decoded.Namespace, err)
		}
	}

	if err := r.ensureCIOperatorRoleBinding(ctx, decoded.Namespace, client, log); err != nil {
		return fmt.Errorf("failed to ensure rolebinding: %w", err)
	}
	if err := r.ensureCIOperatorRole(ctx, decoded.Namespace, client, log); err != nil {
		return fmt.Errorf("failed to ensure role: %w", err)
	}
	if err := r.ensureImageStream(ctx, sourceImageStream, client, log); err != nil {
		return fmt.Errorf("failed to ensure imagestream: %w", err)
	}

	isCurrent, err := r.isImageStreamTagCurrent(ctx, decoded, client, sourceImageStreamTag)
	if err != nil {
		return fmt.Errorf("failed to check if imageStreamTag %s on cluster %s is current: %w", decoded.String(), cluster, err)
	}

	targetImageStream := &imagev1.ImageStream{}
	if err := client.Get(ctx, isName, targetImageStream); err != nil {
		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to get imageStream %s from target cluster %s: %w", isName.String(), cluster, err)
		}
	}
	needReImport := needReImport(sourceImageStream, targetImageStream, imageTag)
	if !needReImport && isCurrent {
		log.WithField("needReImport", needReImport).WithField("isCurrent", isCurrent).Debug("ImageStreamTag is skipped")
		return nil
	}
	if err := controllerutil.EnsureImagePullSecret(ctx, decoded.Namespace, client, log); err != nil {
		return fmt.Errorf("failed to ensure imagePullSecret on cluster %s: %w", cluster, err)
	}
	publicDomainForImage, err := api.PublicDomainForImage(r.registryClusterName, sourceImageStreamTag.Image.DockerImageReference)
	if err != nil {
		return fmt.Errorf("failed to get public domain for %s and %s: %w", r.registryClusterName, sourceImageStreamTag.Image.DockerImageReference, err)
	}
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
					Name: publicDomainForImage,
				},
				To: &corev1.LocalObjectReference{Name: imageTag},
				ReferencePolicy: imagev1.TagReferencePolicy{
					Type: imagev1.LocalTagReferencePolicy,
				},
			}},
		},
	}

	// ImageStreamImport is not an ordinary api but a virtual one that does the import synchronously
	if err := client.Create(ctx, imageStreamImport); err != nil {
		controllerutil.CountImportResult(ControllerName, cluster, decoded.Namespace, imageStreamName, false)
		return fmt.Errorf("failed to import Image: %w", err)
	}

	// This should never be needed, but we shouldn't panic if the server screws up
	if imageStreamImport.Status.Images == nil {
		imageStreamImport.Status.Images = []imagev1.ImageImportStatus{{}}
	}
	if imageStreamImport.Status.Images[0].Image == nil {
		return fmt.Errorf("imageStreamImport did not succeed: reason: %s, message: %s", imageStreamImport.Status.Images[0].Status.Reason, imageStreamImport.Status.Images[0].Status.Message)
	}

	controllerutil.CountImportResult(ControllerName, cluster, decoded.Namespace, imageStreamName, true)

	log.Debug("Imported successfully")
	return nil
}

// needReImport returns true if and only if the source is a local reference and the target points to an api.ci image
func needReImport(source, target *imagev1.ImageStream, tag string) bool {
	sourceDockerImageReference := getDockerImageReference(source, tag)
	targetDockerImageReference := getDockerImageReference(target, tag)
	return strings.HasPrefix(sourceDockerImageReference, "image-registry.openshift-image-registry.svc:5000") && strings.HasPrefix(targetDockerImageReference, api.ServiceDomainAPICIRegistry)
}

func getDockerImageReference(stream *imagev1.ImageStream, tagName string) string {
	if stream == nil {
		return ""
	}
	for _, tag := range stream.Status.Tags {
		if tag.Tag == tagName {
			if len(tag.Items) == 0 {
				return ""
			}
			return tag.Items[0].DockerImageReference
		}
	}
	return ""
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

func (r *reconciler) ensureCIOperatorRole(ctx context.Context, namespace string, client ctrlruntimeclient.Client, log *logrus.Entry) error {
	role, mutateFn := ciOperatorRole(namespace)
	return upsertObject(ctx, client, role, mutateFn, log)
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

func (r *reconciler) ensureCIOperatorRoleBinding(ctx context.Context, namespace string, client ctrlruntimeclient.Client, log *logrus.Entry) error {
	roleBinding, mutateFn := ciOperatorRoleBinding(namespace)
	return upsertObject(ctx, client, roleBinding, mutateFn, log)
}

// ci-operator uses the release controller configuration to determine
// the version of OpenShift we create from the ImageStream, so we need
// to copy the annotation if it exists
const releaseConfigAnnotation = "release.openshift.io/config"

func imagestream(imageStream *imagev1.ImageStream) (*imagev1.ImageStream, crcontrollerutil.MutateFn) {
	stream := &imagev1.ImageStream{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: imageStream.Namespace,
			Name:      imageStream.Name,
		},
	}
	return stream, func() error {
		if config, set := imageStream.Annotations[releaseConfigAnnotation]; set {
			if stream.Annotations == nil {
				stream.Annotations = map[string]string{}
			}
			stream.Annotations[releaseConfigAnnotation] = config
		}
		stream.Spec.LookupPolicy.Local = true
		for i := range stream.Spec.Tags {
			stream.Spec.Tags[i].ReferencePolicy.Type = imagev1.LocalTagReferencePolicy
		}
		return nil
	}
}

func (r *reconciler) ensureImageStream(ctx context.Context, imageStream *imagev1.ImageStream, client ctrlruntimeclient.Client, log *logrus.Entry) error {
	stream, mutateFn := imagestream(imageStream)
	return upsertObject(ctx, client, stream, mutateFn, log)
}

type registryResolver interface {
	ResolveConfig(config api.ReleaseBuildConfiguration) (api.ReleaseBuildConfiguration, error)
}

func testInputImageStreamTagFilterFactory(l *logrus.Entry, ca agents.ConfigAgent, client ctrlruntimeclient.Client, resolver registryResolver, additionalImageStreamTags, additionalImageStreams, additionalImageStreamNamespaces sets.String) (objectFilter, error) {
	const indexName = "config-by-test-input-imagestreamtag"
	if err := ca.AddIndex(indexName, indexConfigsByTestInputImageStreamTag(resolver)); err != nil {
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
		if len(imageStreamResult) > 0 {
			return true
		}

		// We have to consider testimagestreamtagimports to cover the case of:
		// * rehearsal is created, references outdated/inexistent streamtag
		// * rehearsal fails
		// * streamtag gets fixed up
		// * rehearsal is re-executed
		// * If we don't re-consider the list here every time, we won't distribute
		//   the fixed up version of the streamtag
		// Because we don't know for which cluster the request is, this results in
		// us importing it into all clusters which is an acceptable trade-off.
		imports := &testimagestreamtagimportv1.TestImageStreamTagImportList{}
		if err := client.List(context.TODO(), imports); err != nil {
			l.WithError(err).Error("Failed to list testimagestreamtagimport")
		}
		for _, imp := range imports.Items {
			if imp.Spec.Name == nn.Name && imp.Spec.Namespace == nn.Namespace {
				return true
			}
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

func indexConfigsByTestInputImageStreamTag(resolver registryResolver) agents.IndexFn {
	return func(cfg api.ReleaseBuildConfiguration) []string {

		log := logrus.WithFields(logrus.Fields{"org": cfg.Metadata.Org, "repo": cfg.Metadata.Repo, "branch": cfg.Metadata.Branch})
		cfg, err := resolver.ResolveConfig(cfg)
		if err != nil {
			log.WithError(err).Error("Failed to resolve MultiStageTestConfiguration")
			return nil
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

func upsertObject(ctx context.Context, c ctrlruntimeclient.Client, obj ctrlruntimeclient.Object, mutateFn crcontrollerutil.MutateFn, log *logrus.Entry) error {
	// Create log here in case the operation fails and the obj is nil
	log = log.WithFields(logrus.Fields{"namespace": obj.GetNamespace(), "name": obj.GetName(), "type": fmt.Sprintf("%T", obj)})
	result, err := crcontrollerutil.CreateOrUpdate(ctx, c, obj, mutateFn)
	log = log.WithField("operation", result)
	if err != nil && !apierrors.IsConflict(err) {
		log.WithError(err).Error("Upsert failed")
	} else if result != crcontrollerutil.OperationResultNone {
		log.Info("Upsert succeeded")
	}
	return err
}

func isImportForbidden(pullSpec string, forbiddenRegistries sets.String) bool {
	for _, reg := range forbiddenRegistries.List() {
		if strings.HasPrefix(pullSpec, reg) {
			return true
		}
	}
	return false
}
