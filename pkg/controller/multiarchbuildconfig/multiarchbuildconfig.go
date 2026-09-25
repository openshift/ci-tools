package multiarchbuildconfig

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/sirupsen/logrus"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/selection"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	ctrlruntime "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	ctrlruntimeutil "sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	buildv1 "github.com/openshift/api/build/v1"

	v1 "github.com/openshift/ci-tools/pkg/api/multiarchbuildconfig/v1"
	controllerutil "github.com/openshift/ci-tools/pkg/controller/util"
	"github.com/openshift/ci-tools/pkg/manifestpusher"
)

const (
	controllerName        = "multiarchbuildconfig"
	nodeArchitectureLabel = "kubernetes.io/arch"

	registryURL = "image-registry.openshift-image-registry.svc:5000"

	// Conditions
	CreateBuildsDone           = "CreateBuildsDone"
	CreateBuildsSuccessReason  = "CreateBuildsSuccess"
	CreateBuildsSuccessMessage = "Builds exist for all configured architectures"
	CreateBuildsErrorReason    = "CreateBuildsError"

	BuildsCompleted               = "BuildsCompleted"
	BuildsCompletedSuccessReason  = "BuildsCompletedSuccess"
	BuildsCompletedSuccessMessage = "All builds finished successfully"
	WaitingForBuildsReason        = "WaitingForBuilds"
	WaitingForBuildsMessage       = "Waiting for builds to finish"
	BuildsCompletedErrorReason    = "BuildsCompletedError"

	PushImageManifestDone      = "PushManifestDone"
	PushManifestSuccessReason  = "PushManifestSuccess"
	PushManifestSuccessMessage = "Multi-arch manifest list was pushed successfully"
	PushManifestErrorReason    = "PushManifestError"

	MirrorImageManifestDone       = "ImageMirrorDone"
	ImageMirrorSuccessReason      = "ImageMirrorSuccess"
	ImageMirrorSuccessMessage     = "Image mirrored to external registries"
	ImageMirrorRunningMessage     = "Mirroring image to external registries"
	ImageMirrorErrorReason        = "ImageMirrorError"
	ImageMirrorSkipedReason       = "ImageMirrorSkipped"
	ImageMirrorNoExtRegistriesMsg = "No external registries configured"

	MABCNameLogField          = "multiarchbuildconfig_name"
	PushTargetImageLogField   = "target_image"
	MirrorTargetImageLogField = "target_image"
	MirrorRegistriesLogField  = "registries"
	BuildNameLogField         = "build_name"
	BuildNamespaceLogField    = "build_namespace"
)

func AddToManager(mgr manager.Manager, architectures []string, dockerCfgPath string) error {
	logger := logrus.WithField("controller", controllerName)

	mabcPredicateFuncs := predicate.Funcs{
		CreateFunc:  func(e event.CreateEvent) bool { return true },
		DeleteFunc:  func(event.DeleteEvent) bool { return false },
		UpdateFunc:  func(event.UpdateEvent) bool { return true },
		GenericFunc: func(event.GenericEvent) bool { return false },
	}

	buildPredicateFuncs := predicate.Funcs{
		CreateFunc:  func(e event.CreateEvent) bool { return true },
		DeleteFunc:  func(event.DeleteEvent) bool { return false },
		UpdateFunc:  func(e event.UpdateEvent) bool { return true },
		GenericFunc: func(event.GenericEvent) bool { return false },
	}

	if err := ctrlruntime.NewControllerManagedBy(mgr).
		WithOptions(controller.Options{MaxConcurrentReconciles: 1}).
		For(&v1.MultiArchBuildConfig{}, builder.WithPredicates(mabcPredicateFuncs)).
		Owns(&buildv1.Build{}, builder.WithPredicates(buildPredicateFuncs)).
		Complete(&reconciler{
			logger:         logger,
			client:         mgr.GetClient(),
			architectures:  architectures,
			manifestPusher: manifestpusher.NewManifestPusher(logger, registryURL, dockerCfgPath, mgr.GetClient()),
			imageMirrorer:  &ocImage{log: logger, registryConfig: dockerCfgPath},
			scheme:         mgr.GetScheme(),
		}); err != nil {
		return fmt.Errorf("failed to create controller: %w", err)
	}

	return nil
}

type reconciler struct {
	logger         *logrus.Entry
	client         ctrlruntimeclient.Client
	architectures  []string
	manifestPusher manifestpusher.ManifestPusher
	imageMirrorer  imageMirrorer
	scheme         *runtime.Scheme
}

func (r *reconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	logger := r.logger.WithField("request", req.String())
	err := r.reconcile(ctx, logger, req)
	if err != nil {
		logger.WithError(err).Error("Reconciliation failed")
	} else {
		logger.Info("Finished reconciliation")
	}
	return reconcile.Result{}, controllerutil.SwallowIfTerminal(err)
}

