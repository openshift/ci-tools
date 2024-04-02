package steps

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/sirupsen/logrus"

	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/junit"
	"github.com/openshift/ci-tools/pkg/lease"
	"github.com/openshift/ci-tools/pkg/results"
)

var NoLeaseClientForIPErr = errors.New("step needs access to an IP pool, but no lease client provided")

// ipPoolStep wraps another step and acquires/releases chunks of IPs.
type ipPoolStep struct {
	client      *lease.Client
	ipPoolLease stepLease
	wrapped     api.Step
	params      api.Parameters

	// for sending heartbeats during pool acquisition
	namespace func() string
}

func IPPoolStep(client *lease.Client, lease api.StepLease, wrapped api.Step, params api.Parameters, namespace func() string) api.Step {
	ret := ipPoolStep{
		client:      client,
		wrapped:     wrapped,
		namespace:   namespace,
		params:      params,
		ipPoolLease: stepLease{StepLease: lease},
	}
	return &ret
}

func (s *ipPoolStep) Inputs() (api.InputDefinition, error) {
	return s.wrapped.Inputs()
}

func (s *ipPoolStep) Validate() error {
	if s.client == nil {
		return NoLeaseClientForIPErr
	}
	return nil
}

func (s *ipPoolStep) Name() string                        { return s.wrapped.Name() }
func (s *ipPoolStep) Description() string                 { return s.wrapped.Description() }
func (s *ipPoolStep) Requires() []api.StepLink            { return s.wrapped.Requires() }
func (s *ipPoolStep) Creates() []api.StepLink             { return s.wrapped.Creates() }
func (s *ipPoolStep) Objects() []ctrlruntimeclient.Object { return s.wrapped.Objects() }

func (s *ipPoolStep) Provides() api.ParameterMap {
	parameters := s.wrapped.Provides()
	if parameters == nil {
		parameters = api.ParameterMap{}
	}
	l := &s.ipPoolLease
	// Disable unparam lint as we need to confirm to this interface, but there will never be an error
	//nolint:unparam
	parameters[l.Env] = func() (string, error) {
		return strconv.Itoa(len(l.resources)), nil
	}
	return parameters
}

func (s *ipPoolStep) SubTests() []*junit.TestCase {
	if subTests, ok := s.wrapped.(SubtestReporter); ok {
		return subTests.SubTests()
	}
	return nil
}

func (s *ipPoolStep) Run(ctx context.Context) error {
	return results.ForReason("utilizing_ip_pool").ForError(s.run(ctx))
}

func (s *ipPoolStep) run(ctx context.Context) error {
	l := &s.ipPoolLease
	region, err := s.params.Get(api.DefaultLeaseEnv)
	if err != nil || region == "" {
		return results.ForReason("acquiring_ip_pool_lease").WithError(err).Errorf("failed to determine region to acquire lease for %s", l.ResourceType)
	}
	l.ResourceType = fmt.Sprintf("%s-%s", l.ResourceType, region)
	logrus.Infof("Acquiring IP Pool leases for test %s: %v", s.Name(), l.ResourceType)
	client := *s.client
	ctx, cancel := context.WithCancel(ctx)

	names, err := client.AcquireIfAvailableImmediately(l.ResourceType, l.Count, cancel)
	if err != nil {
		if err == lease.ErrNotFound {
			logrus.Infof("no leases of type: %s available", l.ResourceType)
		} else {
			return results.ForReason("acquiring_ip_pool_lease").WithError(err).Errorf("failed to acquire lease for %s: %v", l.ResourceType, err)
		}
	} else {
		logrus.Infof("Acquired %d ip pool lease(s) for %s: %v", l.Count, l.ResourceType, names)
		s.ipPoolLease.resources = names
	}

	wrappedErr := results.ForReason("executing_test").ForError(s.wrapped.Run(ctx))
	logrus.Infof("Releasing ip pool leases for test %s", s.Name())
	releaseErr := results.ForReason("releasing_ip_pool_lease").ForError(releaseLeases(client, *l))

	return aggregateWrappedErrorAndReleaseError(wrappedErr, releaseErr)
}
