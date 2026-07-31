package main

import (
	"context"
	"errors"
	"fmt"
	"net/url"

	"github.com/sirupsen/logrus"
	"gopkg.in/fsnotify.v1"

	aggerrs "k8s.io/apimachinery/pkg/util/errors"

	"github.com/openshift/ci-tools/pkg/load/agents"
)

// URL enhances url.URL by adding the unmarshal capability.
type URL struct {
	*url.URL
}

// UnmarshalText implements [encoding.UnmarshalText].
func (u *URL) UnmarshalText(text []byte) error {
	parsed, err := url.Parse(string(text))
	if err != nil {
		return err
	}
	u.URL = parsed
	return nil
}

type Config struct {
	Routes       []Route `yaml:"routes,omitempty"`
	DefaultRoute *Route  `yaml:"default_route,omitempty"`
}

func (c *Config) validate() error {
	var errs []error

	if c.DefaultRoute == nil {
		errs = append(errs, errors.New("default route is not defined"))
	} else if c.DefaultRoute.Target == nil {
		errs = append(errs, errors.New("default route target URL is not defined"))
	}

	for i, route := range c.Routes {
		if route.Target == nil {
			errs = append(errs, fmt.Errorf("route[%d]: target is nil", i))
		}
	}

	return aggerrs.NewAggregate(errs)
}

type Route struct {
	Target  *URL    `yaml:"target,omitempty"`
	Matches []Match `yaml:"matches,omitempty"`
}

type Match struct {
	Org   string   `yaml:"org,omitempty"`
	Repos []string `yaml:"repos,omitempty"`
}

func watchConfig(ctx context.Context, log *logrus.Entry, path string, onNewConfig func() error) error {
	log = log.WithField("config_path", path)

	events := make(chan fsnotify.Event)
	errs := make(chan error)
	w := agents.UniversalSymlinkWatcher{
		EventCh:   events,
		ErrCh:     errs,
		WatchPath: path,
	}

	startWatching, err := w.GetWatcher()
	if err != nil {
		return fmt.Errorf("get watcher: %w", err)
	}

	go func() {
		for {
			select {
			case _, ok := <-events:
				if !ok {
					log.Warn("Watch config: events channel closed")
					return
				}
				if err := onNewConfig(); err != nil {
					log.WithError(err).Error("Failed to reload the configuration")
				} else {
					log.Info("Configuration reloaded")
				}
			case err, ok := <-errs:
				if !ok {
					log.Warn("Watch config: errors channel closed")
					return
				}
				log.WithError(err).Error("Failed to watch the configuration file")
			case <-ctx.Done():
				log.WithError(context.Cause(ctx)).Info("Configuration watcher stopped")
				return
			}
		}
	}()

	go startWatching(ctx)

	return nil
}
