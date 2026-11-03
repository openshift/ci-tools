package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	egressfirewallv1 "github.com/ovn-org/ovn-kubernetes/go-controller/pkg/crd/egressfirewall/v1"
	"github.com/sirupsen/logrus"

	authapi "k8s.io/api/authorization/v1"
	coreapi "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	rbacapi "k8s.io/api/rbac/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/sets"
	coreclientset "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/util/retry"
	utilpointer "k8s.io/utils/pointer"
	"k8s.io/utils/ptr"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	crcontrollerutil "sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	imageapi "github.com/openshift/api/image/v1"
	projectapi "github.com/openshift/api/project/v1"
	buildclientset "github.com/openshift/client-go/build/clientset/versioned/typed/build/v1"
	projectclientset "github.com/openshift/client-go/project/clientset/versioned"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/api/nsttl"
	"github.com/openshift/ci-tools/pkg/labeledclient"
	"github.com/openshift/ci-tools/pkg/metrics"
	"github.com/openshift/ci-tools/pkg/steps"
	"github.com/openshift/ci-tools/pkg/util"
)

func (o *options) initializeNamespace() error {
	// We have to keep the project client because it return a project for a projectCreationRequest, ctrlruntimeclient can not do dark magic like that
	projectGetter, err := projectclientset.NewForConfig(o.clusterConfig)
	if err != nil {
		return fmt.Errorf("could not get project client for cluster config: %w", err)
	}
	ctrlClient, err := ctrlruntimeclient.New(o.clusterConfig, ctrlruntimeclient.Options{})
	if err != nil {
		return fmt.Errorf("failed to construct client: %w", err)
	}
	client := ctrlruntimeclient.NewNamespacedClient(ctrlClient, o.namespace)
	client = labeledclient.Wrap(client, o.jobSpec)
	ctx := context.Background()

	logrus.Debugf("Creating namespace %s", o.namespace)
	authTimeout := 15 * time.Second
	initBeginning := time.Now()
	for {
		project, err := projectGetter.ProjectV1().ProjectRequests().Create(context.TODO(), &projectapi.ProjectRequest{
			ObjectMeta: metav1.ObjectMeta{
				Name:   o.namespace,
				Labels: map[string]string{api.DPTPRequesterLabel: "ci-operator"},
			},
			DisplayName: fmt.Sprintf("%s - %s", o.namespace, o.jobSpec.Job),
			Description: jobDescription(o.jobSpec),
		}, metav1.CreateOptions{})
		if err != nil && !kerrors.IsAlreadyExists(err) {
			return fmt.Errorf("could not set up namespace for test: %w", err)
		}
		if err != nil {
			project, err = projectGetter.ProjectV1().Projects().Get(context.TODO(), o.namespace, metav1.GetOptions{})
			if err != nil {
				if kerrors.IsNotFound(err) {
					continue
				}
				// wait a few seconds for auth caches to catch up
				if kerrors.IsForbidden(err) && time.Since(initBeginning) < authTimeout {
					time.Sleep(time.Second)
					continue
				}
				return fmt.Errorf("failed to wait for authentication cache to warm up after %s: %w", authTimeout, err)
			}
		}
		if project.Status.Phase == coreapi.NamespaceTerminating {
			logrus.Info("Waiting for namespace to finish terminating before creating another")
			time.Sleep(3 * time.Second)
			continue
		}
		break
	}

	ssarStart := time.Now()
	var selfSubjectAccessReviewSucceeded bool
	for i := 0; i < 30; i++ {
		sar := &authapi.SelfSubjectAccessReview{Spec: authapi.SelfSubjectAccessReviewSpec{ResourceAttributes: &authapi.ResourceAttributes{
			Namespace: o.namespace,
			Verb:      "create",
			Resource:  "rolebindings",
		}}}
		if err := client.Create(ctx, sar); err != nil {
			logrus.WithError(err).Warn("Failed to create SelfSubjectAccessReview when checking to see if the namespace was initialized.")
			continue
		}
		if sar.Status.Allowed {
			selfSubjectAccessReviewSucceeded = true
			break
		}
		logrus.Debugf("[%d/30] RBAC in namespace not yet ready, sleeping for a second...", i)
		time.Sleep(time.Second)
	}
	logrus.Debugf("Spent %v waiting for RBAC to initialize in the new namespace.", time.Since(ssarStart))
	if !selfSubjectAccessReviewSucceeded {
		logrus.Error("Timed out waiting for RBAC to initialize in the test namespace.")
		return errors.New("timed out waiting for RBAC")
	}

	// Annotate the namespace for cleanup by external tooling (ci-ns-ttl-controller)
	// Unfortunately we cannot set the annotations right away when we create a project
	// because that API does not support it (historical limitation).
	//
	// We can also only annotate the project *after* the SSAR check above, which
	// means that if SSAR fails, the project will *not* be annotated for cleanup.
	annotationUpdates := map[string]string{}
	if o.idleCleanupDuration > 0 {
		if o.idleCleanupDurationSet {
			logrus.Debugf("Setting a soft TTL of %s for the namespace", o.idleCleanupDuration.String())
		}
		annotationUpdates[nsttl.AnnotationIdleCleanupDurationTTL] = o.idleCleanupDuration.String()
	}

	if o.cleanupDuration > 0 {
		if o.cleanupDurationSet {
			logrus.Debugf("Setting a hard TTL of %s for the namespace", o.cleanupDuration.String())
		}
		annotationUpdates[nsttl.AnnotationCleanupDurationTTL] = o.cleanupDuration.String()
	}

	// This label makes sure that the namespace is active, and the value will be updated
	// if the namespace will be reused.
	annotationUpdates[nsttl.AnnotationNamespaceLastActive] = time.Now().Format(time.RFC3339)

	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		ns := &coreapi.Namespace{}
		if err := client.Get(ctx, ctrlruntimeclient.ObjectKey{Name: o.namespace}, ns); err != nil {
			return err
		}
		ns.Labels = steps.LabelsFor(o.jobSpec, map[string]string{api.AutoScalePodsLabel: "true"}, "")

		if ns.Annotations == nil {
			ns.Annotations = make(map[string]string)
		}
		for key, value := range annotationUpdates {
			// allow specific annotations to be skipped if they are already set and the user didn't ask
			switch key {
			case nsttl.AnnotationCleanupDurationTTL:
				if !o.cleanupDurationSet && len(ns.Annotations[key]) != 0 {
					continue
				}
			case nsttl.AnnotationIdleCleanupDurationTTL:
				if !o.idleCleanupDurationSet && len(ns.Annotations[key]) != 0 {
					continue
				}
			}
			ns.ObjectMeta.Annotations[key] = value
		}

		updateErr := client.Update(ctx, ns)
		if kerrors.IsForbidden(updateErr) {
			logrus.WithError(err).Warn("Could not edit namespace because you do not have permission to update the namespace.")
			return nil
		}
		return updateErr
	}); err != nil {
		return fmt.Errorf("could not update namespace to add labels, TTLs and active annotations: %w", err)
	}

	intranetAccess := false
	for _, test := range o.configSpec.Tests {
		if slices.Contains(o.targets.values, test.As) {
			if test.RestrictNetworkAccess != nil && !*test.RestrictNetworkAccess {
				intranetAccess = true
				break
			}
		}
	}

	if intranetAccess {
		logrus.Debugf("Deleting egress firewall in namespace %s", o.namespace)
		egressFirewall := &egressfirewallv1.EgressFirewall{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "default",
				Namespace: o.namespace,
			},
		}
		if err := client.Delete(ctx, egressFirewall); err != nil {
			if kerrors.IsNotFound(err) {
				logrus.Warnf("Warning: egress firewall does not exist: %v", err)
			} else {
				if errors.Is(err, &meta.NoKindMatchError{}) {
					logrus.Warnf("crd not installed: %s", err)
				} else {
					return fmt.Errorf("could not delete egress firewall: %w", err)
				}
			}
		}
	}

	pullStart := time.Now()
	var imagePullSecretsMinted bool
	for i := 0; i < 299; i++ {
		imagePullSecretsMinted = true
		serviceAccounts := map[string]*coreapi.ServiceAccount{
			"builder": {},
			"default": {},
		}
		for name := range serviceAccounts {
			if err := client.Get(ctx, ctrlruntimeclient.ObjectKey{Namespace: o.namespace, Name: name}, serviceAccounts[name]); err != nil && !kerrors.IsNotFound(err) {
				return fmt.Errorf("failed to fetch service account %s: %w", name, err)
			}
			imagePullSecretsMinted = imagePullSecretsMinted && len(serviceAccounts[name].ImagePullSecrets) > 0
		}
		if imagePullSecretsMinted {
			break
		}
		logrus.Debugf("[%d/300] Image pull secrets in namespace not yet ready, sleeping for a second...", i)
		time.Sleep(time.Second)
	}
	logrus.Debugf("Spent %v waiting for image pull secrets to initialize in the new namespace.", time.Since(pullStart))
	if !imagePullSecretsMinted {
		logrus.Error("Timed out waiting for image pull secrets in the test namespace.")
		return errors.New("timed out waiting for image pull secrets")
	}

	if o.givePrAuthorAccessToNamespace && len(o.authors) > 0 {
		roleBinding := generateAuthorAccessRoleBinding(o.namespace, o.authors)
		// Generate rolebinding for all the PR Authors.
		logrus.WithField("authors", o.authors).Debugf("Creating ci-op-author-access rolebinding in namespace %s", o.namespace)
		if err := client.Create(ctx, roleBinding); err != nil && !kerrors.IsAlreadyExists(err) {
			return fmt.Errorf("could not create role binding for: %w", err)
		}

	}

	for _, secret := range []*coreapi.Secret{o.pullSecret, o.pushSecret, o.uploadSecret} {
		if secret != nil {
			secret.Immutable = utilpointer.Bool(true)
			if err := client.Create(ctx, secret); err != nil && !kerrors.IsAlreadyExists(err) {
				return fmt.Errorf("couldn't create secret %s: %w", secret.Name, err)
			}
		}
	}

	go func() {
		ticker := time.NewTicker(10 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				ns := &coreapi.Namespace{}
				if err := client.Get(ctx, ctrlruntimeclient.ObjectKey{Name: o.namespace}, ns); err != nil {
					logrus.WithError(err).Warnf("Failed to get namespace %s for heartbeating", o.namespace)
					continue
				}
				originalNS := ns.DeepCopy()
				if ns.Annotations == nil {
					ns.Annotations = map[string]string{}
				}
				ns.Annotations[nsttl.AnnotationNamespaceLastActive] = time.Now().Format(time.RFC3339)
				if err := client.Patch(ctx, ns, ctrlruntimeclient.MergeFrom(originalNS)); err != nil {
					logrus.WithError(err).Warnf("Failed to patch the %s namespace to update the %s annotation.", o.namespace, nsttl.AnnotationNamespaceLastActive)
				}
			}
		}
	}()

	logrus.Debugf("Setting up pipeline ImageStream for the test")

	// create the image stream or read it to get its uid
	is := &imageapi.ImageStream{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: o.jobSpec.Namespace(),
			Name:      api.PipelineImageStream,
		},
		Spec: imageapi.ImageStreamSpec{
			// pipeline:* will now be directly referenceable
			LookupPolicy: imageapi.ImageLookupPolicy{Local: true},
		},
	}
	is, err = util.CreateImageStreamWithMetrics(ctx, client, is, o.metricsAgent)
	if err != nil {
		return fmt.Errorf("could not set up pipeline imagestream for test: %w", err)
	}
	o.jobSpec.SetOwner(&metav1.OwnerReference{
		APIVersion: "image.openshift.io/v1",
		Kind:       "ImageStream",
		Name:       api.PipelineImageStream,
		UID:        is.UID,
	})

	if o.cloneAuthConfig != nil && o.cloneAuthConfig.Secret != nil {
		o.cloneAuthConfig.Secret.Immutable = utilpointer.Bool(true)
		if err := client.Create(ctx, o.cloneAuthConfig.Secret); err != nil && !kerrors.IsAlreadyExists(err) {
			return fmt.Errorf("couldn't create secret %s for %s authentication: %w", o.cloneAuthConfig.Secret.Name, o.cloneAuthConfig.Type, err)
		}
	}

	if o.configSpec.ExternalImages != nil {
		for _, image := range o.configSpec.ExternalImages {
			if image.PullSecret != "" {
				secret, err := getExternalImagePullSecret(ctx, labeledclient.Wrap(ctrlClient, o.jobSpec), image)
				if err != nil {
					return fmt.Errorf("failed to get external image pull secret: %w", err)
				}
				secret.Namespace = o.namespace
				o.secrets = append(o.secrets, secret)
			}
		}
	}

	// If promotion is enabled and there are promotion targets configured, copy the promotion kubeconfig secret
	if o.promote && o.configSpec.PromotionConfiguration != nil && len(api.PromotionTargets(o.configSpec.PromotionConfiguration)) > 0 {
		promotionSecret, err := getPromotionKubeconfigSecret(ctx, labeledclient.Wrap(ctrlClient, o.jobSpec))
		if err != nil {
			return fmt.Errorf("failed to get promotion kubeconfig secret: %w", err)
		}
		promotionSecret.Namespace = o.namespace
		o.secrets = append(o.secrets, promotionSecret)
		logrus.Debugf("Added promotion kubeconfig secret %s to be created in namespace %s", api.PromotionQuayTaggerKubeconfigSecret, o.namespace)
	}

	for _, secret := range o.secrets {
		created, err := util.UpsertImmutableSecret(ctx, client, secret)
		if err != nil {
			return fmt.Errorf("could not update secret %s: %w", secret.Name, err)
		}
		if created {
			logrus.Debugf("Created secret %s", secret.Name)
		} else {
			logrus.Debugf("Updated secret %s", secret.Name)
		}
	}
	pdb, mutateFn := pdb(steps.CreatedByCILabel, o.namespace)
	if _, err := crcontrollerutil.CreateOrUpdate(ctx, client, pdb, mutateFn); err != nil && !kerrors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to create pdb for label key %s: %w", steps.CreatedByCILabel, err)
	}
	logrus.Debugf("Created PDB for pods with %s label", steps.CreatedByCILabel)

	o.metricsAgent.Record(metrics.NewInsightsEvent(metrics.InsightNamespaceInitialized, metrics.Context{"namespace": o.namespace, "duration_seconds": time.Since(initBeginning).Seconds()}))
	return nil
}

