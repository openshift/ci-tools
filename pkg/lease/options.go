package lease

import (
	"flag"
	"fmt"
	"strings"
	"time"

	"github.com/sirupsen/logrus"

	"k8s.io/test-infra/prow/config/secret"

	"github.com/openshift/ci-tools/pkg/api"
)

const (
	leaseAcquireTimeout = 120 * time.Minute
)

var (
	// leaseServerAddress is the default lease server in app.ci
	leaseServerAddress = api.URLForService(api.ServiceBoskos)
)

type Options struct {
	leaseServer                string
	leaseServerCredentialsFile string
	leaseAcquireTimeout        time.Duration
	leaseClient                Client
}

func (o *Options) Bind(fs *flag.FlagSet) {
	flag.StringVar(&o.leaseServer, "lease-server", leaseServerAddress, "Address of the server that manages leases. Required if any test is configured to acquire a lease.")
	flag.StringVar(&o.leaseServerCredentialsFile, "lease-server-credentials-file", "", "The path to credentials file used to access the lease server. The content is of the form <username>:<password>.")
	flag.DurationVar(&o.leaseAcquireTimeout, "lease-acquire-timeout", leaseAcquireTimeout, "Maximum amount of time to wait for lease acquisition")
}

func (o *Options) loadLeaseCredentials() (string, func() []byte, error) {
	if err := secret.Add(o.leaseServerCredentialsFile); err != nil {
		return "", nil, fmt.Errorf("failed to start secret agent on file %s: %s", o.leaseServerCredentialsFile, string(secret.Censor([]byte(err.Error()))))
	}
	splits := strings.Split(string(secret.GetSecret(o.leaseServerCredentialsFile)), ":")
	if len(splits) != 2 {
		return "", nil, fmt.Errorf("got invalid content of lease server credentials file which must be of the form '<username>:<passwrod>'")
	}
	username := splits[0]
	passwordGetter := func() []byte {
		return []byte(splits[1])
	}
	return username, passwordGetter, nil
}

func (o *Options) InitializeLeaseClient(namespace, jobSpecUniqueHash string) error {
	if o.leaseServer == "" && o.leaseServerCredentialsFile == "" {
		return nil
	}

	var err error
	owner := namespace + "-" + jobSpecUniqueHash
	username, passwordGetter, err := o.loadLeaseCredentials()
	if err != nil {
		return fmt.Errorf("failed to load lease credentials: %w", err)
	}
	if o.leaseClient, err = NewClient(owner, o.leaseServer, username, passwordGetter, 60, o.leaseAcquireTimeout); err != nil {
		return fmt.Errorf("failed to create the lease client: %w", err)
	}
	t := time.NewTicker(30 * time.Second)
	go func() {
		for range t.C {
			if err := o.leaseClient.Heartbeat(); err != nil {
				logrus.WithError(err).Warn("Failed to update leases.")
			}
		}
		if l, err := o.leaseClient.ReleaseAll(); err != nil {
			logrus.WithError(err).Errorf("Failed to release leaked leases (%v)", l)
		} else if len(l) != 0 {
			logrus.Warnf("Would leak leases: %v", l)
		}
	}()
	return nil
}

func (o *Options) LeaseClient() *Client {
	return &o.leaseClient
}
