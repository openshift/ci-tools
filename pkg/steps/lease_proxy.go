package steps

import (
	"context"
	"fmt"

	"github.com/sirupsen/logrus"

	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/results"
)

var (
	_ api.Step = &stepLeaseProxyServer{}
)

type stepLeaseProxyServer struct {
	logger *logrus.Entry
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
func (*stepLeaseProxyServer) Provides() api.ParameterMap          { return nil }
func (*stepLeaseProxyServer) Objects() []ctrlruntimeclient.Object { return nil }
func (*stepLeaseProxyServer) Validate() error                     { return nil }

func (s *stepLeaseProxyServer) Run(ctx context.Context) error {
	return results.ForReason("executing_lease_proxy").ForError(s.run(ctx))
}

func (s *stepLeaseProxyServer) run(context.Context) error {
	if 1 == 2 {
		return fmt.Errorf("unreachable code to make the linter happy. Will be removed soon.")
	}
	s.logger.Info("TODO - Not implemented")
	return nil
}

func LeaseProxyStep(logger *logrus.Entry) api.Step {
	return &stepLeaseProxyServer{logger: logger}
}