func getExternalImagePullSecret(ctx context.Context, client ctrlruntimeclient.Client, image api.ExternalImage) (*coreapi.Secret, error) {
	ciSecret := &coreapi.Secret{}
	err := client.Get(ctx, ctrlruntimeclient.ObjectKey{Namespace: "test-credentials", Name: image.PullSecret}, ciSecret)
	if err != nil {
		return nil, fmt.Errorf("failed to get secret '%s' from test-credentials namespace: %w", image.PullSecret, err)
	}

	newSecret := &coreapi.Secret{
		Data: ciSecret.Data,
		Type: coreapi.SecretTypeDockerConfigJson,
		ObjectMeta: metav1.ObjectMeta{
			Name: "external-pull-secret-" + image.PullSecret,
		},
	}
	return newSecret, nil
}

// getPromotionKubeconfigSecret fetches the promotion-quay-tagger-kubeconfig secret from the ci namespace
// and returns a new secret to be created in the test namespace
func getPromotionKubeconfigSecret(ctx context.Context, client ctrlruntimeclient.Client) (*coreapi.Secret, error) {
	ciSecret := &coreapi.Secret{}
	err := client.Get(ctx, ctrlruntimeclient.ObjectKey{Namespace: "ci", Name: api.PromotionQuayTaggerKubeconfigSecret}, ciSecret)
	if err != nil {
		return nil, fmt.Errorf("failed to get secret '%s' from ci namespace: %w", api.PromotionQuayTaggerKubeconfigSecret, err)
	}

	newSecret := &coreapi.Secret{
		Data: ciSecret.Data,
		Type: ciSecret.Type,
		ObjectMeta: metav1.ObjectMeta{
			Name: api.PromotionQuayTaggerKubeconfigSecret,
		},
	}
	return newSecret, nil
}

