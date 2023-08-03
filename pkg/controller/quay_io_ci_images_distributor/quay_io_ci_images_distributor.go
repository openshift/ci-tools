package quay_io_ci_images_distributor

import (
	"context"
	"errors"
	"fmt"

	"github.com/sirupsen/logrus"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	imagev1 "github.com/openshift/api/image/v1"

	controllerutil "github.com/openshift/ci-tools/pkg/controller/util"
	"github.com/openshift/ci-tools/pkg/util/imagestreamtagmapper"
	"github.com/openshift/ci-tools/pkg/util/imagestreamtagwrapper"
)

const ControllerName = "quay_io_ci_images_distributor"

func AddToManager(manager manager.Manager, additionalImageStreamNamespaces sets.Set[string]) error {
	log := logrus.WithField("controller", ControllerName)

	r := &reconciler{
		log:                             log,
		client:                          imagestreamtagwrapper.MustNew(manager.GetClient(), manager.GetCache()),
		additionalImageStreamNamespaces: additionalImageStreamNamespaces,
	}
	c, err := controller.New(ControllerName, manager, controller.Options{
		Reconciler: r,
	})
	if err != nil {
		return fmt.Errorf("failed to construct controller: %w", err)
	}
	if err := c.Watch(
		source.Kind(manager.GetCache(), &imagev1.ImageStream{}),
		imagestreamtagmapper.New(func(in reconcile.Request) []reconcile.Request {
			if additionalImageStreamNamespaces.Has(in.Namespace) {
				return []reconcile.Request{in}
			}
			return nil
		}),
	); err != nil {
		return fmt.Errorf("failed to create watch for ImageStreams: %w", err)
	}

	r.log.Info("Successfully added reconciler to manager")
	return nil
}

type reconciler struct {
	log                             *logrus.Entry
	client                          ctrlruntimeclient.Client
	additionalImageStreamNamespaces sets.Set[string]
}

func (r *reconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	log := r.log.WithField("request", req.String())
	err := r.reconcile(ctx, req, log)
	if err != nil && !apierrors.IsAlreadyExists(err) {
		log.WithError(err).Error("Reconciliation failed")
	} else {
		log.Info("Finished reconciliation")
	}
	return reconcile.Result{}, controllerutil.SwallowIfTerminal(err)
}

func (r *reconciler) reconcile(ctx context.Context, req reconcile.Request, log *logrus.Entry) error {
	*log = *log.WithField("namespace", req.Namespace).WithField("name", req.Name)
	log.Info("Starting reconciliation")
	// This is to make the linter happy
	// TODO (hongkliu): implement this logic with sense-making errors
	if ctx == nil {
		return errors.New("nil ctx")
	}
	return nil
}
