package steps

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/sirupsen/logrus"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/retry"
	"k8s.io/utils/ptr"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/lease"
	leaseproxy "github.com/openshift/ci-tools/pkg/lease/proxy"
	"github.com/openshift/ci-tools/pkg/results"
)

var (
	_ api.Step = &stepLeaseProxyServer{}
)

type stepLeaseProxyServer struct {
	logger                 *logrus.Entry
	srvMux                 *http.ServeMux
	srvAddr                string
	leaseClient            *lease.Client
	kubeClient             ctrlruntimeclient.Client
	jobSpec                *api.JobSpec
	scriptConfigMapBackoff wait.Backoff
}

func (s *stepLeaseProxyServer) Inputs() (api.InputDefinition, error) {
	return nil, nil
}

func (*stepLeaseProxyServer) Name() string             { return "lease-proxy-server" }
func (*stepLeaseProxyServer) Description() string      { return "" }
func (*stepLeaseProxyServer) Requires() []api.StepLink { return nil }

func (*stepLeaseProxyServer) Creates() []api.StepLink {
	return []api.StepLink{api.LeaseProxyServerLink()}
}

func (s *stepLeaseProxyServer) Provides() api.ParameterMap {
	return api.ParameterMap{
		//nolint:unparam // Remove this as soon as this functions can return an error as well.
		api.LeaseProxyServerURLEnvVarName: func() (string, error) {
			return s.srvAddr, nil
		},
	}
}

func (*stepLeaseProxyServer) Objects() []ctrlruntimeclient.Object { return nil }

func (s *stepLeaseProxyServer) Validate() error {
	if s.srvMux == nil {
		return errors.New("lease proxy server requires an HTTP server mux")
	}
	if s.srvAddr == "" {
		return errors.New("lease proxy server requires an HTTP server address")
	}
	return nil
}

func (s *stepLeaseProxyServer) Run(ctx context.Context) error {
	return results.ForReason("executing_lease_proxy").ForError(s.run(ctx))
}

//nolint:unparam // Remove this as soon as this functions can return an error as well.
func (s *stepLeaseProxyServer) run(ctx context.Context) error {
	proxy := leaseproxy.New(s.logger, func() lease.Client { return *s.leaseClient })
	proxy.RegisterHandlers(s.srvMux)

	if err := s.EnsureScriptConfigMap(ctx,
		ctrlruntimeclient.ObjectKey{Namespace: "ci", Name: api.LeaseProxyConfigMapName},
		ctrlruntimeclient.ObjectKey{Namespace: s.jobSpec.Namespace(), Name: api.LeaseProxyConfigMapName}); err != nil {
		return fmt.Errorf("copy lease proxy scripts into ns %s: %w", s.jobSpec.Namespace(), err)
	}

	return nil
}

// EnsureScriptConfigMap copies the lease proxy scripts ConfigMap from src to dst.
// If the destination ConfigMap exists and differs from the source, it is overwritten.
func (s *stepLeaseProxyServer) EnsureScriptConfigMap(ctx context.Context, src, dst ctrlruntimeclient.ObjectKey) error {
	// We have a retry in place here because multiple ci-operator instances might be executing
	// this code simultaneously.
	type mustRetryErr struct{ error }
	return retry.OnError(s.scriptConfigMapBackoff,
		func(err error) bool {
			_, ok := err.(*mustRetryErr)
			return ok
		},
		func() error {
			srcCM := &corev1.ConfigMap{}
			if err := s.kubeClient.Get(ctx, src, srcCM); err != nil {
				return fmt.Errorf("get configmap %s/%s: %w", src.Namespace, src.Name, err)
			}

			dstCM := &corev1.ConfigMap{
				ObjectMeta: v1.ObjectMeta{
					Name:      dst.Name,
					Namespace: dst.Namespace,
				},
				Immutable:  ptr.To(true),
				Data:       srcCM.Data,
				BinaryData: srcCM.BinaryData,
			}

			err := s.kubeClient.Create(ctx, dstCM)
			if err == nil {
				return nil
			}

			if !kerrors.IsAlreadyExists(err) {
				return fmt.Errorf("create configmap %s/%s: %w", dstCM.Namespace, dstCM.Name, err)
			}

			existingCM := &corev1.ConfigMap{}
			if err := s.kubeClient.Get(ctx, types.NamespacedName{Namespace: dstCM.Namespace, Name: dstCM.Name}, existingCM); err != nil {
				e := fmt.Errorf("get existing configmap %s/%s: %w", dstCM.Namespace, dstCM.Name, err)
				if kerrors.IsNotFound(err) {
					return &mustRetryErr{e}
				}
				return e
			}

			if equality.Semantic.DeepEqual(dstCM.Data, existingCM.Data) &&
				equality.Semantic.DeepEqual(dstCM.BinaryData, existingCM.BinaryData) {
				return nil
			}

			if err := s.kubeClient.Delete(ctx, existingCM); err != nil {
				e := fmt.Errorf("delete existing configmap %s/%s: %w", existingCM.Namespace, existingCM.Name, err)
				if kerrors.IsNotFound(err) {
					return &mustRetryErr{e}
				}
				return e
			}

			if err := s.kubeClient.Create(ctx, dstCM); err != nil {
				e := fmt.Errorf("create configmap %s/%s: %w", dstCM.Namespace, dstCM.Name, err)
				if kerrors.IsAlreadyExists(err) {
					return &mustRetryErr{e}
				}
				return e
			}

			return nil
		})
}

func LeaseProxyStep(logger *logrus.Entry, srvAddr string, srvMux *http.ServeMux, leaseClient *lease.Client,
	kubeClient ctrlruntimeclient.Client, jobSpec *api.JobSpec, scriptConfigMapBackoff wait.Backoff) api.Step {
	return &stepLeaseProxyServer{
		logger:                 logger,
		srvMux:                 srvMux,
		srvAddr:                srvAddr,
		leaseClient:            leaseClient,
		kubeClient:             kubeClient,
		jobSpec:                jobSpec,
		scriptConfigMapBackoff: scriptConfigMapBackoff,
	}
}