func (r *reconciler) reconcile(ctx context.Context, logger *logrus.Entry, req reconcile.Request) error {
	logger = logger.WithField(MABCNameLogField, req.Name)
	logger.Info("Starting reconciliation")

	mabc := &v1.MultiArchBuildConfig{}
	if err := r.client.Get(ctx, req.NamespacedName, mabc); err != nil {
		return fmt.Errorf("failed to get the MultiArchBuildConfig: %w", err)
	}

	mabc = mabc.DeepCopy()

	// Deletion is being processed, do nothing
	if mabc.ObjectMeta.DeletionTimestamp != nil {
		logger.Info("Ongoing deletion, skip")
		return nil
	}

	if mabc.Status.State == v1.SuccessState || mabc.Status.State == v1.FailureState {
		logger.Infof("State %q, skip", mabc.Status.State)
		return nil
	}

	observedStatus := &v1.MultiArchBuildConfigStatus{}
	if err := r.handleMultiArchBuildConfig(ctx, logger, mabc, observedStatus); err != nil {
		if updateErr := r.updateStatus(ctx, mabc, observedStatus); updateErr != nil {
			msg := utilerrors.NewAggregate([]error{updateErr, err}).Error()
			return errors.New(msg)
		}
		return err
	}

	return r.updateStatus(ctx, mabc, observedStatus)
}

func (r *reconciler) handleMultiArchBuildConfig(ctx context.Context, logger *logrus.Entry, mabc *v1.MultiArchBuildConfig, observedStatus *v1.MultiArchBuildConfigStatus) error {
	if mabc.Spec.BuildSpec.CommonSpec.Output.To.Kind == "DockerImage" && len(mabc.Spec.ExternalRegistries) > 0 {
		return fmt.Errorf("external_registries cannot be used with DockerImage output")
	}
	builds, err := r.listBuilds(ctx, mabc.Name)
	if err != nil {
		return fmt.Errorf("couldn't list builds: %w", err)
	}

	upsertCondition(observedStatus, &metav1.Condition{
		Type:               CreateBuildsDone,
		Status:             metav1.ConditionTrue,
		LastTransitionTime: metav1.Now(),
		Reason:             CreateBuildsSuccessReason,
		Message:            CreateBuildsSuccessMessage,
	})
	if len(r.architectures) != len(builds.Items) {
		if err = r.createBuilds(ctx, logger, mabc); err != nil {
			upsertCondition(observedStatus, &metav1.Condition{
				Type:               CreateBuildsDone,
				Status:             metav1.ConditionFalse,
				LastTransitionTime: metav1.Now(),
				Reason:             CreateBuildsErrorReason,
				Message:            err.Error(),
			})
			return fmt.Errorf("couldn't create builds for architectures: %s: %w", strings.Join(r.architectures, ","), err)
		}
		return nil
	}

	upsertCondition(observedStatus, &metav1.Condition{
		Type:               BuildsCompleted,
		Status:             metav1.ConditionFalse,
		LastTransitionTime: metav1.Now(),
		Reason:             WaitingForBuildsReason,
		Message:            WaitingForBuildsMessage,
	})

	if !checkAllBuildsFinished(builds) {
		logger.Info("Waiting for the builds to complete")
		return nil
	}

	if !checkAllBuildsSuccessful(logger, builds) {
		upsertCondition(observedStatus, &metav1.Condition{
			Type:               BuildsCompleted,
			Status:             metav1.ConditionFalse,
			LastTransitionTime: metav1.Now(),
			Reason:             BuildsCompletedErrorReason,
			Message:            "Some builds have failed",
		})
		observedStatus.State = v1.FailureState
		return nil
	}

	upsertCondition(observedStatus, &metav1.Condition{
		Type:               BuildsCompleted,
		Status:             metav1.ConditionTrue,
		LastTransitionTime: metav1.Now(),
		Reason:             BuildsCompletedSuccessReason,
		Message:            BuildsCompletedSuccessMessage,
	})

	imageRef := targetImageRef(mabc)
	if !isPushImageManifestDone(mabc) {
		r.handlePushImageWithManifest(logger, imageRef, builds, observedStatus)
		return nil
	}

	upsertCondition(observedStatus, getConditionByType(mabc, PushImageManifestDone))

	if !isImageMirrorDone(mabc) {
		if err := r.handleMirrorImage(logger, imageRef, mabc, observedStatus); err != nil {
			return fmt.Errorf("mirror images: %w", err)
		}
	} else if mirrorDoneCond := getConditionByType(mabc, MirrorImageManifestDone); mirrorDoneCond != nil {
		upsertCondition(observedStatus, getConditionByType(mabc, MirrorImageManifestDone))
	}

	observedStatus.State = v1.SuccessState
	return nil
}

