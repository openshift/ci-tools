package steps

import (
	"context"
	"errors"
	"log"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/junit"
	"github.com/openshift/ci-tools/pkg/lease"
	"github.com/openshift/ci-tools/pkg/results"
)

const leaseEnv = "LEASED_RESOURCE"

// leaseStep wraps another step and acquires/releases a lease.
type leaseStep struct {
	client         *lease.Client
	leaseType      string
	leasedResource string
	wrapped        api.Step
}

func LeaseStep(client *lease.Client, lease string, wrapped api.Step) api.Step {
	return &leaseStep{
		client:    client,
		leaseType: lease,
		wrapped:   wrapped,
	}
}

func (s *leaseStep) Inputs(dry bool) (api.InputDefinition, error) {
	return s.wrapped.Inputs(dry)
}

func (s *leaseStep) Name() string             { return s.wrapped.Name() }
func (s *leaseStep) Description() string      { return s.wrapped.Description() }
func (s *leaseStep) Requires() []api.StepLink { return s.wrapped.Requires() }
func (s *leaseStep) Creates() []api.StepLink  { return s.wrapped.Creates() }
func (s *leaseStep) Provides() (api.ParameterMap, api.StepLink) {
	parameters, links := s.wrapped.Provides()
	if parameters == nil {
		parameters = api.ParameterMap{}
	}
	parameters[leaseEnv] = func() (string, error) {
		return s.leasedResource, nil
	}
	return parameters, links
}

func (s *leaseStep) SubTests() []*junit.TestCase {
	if subTests, ok := s.wrapped.(subtestReporter); ok {
		return subTests.SubTests()
	}
	return nil
}

func (s *leaseStep) Run(ctx context.Context, dry bool) error {
	return results.ForReason("utilizing_lease").ForError(s.run(ctx, dry))
}

func (s *leaseStep) run(ctx context.Context, dry bool) error {
	log.Printf("Acquiring lease for %q", s.leaseType)
	client := *s.client
	if client == nil {
		return results.ForReason("initializing_client").ForError(errors.New("step needs a lease but no lease client provided"))
	}
	ctx, cancel := context.WithCancel(ctx)
	lease, err := client.Acquire(s.leaseType, ctx, cancel)
	if err != nil {
		return results.ForReason("acquiring_lease").WithError(err).Errorf("failed to acquire lease: %v", err)
	}
	log.Printf("Acquired lease %q for %q", lease, s.leaseType)
	s.leasedResource = lease
	wrappedErr := results.ForReason("executing_test").ForError(s.wrapped.Run(ctx, dry))
	log.Printf("Releasing lease for %q", s.leaseType)
	releaseErr := results.ForReason("releasing_lease").ForError(client.Release(lease))

	// we want a sensible output error for reporting, so we bubble up these individually
	//if we can, as this is the only step that can have multiple errors
	if wrappedErr != nil && releaseErr == nil {
		return wrappedErr
	} else if wrappedErr == nil && releaseErr != nil {
		return releaseErr
	} else {
		return utilerrors.NewAggregate([]error{wrappedErr, releaseErr})
	}
}