func generateAuthorAccessRoleBinding(namespace string, authors []string) *rbacapi.RoleBinding {
	var subjects []rbacapi.Subject
	authorSet := sets.New[string](authors...)
	for _, author := range sets.List(authorSet) {
		subjects = append(subjects, rbacapi.Subject{Kind: "Group", Name: api.GitHubUserGroup(author)})
	}
	return &rbacapi.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ci-op-author-access",
			Namespace: namespace,
		},
		Subjects: subjects,
		RoleRef: rbacapi.RoleRef{
			Kind: "ClusterRole",
			Name: "admin",
		},
	}
}

func pdb(labelKey, namespace string) (*policyv1.PodDisruptionBudget, crcontrollerutil.MutateFn) {
	pdb := &policyv1.PodDisruptionBudget{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("ci-operator-%s", strings.ReplaceAll(labelKey, "/", "-")),
			Namespace: namespace,
		},
	}
	return pdb, func() error {
		pdb.Spec.MaxUnavailable = &intstr.IntOrString{
			Type:   intstr.Int,
			IntVal: 0,
		}
		pdb.Spec.Selector = &metav1.LabelSelector{
			MatchExpressions: []metav1.LabelSelectorRequirement{{
				Key:      labelKey,
				Operator: metav1.LabelSelectorOpExists,
			}},
		}
		pdb.Spec.UnhealthyPodEvictionPolicy = ptr.To(policyv1.AlwaysAllow)
		return nil
	}
}

