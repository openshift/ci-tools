package steps

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/sirupsen/logrus"

	coreapi "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/junit"
	"github.com/openshift/ci-tools/pkg/lease"
	"github.com/openshift/ci-tools/pkg/results"
	"github.com/openshift/ci-tools/pkg/steps/loggingclient"
)

var NoLeaseClientForIPErr = errors.New("step needs access to an IP pool, but no lease client provided")

// ipPoolStep wraps another step and acquires/releases chunks of IPs.
type ipPoolStep struct {
	client       *lease.Client
	secretClient loggingclient.LoggingClient
	ipPoolLease  stepLease
	wrapped      api.Step
	params       api.Parameters

	namespace func() string
}

func IPPoolStep(client *lease.Client, secretClient loggingclient.LoggingClient, lease api.StepLease, wrapped api.Step, params api.Parameters, namespace func() string) api.Step {
	ret := ipPoolStep{
		client:       client,
		secretClient: secretClient,
		wrapped:      wrapped,
		namespace:    namespace,
		params:       params,
		ipPoolLease:  stepLease{StepLease: lease},
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
	return results.ForReason("utilizing_ip_pool").ForError(s.run(ctx, time.Minute))
}

// minute is provided as an argument to assist with unit testing
func (s *ipPoolStep) run(ctx context.Context, minute time.Duration) error {
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

	remainingResources := make(chan []string)
	if len(names) > 0 {
		go checkAndReleaseUnusedLeases(ctx, s.namespace(), s.wrapped.Name(), names, s.secretClient, s.client, minute, remainingResources)
	}

	wrappedErr := results.ForReason("executing_test").ForError(s.wrapped.Run(ctx))
	logrus.Infof("Releasing ip pool leases for test %s", s.Name())
	select {
	case s.ipPoolLease.resources = <-remainingResources:
		logrus.Debugf("resources left to release after unused have already been released %v", s.ipPoolLease.resources)
	default:
		logrus.Debug("no unused resources were released, releasing all")
	}
	releaseErr := results.ForReason("releasing_ip_pool_lease").ForError(releaseLeases(client, *l))

	return aggregateWrappedErrorAndReleaseError(wrappedErr, releaseErr)
}

const UnusedIpCount = "UNUSED_IP_COUNT"

// checkAndReleaseUnusedLeases periodically checks for a positive value in the
// UNUSED_IP_COUNT data in the shared secret which signals that there are leases that can be released.
// If/when this is discovered it will release that number of leases, and stop checking.
// minute is provided as an argument to assist with unit testing.
// The remainingResources channel stores the names of the resources that haven't been released if applicable.
func checkAndReleaseUnusedLeases(ctx context.Context, namespace, testName string, resources []string, secretClient ctrlruntimeclient.Client, leaseClient *lease.Client, minute time.Duration, remainingResources chan<- []string) {
	waitUntil := time.After(minute * 15)
	sharedDirKey := types.NamespacedName{
		Namespace: namespace,
		Name:      testName, // This is the name of the shared-dir secret
	}
	ticker := time.NewTicker(minute)
	defer ticker.Stop()
	for range ticker.C {
		logrus.Debugf("checking for unused ip-pool leases to release")
		select {
		case <-ctx.Done():
			return
		case <-waitUntil:
			logrus.Debugf("timeout to check for unused ip-pool leases has passed, no longer waiting")
			return
		default:
			sharedDirSecret := coreapi.Secret{}
			if err := secretClient.Get(ctx, sharedDirKey, &sharedDirSecret); err != nil {
				logrus.WithError(err).Warn("could not get shared dir secret")
				continue
			}
			rawCount := string(sharedDirSecret.Data[UnusedIpCount])
			if rawCount == "" {
				continue
			}
			rawCount = strings.TrimSpace(rawCount)
			count, err := strconv.Atoi(rawCount)
			if err != nil {
				logrus.WithError(err).Warnf("cannot convert %s contents to int", UnusedIpCount)
			}
			logrus.Infof("there are %d unused ip-pool addresses to release", count)

			client := *leaseClient
			if count > len(resources) {
				logrus.Warnf("requested to release %d ip-pool leases, but only %d have been leased; ignoring request", count, len(resources))
				return
			}
			for i := 0; i < count; i++ {
				name := resources[i]
				logrus.Infof("releasing unused ip-pool lease: %s", name)
				if err = client.Release(name); err != nil {
					logrus.WithError(err).Warnf("cannot release ip-pool lease %s", name)
				}
			}
			remainingResources <- resources[count:]
			return
		}
	}
}
