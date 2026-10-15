package gsmproxy

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/sirupsen/logrus"
	"gopkg.in/fsnotify.v1"

	prowconfig "sigs.k8s.io/prow/pkg/config"

	"github.com/openshift/ci-tools/pkg/group"
)

// NewReloadingCollectionAuthorizer loads secret collection ownership config
// and watches for updates. Load failures preserve the last valid snapshot.
func NewReloadingCollectionAuthorizer(ctx context.Context, configPath string, log *logrus.Entry) (*CollectionAuthorizer, error) {
	configPath, err := filepath.Abs(configPath) //eg ownership.yaml -> /some/directory/ownership.yaml
	if err != nil {
		return nil, fmt.Errorf("failed to resolve ownership configuration path: %w", err)
	}
	authorizer := NewCollectionAuthorizer(nil)

	reload := func() error {
		config, err := group.LoadConfig(configPath)
		if err != nil {
			return errors.New("failed to load ownership configuration")
		}
		authorizer.config.Store(config)
		log.WithField("groups", len(config.Groups)).Info("loaded ownership configuration")
		return nil
	}

	updates := make(chan struct{}, 1)
	// nolint:unparam
	onChange := func() error {
		select {
		case updates <- struct{}{}:
		default:
		}
		return nil
	}
	watchError := func(err error, msg string) {
		log.WithError(err).
			WithField("watcher_message", msg).
			Error("ownership configuration watcher failed; keeping last valid configuration")
	}
	directory := filepath.Dir(configPath)
	isConfigMapMount, err := prowconfig.IsConfigMapMount(directory)
	if err != nil {
		return nil, fmt.Errorf("failed to inspect ownership configuration directory: %w", err)
	}

	var watchConfigFunc func(context.Context)
	if isConfigMapMount {
		watchConfigFunc, err = prowconfig.GetCMMountWatcher(onChange, watchError, directory)
	} else {
		watchConfigFunc, err = prowconfig.GetFileWatcher(func(_ *fsnotify.Watcher) error {
			return onChange()
		}, watchError, directory)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to watch ownership configuration: %w", err)
	}

	// Register the watch before loading to avoid missing an update during startup.
	watchCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer cancel()
		watchConfigFunc(watchCtx)
	}()
	err = reload()
	if err != nil {
		cancel()
		<-done
		return nil, err
	}
	go func() {
		for {
			select {
			case <-watchCtx.Done():
				return
			case <-updates:
				if err := reload(); err != nil {
					log.Error("ownership configuration reload failed; keeping last valid configuration")
				}
			}
		}
	}()
	return authorizer, nil
}
