package multiarchbuildconfig

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/sirupsen/logrus"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	buildv1 "github.com/openshift/api/build/v1"

	v1 "github.com/openshift/ci-tools/pkg/api/multiarchbuildconfig/v1"
	"github.com/openshift/ci-tools/pkg/controller/multiarchbuildconfig/buildsreconciler"
	controllerutil "github.com/openshift/ci-tools/pkg/controller/util"
	"github.com/openshift/ci-tools/pkg/manifestpusher"
)

const (
	controllerName        = "multiarchbuildconfig"
	nodeArchitectureLabel = "kubernetes.io/arch"

	registryURL = "image-registry.openshift-image-registry.svc:5000"

	// Conditions
	PushImageManifestDone     = "PushManifestDone"
	PushManifestSuccessReason = "PushManifestSuccess"
	PushManifestErrorReason   = "PushManifestError"

	MirrorImageManifestDone  = "ImageMirrorDone"
	ImageMirrorSuccessReason = "ImageMirrorSuccess"
	ImageMirrorErrorReason   = "ImageMirrorError"
)

func AddToManager(mgr manager.Manager, architectures []string, dockerCfgPath string) error {
	if err := buildsreconciler.AddToManager(mgr); err != nil {
		return fmt.Errorf("failed to construct builds reconciler: %w", err)
	}

	logger := logrus.WithField("controller", controllerName)
	c, err := controller.New(controllerName, mgr, controller.Options{
		MaxConcurrentReconciles: 1,
		Reconciler: &reconciler{
			logger:         logger,
			client:         mgr.GetClient(),
			architectures:  architectures,
			manifestPusher: manifestpusher.NewManifestPushfer(logger, registryURL, dockerCfgPath),
			imageMirrorer:  &ocImage{log: logger, registryConfig: dockerCfgPath},
		},
	})
	if err != nil {
		return fmt.Errorf("failed to construct controller: %w", err)
	}

	predicateFuncs := predicate.Funcs{
		CreateFunc:  func(e event.CreateEvent) bool { return true },
		DeleteFunc:  func(event.DeleteEvent) bool { return false },
		UpdateFunc:  func(event.UpdateEvent) bool { return true },
		GenericFunc: func(event.GenericEvent) bool { return false },
	}

	if err := c.Watch(source.Kind(mgr.GetCache(), &v1.MultiArchBuildConfig{}),
		&handler.EnqueueRequestForObject{}, predicateFuncs); err != nil {
		return fmt.Errorf("failed to create watch for MultiArchBuildConfig: %w", err)
	}

	return nil
}

type reconciler struct {
	logger         *logrus.Entry
	client         ctrlruntimeclient.Client
	architectures  []string
	manifestPusher manifestpusher.ManifestPusher
	imageMirrorer  imageMirrorer
}

func (r *reconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	logger := r.logger.WithField("request", req.String())
	err := r.reconcile(ctx, req, logger)
	if err != nil {
		logger.WithError(err).Error("Reconciliation failed")
	} else {
		logger.Info("Finished reconciliation")
	}
	return reconcile.Result{}, controllerutil.SwallowIfTerminal(err)
}

func (r *reconciler) reconcile(ctx context.Context, req reconcile.Request, logger *logrus.Entry) error {
	logger = logger.WithField("multiarchbuildconfig_name", req.Name)
	logger.Info("Starting reconciliation")

	mabc := &v1.MultiArchBuildConfig{}
	if err := r.client.Get(ctx, req.NamespacedName, mabc); err != nil {
		return fmt.Errorf("failed to get the MultiArchBuildConfig: %w", err)
	}

	if mabc.Status.State == v1.SuccessState || mabc.Status.State == v1.FailureState {
		return nil
	}

	if err := r.handleMultiArchBuildConfig(ctx, mabc); err != nil {
		return err
	}

	return nil
}

func (r *reconciler) handleMultiArchBuildConfig(ctx context.Context, mabc *v1.MultiArchBuildConfig) error {
	if mabc.Status.Builds == nil {
		if err := r.createBuildsForArchitectures(ctx, mabc); err != nil {
			return fmt.Errorf("couldn't create builds for architectures: %s: %w", strings.Join(r.architectures, ","), err)
		}
		return nil
	}

	if !checkAllBuildsFinished(mabc.Status.Builds) {
		return nil
	}

	if !checkAllBuildsSuccessful(mabc.Status.Builds) {
		mutateFn := func(mabcToMutate *v1.MultiArchBuildConfig) { mabcToMutate.Status.State = v1.FailureState }
		if err := v1.UpdateMultiArchBuildConfig(ctx, r.logger, r.client, ctrlruntimeclient.ObjectKey{Namespace: mabc.Namespace, Name: mabc.Name}, mutateFn); err != nil {
			return fmt.Errorf("failed to update the MultiArchBuildConfig %s/%s: %w", mabc.Namespace, mabc.Name, err)
		}
		return nil
	}

	targetImageRef := fmt.Sprintf("%s/%s", mabc.Spec.BuildSpec.CommonSpec.Output.To.Namespace, mabc.Spec.BuildSpec.CommonSpec.Output.To.Name)
	// First condition to be added is PushImageManifestDone
	if len(mabc.Status.Conditions) == 0 {
		if err := r.handlePushImageWithManifest(ctx, mabc, targetImageRef); err != nil {
			return fmt.Errorf("couldn't push the manifest: %w", err)
		}
		return nil
	}

	if isPushImageManifestDone(mabc) {
		srcImage := fmt.Sprintf("%s/%s", registryURL, targetImageRef)
		if err := r.handleMirrorImage(ctx, srcImage, mabc); err != nil {
			return fmt.Errorf("couldn't mirror the image: %w", err)
		}
	}

	return nil
}

