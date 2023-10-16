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
	PushImageManifestDone string = "PushManifestDone"
)

func AddToManager(mgr manager.Manager, architectures []string, dockerCfgPath string) error {
	if err := buildsreconciler.AddToManager(mgr); err != nil {
		return fmt.Errorf("failed to construct builds reconciler: %w", err)
	}

	logger := logrus.WithField("controller", controllerName)
	c, err := controller.New(controllerName, mgr, controller.Options{
		MaxConcurrentReconciles: 1,
		Reconciler: &reconciler{
			logger:           logger,
			client:           mgr.GetClient(),
			architectures:    architectures,
			dockerConfigPath: dockerCfgPath,
			manifestPusher:   manifestpusher.NewManifestPushfer(logger, registryURL, dockerCfgPath),
			timeNowFn:        time.Now,
			mirrorImagesFn:   mirrorImages,
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
	logger           *logrus.Entry
	client           ctrlruntimeclient.Client
	architectures    []string
	manifestPusher   manifestpusher.ManifestPusher
	dockerConfigPath string

	// For testing purpose only
	timeNowFn      func() time.Time
	mirrorImagesFn func(log *logrus.Entry, registryConfig string, images []string) error
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

	targetImageRef := fmt.Sprintf("%s/%s", mabc.Spec.BuildSpec.CommonSpec.Output.To.Namespace, mabc.Spec.BuildSpec.CommonSpec.Output.To.Name)
	mutateFn := r.runPostBuildActions(
		// Make sure the builds completed successfully
		func() (func(mabcToMutate *v1.MultiArchBuildConfig), bool) {
			return nil, checkAllBuildsSuccessful(mabc.Status.Builds)
		},
		// Assemble and push the manifest list
		func() (func(mabcToMutate *v1.MultiArchBuildConfig), bool) {
			return r.handlePushImageWithManifest(mabc, targetImageRef)
		},
		// Mirror to external registries
		func() (func(mabcToMutate *v1.MultiArchBuildConfig), bool) {
			srcImage := fmt.Sprintf("%s/%s", registryURL, targetImageRef)
			return r.handleMirrorImage(srcImage, mabc)
		},
	)

	if err := v1.UpdateMultiArchBuildConfig(ctx, r.logger, r.client, ctrlruntimeclient.ObjectKey{Namespace: mabc.Namespace, Name: mabc.Name}, mutateFn); err != nil {
		return fmt.Errorf("failed to update the MultiArchBuildConfig %s/%s: %w", mabc.Namespace, mabc.Name, err)
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

func (r *reconciler) handlePushImageWithManifest(mabc *v1.MultiArchBuildConfig, targetImageRef string) (func(mabcToMutate *v1.MultiArchBuildConfig), bool) {
	if getCondition(mabc, PushImageManifestDone) != nil {
		return nil, true
	}

	if err := r.manifestPusher.PushImageWithManifest(mabc.Status.Builds, targetImageRef); err != nil {
		return func(mabcToMutate *v1.MultiArchBuildConfig) {
			mabcToMutate.Status.Conditions = append(mabcToMutate.Status.Conditions, metav1.Condition{
				Type:               PushImageManifestDone,
				Status:             metav1.ConditionFalse,
				LastTransitionTime: metav1.Time{Time: time.Now()},
				Reason:             "PushManifestError",
				Message:            err.Error(),
			})
			mabcToMutate.Status.State = v1.FailureState
		}, false
	}

	return func(mabcToMutate *v1.MultiArchBuildConfig) {
		mabcToMutate.Status.Conditions = append(mabcToMutate.Status.Conditions, metav1.Condition{
			Type:               PushImageManifestDone,
			Status:             metav1.ConditionTrue,
			LastTransitionTime: metav1.Time{Time: time.Now()},
			Reason:             "PushManifestSuccess",
		})
	}, true
}

// runPostBuildActions ensure some actions are performed after the builds have been spawned.
// Actions are performed in order, one after the other. If at least one fails then the chain breaks
// and the overall status is set to failed.
func (r *reconciler) runPostBuildActions(actions ...func() (func(mabcToMutate *v1.MultiArchBuildConfig), bool)) func(mabcToMutate *v1.MultiArchBuildConfig) {
	// Every action might produce a mutator, so this array might be at most len(actions)+1 in size.
	// The last element (+1) is given by the mutator that set the overall status.
	mutateFuncs := make([]func(mabcToMutate *v1.MultiArchBuildConfig), 0, len(actions)+1)

	var atLeastOneFailed bool

	for _, action := range actions {
		mutateFunc, succeeded := action()
		if mutateFunc != nil {
			mutateFuncs = append(mutateFuncs, mutateFunc)
		}
		if !succeeded {
			atLeastOneFailed = true
			break
		}
	}

	if atLeastOneFailed {
		mutateFuncs = append(mutateFuncs, func(mabcToMutate *v1.MultiArchBuildConfig) {
			mabcToMutate.Status.State = v1.FailureState
		})
	} else {
		mutateFuncs = append(mutateFuncs, func(mabcToMutate *v1.MultiArchBuildConfig) {
			mabcToMutate.Status.State = v1.SuccessState
		})
	}

	return func(mabcToMutate *v1.MultiArchBuildConfig) {
		for _, f := range mutateFuncs {
			f(mabcToMutate)
		}
	}
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