func monitorNamespace(ctx context.Context, cancel func(), namespace string, client coreclientset.NamespaceInterface) {
reset:
	for {
		watcher, err := client.Watch(context.Background(), metav1.ListOptions{
			TypeMeta:      metav1.TypeMeta{},
			FieldSelector: fields.Set{"metadata.name": namespace}.AsSelector().String(),
			Watch:         true,
		})
		if err != nil {
			logrus.WithError(err).Warn("Could not start a watch on our test namespace.")
			cancel()
			return
		}
		for {
			select {
			case <-ctx.Done():
				// we're done operating anyway
				return
			case event, ok := <-watcher.ResultChan():
				if !ok {
					continue reset
				}
				ns, ok := event.Object.(*coreapi.Namespace)
				if !ok {
					continue
				}
				if ns.Name != namespace {
					continue
				}
				if ns.DeletionTimestamp != nil {
					logrus.Info("The namespace in which this test is executing has been deleted, cancelling the test...")
					cancel()
					return
				}
			}
		}
	}
}

// saveNamespaceArtifacts is a best effort attempt to save ci-operator namespace artifacts to disk
// for review later.
func (o *options) saveNamespaceArtifacts() {
	namespaceDir := api.NamespaceDir
	if kubeClient, err := coreclientset.NewForConfig(o.clusterConfig); err == nil {
		pods, _ := kubeClient.Pods(o.namespace).List(context.TODO(), metav1.ListOptions{})
		data, _ := json.MarshalIndent(pods, "", "  ")
		path := filepath.Join(namespaceDir, "pods.json")
		_ = api.SaveArtifact(o.censor, path, data)
		events, _ := kubeClient.Events(o.namespace).List(context.TODO(), metav1.ListOptions{})
		data, _ = json.MarshalIndent(events, "", "  ")
		path = filepath.Join(namespaceDir, "events.json")
		_ = api.SaveArtifact(o.censor, path, data)
	}

	if buildClient, err := buildclientset.NewForConfig(o.clusterConfig); err == nil {
		builds, _ := buildClient.Builds(o.namespace).List(context.TODO(), metav1.ListOptions{})
		data, _ := json.MarshalIndent(builds, "", "  ")
		path := filepath.Join(namespaceDir, "builds.json")
		_ = api.SaveArtifact(o.censor, path, data)
	}

	if client, err := ctrlruntimeclient.New(o.clusterConfig, ctrlruntimeclient.Options{}); err == nil {
		imagestreams := &imageapi.ImageStreamList{}
		_ = client.List(context.TODO(), imagestreams, ctrlruntimeclient.InNamespace(o.namespace))
		data, _ := json.MarshalIndent(imagestreams, "", "  ")
		path := filepath.Join(namespaceDir, "imagestreams.json")
		_ = api.SaveArtifact(o.censor, path, data)
	}

	o.metricsAgent.Record(metrics.NewInsightsEvent(metrics.InsightNamespaceArtifacts, metrics.Context{"namespace": o.namespace}))
}
