package steps

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/sirupsen/logrus"

	coreapi "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/junit"
	"github.com/openshift/ci-tools/pkg/lease"
	"github.com/openshift/ci-tools/pkg/metrics"
	"github.com/openshift/ci-tools/pkg/results"
	"github.com/openshift/ci-tools/pkg/util"
)

var NoLeaseClientErr = errors.New("step needs a lease but no lease client provided")

type stepLease struct {
	api.StepLease
	resources []string
}

type ClusterProfileGetter func(name string) (*api.ClusterProfileDetails, error)

// leaseStep wraps another step and acquires/releases one or more leases.
type leaseStep struct {
	client               *lease.Client
	leases               []stepLease
	wrapped              api.Step
	metricsAgent         *metrics.MetricsAgent
	kubeClient           ctrlruntimeclient.Client
	clusterProfileGetter ClusterProfileGetter

	// for sending heartbeats during lease acquisition
	namespace             func() string
	clusterProfileSetName string
	clusterProfileName    string
}

func LeaseStep(client *lease.Client, leases []api.StepLease, wrapped api.Step, namespace func() string, metricsAgent *metrics.MetricsAgent,
	kubeClient ctrlruntimeclient.Client, clusterProfileGetter ClusterProfileGetter) api.Step {
	ret := leaseStep{
		client:               client,
		wrapped:              wrapped,
		namespace:            namespace,
		metricsAgent:         metricsAgent,
		kubeClient:           kubeClient,
		clusterProfileGetter: clusterProfileGetter,
	}
	for _, l := range leases {
		ret.leases = append(ret.leases, stepLease{StepLease: l})
	}
	return &ret
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

func (s *leaseStep) Name() string                        { return s.wrapped.Name() }
func (s *leaseStep) Description() string                 { return s.wrapped.Description() }
func (s *leaseStep) Requires() []api.StepLink            { return s.wrapped.Requires() }
func (s *leaseStep) Creates() []api.StepLink             { return s.wrapped.Creates() }
func (s *leaseStep) Objects() []ctrlruntimeclient.Object { return s.wrapped.Objects() }

func (s *leaseStep) Provides() api.ParameterMap {
	parameters := s.wrapped.Provides()
	if parameters == nil {
		parameters = api.ParameterMap{}
	}

	// nolint:unparam
	parameters[api.ClusterProfileSetEnv] = func() (string, error) { return s.clusterProfileSetName, nil }
	// nolint:unparam
	parameters[api.ClusterProfileParam] = func() (string, error) { return s.clusterProfileName, nil }

	for i := range s.leases {
		l := &s.leases[i]
		// nolint:unparam
		parameters[l.Env] = func() (string, error) {
			if len(l.resources) == 0 {
				return "", nil
			}

			values := func(yield func(resource string) bool) {
				for _, res := range l.resources {
					val := res
					parts := strings.Split(res, "--")
					switch {
					case len(parts) > 2:
						val = parts[1]
					case len(parts) > 1:
						val = parts[0]
					}

					if !yield(val) {
						return
					}
				}
			}

			return strings.Join(slices.Collect(values), " "), nil
		}
	}

	return parameters
}

func (s *leaseStep) SubTests() []*junit.TestCase {
	if subTests, ok := s.wrapped.(SubtestReporter); ok {
		return subTests.SubTests()
	}
	return nil
}

func (s *leaseStep) Run(ctx context.Context) error {
	return results.ForReason("utilizing_lease").ForError(s.run(ctx))
}

func (s *leaseStep) run(ctx context.Context) error {
	var types []string
	for i := range s.leases {
		types = append(types, s.leases[i].ResourceType)
	}
	logrus.Infof("Acquiring leases for test %s: %v", s.Name(), types)
	client := *s.client
	ctx, cancel := context.WithCancel(ctx)
	if err := s.acquireLeases(ctx, cancel); err != nil {
		return err
	}
	wrappedErr := results.ForReason("executing_test").ForError(s.wrapped.Run(ctx))
	logrus.Infof("Releasing leases for test %s", s.Name())
	releaseErr := results.ForReason("releasing_lease").ForError(releaseLeases(client, s.metricsAgent, s.leases...))

	return aggregateWrappedErrorAndReleaseError(wrappedErr, releaseErr)
}

func aggregateWrappedErrorAndReleaseError(wrappedErr, releaseErr error) error {
	// we want a sensible output error for reporting, so we bubble up these individually if we can
	if wrappedErr != nil && releaseErr == nil {
		return wrappedErr
	} else if wrappedErr == nil && releaseErr != nil {
		return releaseErr
	} else {
		return utilerrors.NewAggregate([]error{wrappedErr, releaseErr})
	}
}

func (s *leaseStep) acquireLeases(ctx context.Context, cancel context.CancelFunc) error {
	client := *s.client
	// Sort by resource type to avoid a(n unlikely and temporary) deadlock.
	var sorted []int
	for i := range s.leases {
		sorted = append(sorted, i)
	}
	sort.Slice(sorted, func(i, j int) bool {
		return s.leases[i].ResourceType < s.leases[j].ResourceType
	})
	var errs []error
	for _, i := range sorted {
		l := &s.leases[i]
		start := time.Now()
		logrus.Debugf("Acquiring %d lease(s) for %s", l.Count, l.ResourceType)
		names, err := client.Acquire(l.ResourceType, l.Count, ctx, cancel)
		if err != nil {
			if err == lease.ErrNotFound {
				printResourceMetrics(client, l.ResourceType)
			}
			errs = append(errs, results.ForReason(results.Reason("acquiring_lease")).WithError(err).Errorf("failed to acquire lease for %q: %v", l.ResourceType, err))
			break
		}

		s.clusterProfileName = l.ClusterProfile
		if clusterProfileName := clusterProfileFromResources(names); clusterProfileName != "" {
			s.clusterProfileSetName = l.ClusterProfile
			s.clusterProfileName = clusterProfileName
		}

		if s.clusterProfileName != "" {
			cpDetails, err := s.clusterProfileGetter(s.clusterProfileName)
			if err != nil {
				errs = append(errs, fmt.Errorf("resolve cluster profile %s: %w", s.clusterProfileName, err))
				break
			}

			if err := s.importClusterProfileSecret(ctx, cpDetails.Secret, l.ClusterProfileTarget); err != nil {
				errs = append(errs, fmt.Errorf("import secret %s for cluster profile %s: %w", cpDetails.Secret, s.clusterProfileName, err))
				break
			}
		}

		for _, name := range names {
			s.metricsAgent.Record(&metrics.LeaseAcquisitionMetricEvent{RawLeaseName: name, AcquisitionDurationSeconds: time.Since(start).Seconds()})
		}

		logrus.Infof("Acquired %d lease(s) for %s: %v", l.Count, l.ResourceType, names)
		l.resources = names
	}

	if errs != nil {
		if err := releaseLeases(client, s.metricsAgent, s.leases...); err != nil {
			errs = append(errs, fmt.Errorf("failed to release leases after acquisition failure: %w", err))
		}
	}

	return utilerrors.NewAggregate(errs)
}

func releaseLeases(client lease.Client, metricsAgent *metrics.MetricsAgent, leases ...stepLease) error {
	var errs []error
	for _, l := range leases {
		for _, r := range l.resources {
			if r == "" {
				continue
			}
			logrus.Debugf("Releasing lease for %s: %v", l.ResourceType, r)
			releaseEvent := &metrics.LeaseReleaseMetricEvent{RawLeaseName: r, Released: true}
			start := time.Now()
			if err := client.Release(r); err != nil {
				errs = append(errs, err)
				releaseEvent.Released = false
				releaseEvent.Error = err.Error()
			}
			releaseEvent.ReleaseDurationSeconds = time.Since(start).Seconds()

			metricsAgent.Record(releaseEvent)
		}
	}
	return utilerrors.NewAggregate(errs)
}

func printResourceMetrics(client lease.Client, rtype string) {
	m, err := client.Metrics(rtype)
	if err != nil {
		logrus.WithError(err).Warn("Could not get resource metrics.")
		return
	}
	logrus.Errorf("error: Failed to acquire resource, current capacity: %d free, %d leased", m.Free, m.Leased)
}

func clusterProfileFromResources(resources []string) string {
	for _, resource := range resources {
		if parts := strings.Split(resource, "--"); len(parts) > 2 {
			return parts[0]
		}
	}
	return ""
}

// importClusterProfileSecret retrieves the cluster profile secret name using config resolver,
// and gets the secret from the ci namespace
func (s *leaseStep) importClusterProfileSecret(ctx context.Context, secretName, testName string) error {
	ciSecret := &coreapi.Secret{}
	err := s.kubeClient.Get(ctx, ctrlruntimeclient.ObjectKey{Namespace: "ci", Name: secretName}, ciSecret)
	if err != nil {
		return fmt.Errorf("failed to get secret '%s' from ci namespace: %w", secretName, err)
	}

	newSecret := &coreapi.Secret{
		Data: ciSecret.Data,
		Type: ciSecret.Type,
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-cluster-profile", testName),
			Namespace: s.namespace(),
		},
	}

	created, err := util.UpsertImmutableSecret(ctx, s.kubeClient, newSecret)
	if err != nil {
		return fmt.Errorf("could not update secret %s: %w", newSecret.Name, err)
	}
	if created {
		logrus.Debugf("Created secret %s", newSecret.Name)
	} else {
		logrus.Debugf("Updated secret %s", newSecret.Name)
	}

	return nil
}