func (r *reconciler) createBuildsForArchitectures(ctx context.Context, mabc *v1.MultiArchBuildConfig) error {
	for _, arch := range r.architectures {
		commonSpec := mabc.Spec.BuildSpec.CommonSpec.DeepCopy()
		commonSpec.NodeSelector = map[string]string{nodeArchitectureLabel: arch}
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

		r.logger.WithField("build_namespace", build.Namespace).WithField("build_name", build.Name).Info("Creating build")
		if err := r.client.Create(ctx, build); err != nil {
			return fmt.Errorf("coudldn't create build %s/%s: %w", build.Namespace, build.Name, err)
		}
	}
	return nil
}

func (r *reconciler) handlePushImageWithManifest(ctx context.Context, mabc *v1.MultiArchBuildConfig, targetImageRef string) error {
	mutateFn := func(mabcToMutate *v1.MultiArchBuildConfig) {
		mabcToMutate.Status.Conditions = append(mabcToMutate.Status.Conditions, metav1.Condition{
			Type:               PushImageManifestDone,
			Status:             metav1.ConditionTrue,
			LastTransitionTime: metav1.Time{Time: time.Now()},
			Reason:             PushManifestSuccessReason,
		})
	}

	if err := r.manifestPusher.PushImageWithManifest(mabc.Status.Builds, targetImageRef); err != nil {
		mutateFn = func(mabcToMutate *v1.MultiArchBuildConfig) {
			mabcToMutate.Status.Conditions = append(mabcToMutate.Status.Conditions, metav1.Condition{
				Type:               PushImageManifestDone,
				Status:             metav1.ConditionFalse,
				LastTransitionTime: metav1.Time{Time: time.Now()},
				Reason:             PushManifestErrorReason,
				Message:            err.Error(),
			})
			mabcToMutate.Status.State = v1.FailureState
		}
	}

	if err := v1.UpdateMultiArchBuildConfig(ctx, r.logger, r.client, ctrlruntimeclient.ObjectKey{Namespace: mabc.Namespace, Name: mabc.Name}, mutateFn); err != nil {
		return fmt.Errorf("failed to update the MultiArchBuildConfig %s/%s: %w", mabc.Namespace, mabc.Name, err)
	}

	return nil
}

// handleMirrorImage pushes an image to the locations specified in .spec.external_registries. The image
// required has to exist on local registry.
func (r *reconciler) handleMirrorImage(ctx context.Context, srcImage string, mabc *v1.MultiArchBuildConfig) error {
	if len(mabc.Spec.ExternalRegistries) == 0 {
		return nil
	}

	mutateFn := func(mabcToMutate *v1.MultiArchBuildConfig) {
		mabcToMutate.Status.Conditions = append(mabcToMutate.Status.Conditions, metav1.Condition{
			Type:               MirrorImageManifestDone,
			Status:             metav1.ConditionTrue,
			LastTransitionTime: metav1.Time{Time: time.Now()},
			Reason:             ImageMirrorSuccessReason,
		})
		mabcToMutate.Status.State = v1.SuccessState
	}

	imageMirrorArgs := ocImageMirrorArgs(srcImage, mabc.Spec.ExternalRegistries)
	if err := r.imageMirrorer.mirror(imageMirrorArgs); err != nil {
		mutateFn = func(mabcToMutate *v1.MultiArchBuildConfig) {
			mabcToMutate.Status.Conditions = append(mabcToMutate.Status.Conditions, metav1.Condition{
				Type:               MirrorImageManifestDone,
				Status:             metav1.ConditionFalse,
				LastTransitionTime: metav1.Time{Time: time.Now()},
				Reason:             ImageMirrorErrorReason,
				Message:            fmt.Sprintf("oc image mirror: %s", err),
			})
			mabcToMutate.Status.State = v1.FailureState
		}
	}

	if err := v1.UpdateMultiArchBuildConfig(ctx, r.logger, r.client, ctrlruntimeclient.ObjectKey{Namespace: mabc.Namespace, Name: mabc.Name}, mutateFn); err != nil {
		return fmt.Errorf("failed to update the MultiArchBuildConfig %s/%s: %w", mabc.Namespace, mabc.Name, err)
	}

	return nil
}

func checkAllBuildsSuccessful(builds map[string]*buildv1.Build) bool {
	for _, build := range builds {
		if build.Status.Phase != buildv1.BuildPhaseComplete {
			return false
		}
	}
	return true
}

func checkAllBuildsFinished(builds map[string]*buildv1.Build) bool {
	for _, build := range builds {
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
	c := mabc.Status.Conditions[len(mabc.Status.Conditions)-1]
	return c.Type == PushImageManifestDone && c.Reason == PushManifestSuccessReason && c.Status == metav1.ConditionTrue
}
