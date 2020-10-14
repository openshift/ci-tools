package steps

import (
	"context"
	"errors"
	"log"
	"strings"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	coreclientset "k8s.io/client-go/kubernetes/typed/core/v1"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/junit"
	"github.com/openshift/ci-tools/pkg/lease"
	"github.com/openshift/ci-tools/pkg/results"
)

const leaseEnv = "LEASED_RESOURCE"

var NoLeaseClientErr = errors.New("step needs a lease but no lease client provided")

// leaseStep wraps another step and acquires/releases a lease.
type leaseStep struct {
	client         *lease.Client
	leaseType      string
	leasedResource string
	wrapped        api.Step

	// for sending heartbeats during lease acquisition
	namespace       func() string
	namespaceClient coreclientset.NamespaceInterface
}

func LeaseStep(client *lease.Client, lease string, wrapped api.Step, namespace func() string, namespaceClient coreclientset.NamespaceInterface) api.Step {
	return &leaseStep{
		client:          client,
		leaseType:       lease,
		wrapped:         wrapped,
		namespace:       namespace,
		namespaceClient: namespaceClient,
	}
}

func (s *leaseStep) Inputs() (api.InputDefinition, error) {
	return s.wrapped.Inputs()
}

func (s *leaseStep) Validate() error {
	if s.client == nil {
		return NoLeaseClientErr
	}
	return nil
}

func (s *leaseStep) Name() string             { return s.wrapped.Name() }
func (s *leaseStep) Description() string      { return s.wrapped.Description() }
func (s *leaseStep) Requires() []api.StepLink { return s.wrapped.Requires() }
func (s *leaseStep) Creates() []api.StepLink  { return s.wrapped.Creates() }
func (s *leaseStep) Provides() api.ParameterMap {
	parameters := s.wrapped.Provides()
	if parameters == nil {
		parameters = api.ParameterMap{}
	}
	parameters[leaseEnv] = func() (string, error) {
		chunks := strings.SplitN(s.leasedResource, "--", 2)
		return chunks[0], nil
	}
	return parameters
}

func (s *leaseStep) SubTests() []*junit.TestCase {
	if subTests, ok := s.wrapped.(subtestReporter); ok {
		return subTests.SubTests()
	}
	return nil
}

func (s *leaseStep) Run(ctx context.Context) error {
	return results.ForReason("utilizing_lease").ForError(s.run(ctx))
}

func (s *leaseStep) run(ctx context.Context) error {
	log.Printf("Acquiring lease for %q", s.leaseType)
	client := *s.client
	ctx, cancel := context.WithCancel(ctx)
	resource, err := client.Acquire(s.leaseType, ctx, cancel)
	if err != nil {
		if err == lease.ErrNotFound {
			printResourceMetrics(client, s.leaseType)
		}
		return results.ForReason(results.Reason("acquiring_lease:"+s.leaseType)).WithError(err).Errorf("failed to acquire lease: %v", err)
	}
	log.Printf("Acquired lease %q for %q", resource, s.leaseType)
	s.leasedResource = resource
	wrappedErr := results.ForReason("executing_test").ForError(s.wrapped.Run(ctx))
	log.Printf("Releasing lease for %q", s.leaseType)
	releaseErr := results.ForReason("releasing_lease").ForError(client.Release(resource))

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

func printResourceMetrics(client lease.Client, rtype string) {
	m, err := client.Metrics(rtype)
	if err != nil {
		log.Printf("warning: Could not get resource metrics: %v", err)
		return
	}
	log.Printf("error: Failed to acquire resource, current capacity: %d free, %d leased", m.Free, m.Leased)
}