func (r *reconciler) createBuilds(ctx context.Context, logger *logrus.Entry, mabc *v1.MultiArchBuildConfig) error {
	for _, arch := range r.architectures {
		commonSpec := mabc.Spec.BuildSpec.CommonSpec.DeepCopy()
		commonSpec.NodeSelector = map[string]string{nodeArchitectureLabel: arch}
		if commonSpec.Output.To.Kind != "DockerImage" && commonSpec.Output.To.Namespace == "" {
			commonSpec.Output.To.Namespace = mabc.Namespace
		}
		commonSpec.Output.To.Name = fmt.Sprintf("%s-%s", commonSpec.Output.To.Name, arch)

		build := &buildv1.Build{
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf("%s-%s", mabc.Name, arch),
				Namespace: mabc.Namespace,
				Labels: map[string]string{
					v1.MultiArchBuildConfigNameLabel: mabc.Name,
					v1.MultiArchBuildConfigArchLabel: arch,
				},
			},
			Spec: buildv1.BuildSpec{
				CommonSpec: *commonSpec,
			},
		}

		logger = logger.WithField(BuildNamespaceLogField, build.Namespace).WithField(BuildNameLogField, build.Name)
		logger.Info("Creating build")

		if err := ctrlruntimeutil.SetControllerReference(mabc, build, r.scheme); err != nil {
			return fmt.Errorf("couldn't set controller reference %w", err)
		}

		if err := r.client.Create(ctx, build); err != nil {
			return fmt.Errorf("couldn't create build %s/%s: %w", build.Namespace, build.Name, err)
		}
	}
	return nil
}

func targetImageRef(mabc *v1.MultiArchBuildConfig) string {
	output := mabc.Spec.BuildSpec.CommonSpec.Output.To
	if output.Kind == "DockerImage" {
		return output.Name
	}
	targetNamespace := output.Namespace
	if targetNamespace == "" {
		targetNamespace = mabc.Namespace
	}
	return fmt.Sprintf("%s/%s", targetNamespace, output.Name)
}

func (r *reconciler) handlePushImageWithManifest(logger *logrus.Entry, targetImageRef string, builds *buildv1.BuildList, observedStatus *v1.MultiArchBuildConfigStatus) {
	logger = logger.WithField(PushTargetImageLogField, targetImageRef)

	logger.Info("Pushing manifest")
	upsertCondition(observedStatus, &metav1.Condition{
		Type:               PushImageManifestDone,
		Status:             metav1.ConditionTrue,
		LastTransitionTime: metav1.Now(),
		Reason:             PushManifestSuccessReason,
		Message:            PushManifestSuccessMessage,
	})

	if err := r.manifestPusher.PushImageWithManifest(builds.Items, targetImageRef); err != nil {
		logger.Errorf("Failed to push manifest: %s", err)
		upsertCondition(observedStatus, &metav1.Condition{
			Type:               PushImageManifestDone,
			Status:             metav1.ConditionFalse,
			LastTransitionTime: metav1.Now(),
			Reason:             PushManifestErrorReason,
			Message:            err.Error(),
		})
		observedStatus.State = v1.FailureState
	} else {
		logger.Info("Manifest pushed")
	}
}

// handleMirrorImage pushes an image to the locations specified in .spec.external_registries. The image
// required has to exist on local registry.
func (r *reconciler) handleMirrorImage(logger *logrus.Entry, targetImageRef string, mabc *v1.MultiArchBuildConfig, observedStatus *v1.MultiArchBuildConfigStatus) error {
	logger = logger.WithField(MirrorTargetImageLogField, targetImageRef)

	if len(mabc.Spec.ExternalRegistries) == 0 {
		logger.Info("No registries set, skip mirroring")
		upsertCondition(observedStatus, &metav1.Condition{
			Type:               MirrorImageManifestDone,
			Status:             metav1.ConditionTrue,
			LastTransitionTime: metav1.Now(),
			Reason:             ImageMirrorSkipedReason,
			Message:            ImageMirrorNoExtRegistriesMsg,
		})
		return nil
	}

	upsertCondition(observedStatus, &metav1.Condition{
		Type:               MirrorImageManifestDone,
		Status:             metav1.ConditionTrue,
		LastTransitionTime: metav1.Now(),
		Reason:             ImageMirrorSuccessReason,
		Message:            ImageMirrorRunningMessage,
	})

	logger = logger.WithField(MirrorRegistriesLogField, strings.Join(mabc.Spec.ExternalRegistries, ","))
	logger.Info("Mirroring image")

	imageMirrorArgs := ocImageMirrorArgs(targetImageRef, mabc.Spec.ExternalRegistries)
	if err := r.imageMirrorer.mirror(imageMirrorArgs); err != nil {
		logger.Errorf("Failed to mirror image: %s", err)
		upsertCondition(observedStatus, &metav1.Condition{
			Type:               MirrorImageManifestDone,
			Status:             metav1.ConditionFalse,
			LastTransitionTime: metav1.Now(),
			Reason:             ImageMirrorErrorReason,
			Message:            fmt.Sprintf("oc image mirror: %s", err),
		})
		observedStatus.State = v1.FailureState
		return err
	}

	logger.Info("Image mirrored")
	upsertCondition(observedStatus, &metav1.Condition{
		Type:               MirrorImageManifestDone,
		Status:             metav1.ConditionTrue,
		LastTransitionTime: metav1.Now(),
		Reason:             ImageMirrorSuccessReason,
		Message:            ImageMirrorSuccessMessage,
	})

	return nil
}

