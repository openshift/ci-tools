package lease

import (
	"context"
	"fmt"
	"sync"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"

	boskos "k8s.io/test-infra/boskos/client"
	"k8s.io/test-infra/boskos/common"
)

const (
	freeState   = "free"
	leasedState = "leased"
)

type boskosClient interface {
	Acquire(rtype, state, dest string) (*common.Resource, error)
	UpdateOne(name, dest string, _ *common.UserData) error
	ReleaseOne(name, dest string) error
	ReleaseAll(dest string) error
}

// Client manages resource leases, acquiring, releasing, and keeping them
// updated.
type Client interface {
	// Acquire leases a resource and returns the lease name.
	// `cancel` is called if any subsequent updates to the lease fail.
	Acquire(rtype string, cancel context.CancelFunc) (string, error)
	// Heartbeat updates all leases. It calls the cancellation function of each
	// lease it fails to update.
	Heartbeat() error
	// Release ends one lease by name.
	Release(name string) error
	// ReleaseAll ends all leases and returns the names of those that were
	// successfully released.
	ReleaseAll() ([]string, error)
}

// NewClient creates a client that leases resources with the specified owner.
func NewClient(owner, url, username, passwordFile string) (Client, error) {
	c, err := boskos.NewClient(owner, url, username, passwordFile)
	if err != nil {
		return nil, err
	}
	return newClient(c), nil
}

func newClient(boskos boskosClient) Client {
	return &client{
		boskos: boskos,
		cancel: make(map[string]context.CancelFunc),
	}
}

type client struct {
	sync.RWMutex
	boskos boskosClient
	// cancel holds cancellation functions for steps that depend on leases
	// being active; we must cancel these when we encounter errors to tie the
	// lifetime of the downstream user routines to those of the leases they
	// require
	cancel map[string]context.CancelFunc
}

func (c *client) Acquire(rtype string, cancel context.CancelFunc) (string, error) {
	r, err := c.boskos.Acquire(rtype, freeState, leasedState)
	if err != nil {
		return "", err
	}
	c.Lock()
	c.cancel[r.Name] = cancel
	c.Unlock()
	return r.Name, nil
}

func (c *client) Heartbeat() error {
	c.Lock()
	defer c.Unlock()
	var errs []error
	for name, cancel := range c.cancel {
		if err := c.boskos.UpdateOne(name, leasedState, nil); err != nil {
			errs = append(errs, fmt.Errorf("failed to update lease %q: %v", name, err))
			cancel()
			delete(c.cancel, name)
		}
	}
	return utilerrors.NewAggregate(errs)
}

func (c *client) Release(name string) error {
	c.Lock()
	defer c.Unlock()
	if err := c.boskos.ReleaseOne(name, freeState); err != nil {
		return err
	}
	delete(c.cancel, name)
	return nil
}

func (c *client) ReleaseAll() ([]string, error) {
	c.Lock()
	defer c.Unlock()
	var ret []string
	var errs []error
	for l := range c.cancel {
		ret = append(ret, l)
		if err := c.boskos.ReleaseOne(l, freeState); err != nil {
			errs = append(errs, err)
			continue
		}
		delete(c.cancel, l)
	}
	return ret, utilerrors.NewAggregate(errs)
}
