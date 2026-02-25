package steps

import (
	"context"
	"errors"
	"net/http"

	"github.com/sirupsen/logrus"

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
	logger      *logrus.Entry
	srvMux      *http.ServeMux
	srvAddr     string
	leaseClient *lease.Client
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
func (s *stepLeaseProxyServer) run(context.Context) error {
	proxy := leaseproxy.New(s.logger, func() lease.Client { return *s.leaseClient })
	proxy.RegisterHandlers(s.srvMux)
	return nil
}

func LeaseProxyStep(logger *logrus.Entry, srvAddr string, srvMux *http.ServeMux, leaseClient *lease.Client) api.Step {
	return &stepLeaseProxyServer{
		srvAddr:     srvAddr,
		srvMux:      srvMux,
		leaseClient: leaseClient,
		logger:      logger,
	}
}