func (r *reconciler) listBuilds(ctx context.Context, mabcName string) (*buildv1.BuildList, error) {
	builds := buildv1.BuildList{}
	requirement, err := labels.NewRequirement(v1.MultiArchBuildConfigNameLabel, selection.Equals, []string{mabcName})
	if err != nil {
		return nil, fmt.Errorf("failed to create requirement: %w", err)
	}
	listOpts := ctrlruntimeclient.ListOptions{LabelSelector: labels.NewSelector().Add(*requirement)}
	if err := r.client.List(ctx, &builds, &listOpts); err != nil {
		return nil, fmt.Errorf("failed to list builds: %w", err)
	}
	return &builds, nil
}

func checkAllBuildsSuccessful(logger *logrus.Entry, builds *buildv1.BuildList) bool {
	for _, build := range builds.Items {
		if build.Status.Phase != buildv1.BuildPhaseComplete {
			logger.Warnf("Build %s didn't complete successfully", build.Name)
			return false
		}
	}
	return true
}

func checkAllBuildsFinished(builds *buildv1.BuildList) bool {
	for _, build := range builds.Items {
		if build.Status.Phase != buildv1.BuildPhaseComplete &&
			build.Status.Phase != buildv1.BuildPhaseFailed &&
			build.Status.Phase != buildv1.BuildPhaseCancelled &&
			build.Status.Phase != buildv1.BuildPhaseError {
			return false
		}
	}
	return true
}

func isPushImageManifestDone(mabc *v1.MultiArchBuildConfig) bool {
	return getCondition(mabc, PushImageManifestDone, PushManifestSuccessReason, metav1.ConditionTrue) != nil
}

func isImageMirrorDone(mabc *v1.MultiArchBuildConfig) bool {
	return getCondition(mabc, MirrorImageManifestDone, ImageMirrorSuccessReason, metav1.ConditionTrue) != nil
}

func getCondition(mabc *v1.MultiArchBuildConfig, condType, reason string, status metav1.ConditionStatus) *metav1.Condition {
	for i := range mabc.Status.Conditions {
		c := &mabc.Status.Conditions[i]
		if c.Type == condType && c.Reason == reason && c.Status == status {
			return c
		}
	}
	return nil
}

func getConditionByType(mabc *v1.MultiArchBuildConfig, condType string) *metav1.Condition {
	for i := range mabc.Status.Conditions {
		c := &mabc.Status.Conditions[i]
		if c.Type == condType {
			return c
		}
	}
	return nil
}

func upsertCondition(status *v1.MultiArchBuildConfigStatus, newCond *metav1.Condition) {
	conditionsEqual := func(a, b *metav1.Condition) bool {
		return a.Message == b.Message && a.Reason == b.Reason &&
			a.Status == b.Status && a.Type == b.Type
	}

	for i := range status.Conditions {
		cond := &status.Conditions[i]
		if cond.Type == newCond.Type {
			if conditionsEqual(cond, newCond) {
				return
			}
			status.Conditions[i] = *newCond
			return
		}
	}

	status.Conditions = append(status.Conditions, *newCond)
}

func (r *reconciler) updateStatus(ctx context.Context, mabc *v1.MultiArchBuildConfig, observedStatus *v1.MultiArchBuildConfigStatus) error {
	if !cmp.Equal(&mabc.Status, observedStatus, cmpopts.IgnoreFields(metav1.Condition{}, "LastTransitionTime")) {
		mabc.Status = *observedStatus
		return r.updateMABC(ctx, mabc)
	}
	return nil
}

func (r *reconciler) updateMABC(ctx context.Context, mabc *v1.MultiArchBuildConfig) error {
	if err := r.client.Update(ctx, mabc); err != nil {
		return fmt.Errorf("update mabc: %w", err)
	}
	return nil
}
