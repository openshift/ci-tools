package main

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/sirupsen/logrus"

	"sigs.k8s.io/prow/pkg/config/secret"

	"github.com/openshift/ci-tools/pkg/lease"
	"github.com/openshift/ci-tools/pkg/metrics"
)

func (o *options) isLeaseClientAvailable() bool {
	return o.leaseServer != "" && o.leaseServerCredentialsFile != ""
}

func loadLeaseCredentials(leaseServerCredentialsFile string) (string, func() []byte, error) {
	if err := secret.Add(leaseServerCredentialsFile); err != nil {
		return "", nil, fmt.Errorf("failed to start secret agent on file %s: %s", leaseServerCredentialsFile, string(secret.Censor([]byte(err.Error()))))
	}
	splits := strings.Split(string(secret.GetSecret(leaseServerCredentialsFile)), ":")
	if len(splits) != 2 {
		return "", nil, fmt.Errorf("got invalid content of lease server credentials file which must be of the form '<username>:<passwrod>'")
	}
	username := splits[0]
	passwordGetter := func() []byte {
		return []byte(splits[1])
	}
	return username, passwordGetter, nil
}

// adjustLeaseAcquireTimeout increases the lease acquire timeout to match the
// test timeout when a target test has a longer timeout configured. This
// prevents jobs from failing to acquire leases when the pool is busy, even
// though the overall job timeout would allow waiting longer.
func (o *options) adjustLeaseAcquireTimeout() {
	for _, test := range o.configSpec.Tests {
		if len(o.targets.values) > 0 && !slices.Contains(o.targets.values, test.As) {
			continue
		}
		if test.Timeout != nil && test.Timeout.Duration > o.leaseAcquireTimeout {
			logrus.Infof("Adjusting lease acquire timeout from %s to %s to match test %q timeout", o.leaseAcquireTimeout, test.Timeout.Duration, test.As)
			o.leaseAcquireTimeout = test.Timeout.Duration
		}
	}
}

func (o *options) initializeLeaseClient() error {
	var err error
	owner := o.namespace + "-" + o.jobSpec.UniqueHash()
	username, passwordGetter, err := loadLeaseCredentials(o.leaseServerCredentialsFile)
	if err != nil {
		return fmt.Errorf("failed to load lease credentials: %w", err)
	}

	o.metricsAgent.Record(metrics.NewInsightsEvent(metrics.InsightLeaseCredentials, metrics.Context{"lease_server": o.leaseServer, "username": username}))

	if o.leaseClient, err = lease.NewClient(owner, o.leaseServer, username, passwordGetter, 60, o.leaseAcquireTimeout); err != nil {
		return fmt.Errorf("failed to create the lease client: %w", err)
	}
	return nil
}

func (o *options) startLeaseHearthbeating() chan struct{} {
	stopChan := make(chan struct{})
	t := time.NewTicker(30 * time.Second)
	go func() {
	loop:
		for {
			select {
			case <-t.C:
				if err := o.leaseClient.Heartbeat(); err != nil {
					logrus.WithError(err).Warn("Failed to update leases.")
				}
			case <-stopChan:
				t.Stop()
				break loop
			}
		}
	}()
	return stopChan
}
