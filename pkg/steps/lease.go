package steps

import (
	"context"
	"fmt"
	"log"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/junit"
	"github.com/openshift/ci-tools/pkg/lease"
)

// leaseStep wraps another step and acquires/releases a lease.
type leaseStep struct {
	client    lease.Client
	leaseType string
	wrapped   api.Step
}

func LeaseStep(client lease.Client, lease string, wrapped api.Step) api.Step {
	return &leaseStep{
		client:    client,
		leaseType: lease,
		wrapped:   wrapped,
	}
}

func (s *leaseStep) Inputs(ctx context.Context, dry bool) (api.InputDefinition, error) {
	return s.wrapped.Inputs(ctx, dry)
}

func (s *leaseStep) Name() string                               { return s.wrapped.Name() }
func (s *leaseStep) Description() string                        { return s.wrapped.Description() }
func (s *leaseStep) Requires() []api.StepLink                   { return s.wrapped.Requires() }
func (s *leaseStep) Creates() []api.StepLink                    { return s.wrapped.Creates() }
func (s *leaseStep) Provides() (api.ParameterMap, api.StepLink) { return s.wrapped.Provides() }

func (s *leaseStep) SubTests() []*junit.TestCase {
	if subTests, ok := s.wrapped.(subtestReporter); ok {
		return subTests.SubTests()
	}
	return nil
}

func (s *leaseStep) Run(ctx context.Context, dry bool) error {
	log.Printf("Acquiring lease for %q", s.leaseType)
	if s.client == nil {
		return fmt.Errorf("step needs a lease but no lease client provided")
	}
	ctx, cancel := context.WithCancel(ctx)
	lease, err := s.client.Acquire(s.leaseType, ctx, cancel)
	if err != nil {
		return fmt.Errorf("failed to acquire lease: %v", err)
	}
	var errs []error
	errs = append(errs, s.wrapped.Run(ctx, dry))
	log.Printf("Releasing lease for %q", s.leaseType)
	errs = append(errs, s.client.Release(lease))
	return utilerrors.NewAggregate(errs)
}

func (s *leaseStep) Done() (bool, error) { return s.wrapped.Done() }
