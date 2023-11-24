package agents

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/sirupsen/logrus"
	"gopkg.in/fsnotify.v1"

	"k8s.io/test-infra/prow/config"
	"k8s.io/test-infra/prow/interrupts"
)

func startWatchers(path string, errCh chan<- error, callback func() error, metric *prometheus.CounterVec, universalSymlinkWatcher *UniversalSymlinkWatcher) error {
	var watchers []func(context.Context)

	if universalSymlinkWatcher != nil {
		watcher := func(ctx context.Context) {
			errFunc := func(err error, msg string) {
				recordErrorForMetric(metric, msg)
				logrus.WithError(err).Error(msg)
				errCh <- err
			}

			for {
				select {
				case <-ctx.Done():
					return
				case event := <-universalSymlinkWatcher.EventCh:
					logrus.Infof("Received event: %s", event.String())
					if err := universalSymlinkWatcher.ConfigEventFn(); err != nil {
						errFunc(err, "failed to load config")
					}
					if err := universalSymlinkWatcher.RegistryEventFn(); err != nil {
						errFunc(err, "failed to load registry")
					}

				case err := <-universalSymlinkWatcher.ErrCh:
					errFunc(err, "received error")
				}
			}
		}
		watchers = append(watchers, watcher)
	} else {
		cms, dirs, err := config.ListCMsAndDirs(path)
		if err != nil {
			return err
		}
		errFunc := func(err error, msg string) {
			recordErrorForMetric(metric, msg)
			logrus.WithError(err).Error(msg)
		}

		for cm := range cms {
			watcher, err := config.GetCMMountWatcher(callback, errFunc, cm)
			if err != nil {
				return err
			}
			watchers = append(watchers, watcher)
		}
		if len(dirs) != 0 {
			eventFunc := func(w *fsnotify.Watcher) error {
				go func() {
					err := callback()
					if err != nil {
						logrus.WithError(err).Errorf("Coalescer function failed")
					}
				}()
				// add new files to be watched; if a watch already exists on a file, the
				// watch is simply updated
				_, dirs, err := config.ListCMsAndDirs(path)
				if err != nil {
					return err
				}
				for dir := range dirs {
					// Adding a file or directory that already exists in fsnotify is a no-op, so it is safe to always run Add
					if err := w.Add(dir); err != nil {
						return fmt.Errorf("failed to update watcher: %w", err)
					}
				}
				return nil
			}
			watcher, err := config.GetFileWatcher(eventFunc, errFunc, dirs.UnsortedList()...)
			if err != nil {
				return err
			}
			watchers = append(watchers, watcher)
		}
	}

	for _, watcher := range watchers {
		interrupts.Run(watcher)
	}
	return nil
}

type UniversalSymlinkWatcher struct {
	WatchPath       string
	EventCh         chan fsnotify.Event
	ErrCh           chan error
	ConfigEventFn   func() error
	RegistryEventFn func() error
}

func recordErrorForMetric(metric *prometheus.CounterVec, label string) {
	labels := prometheus.Labels{"error": label}
	metric.With(labels).Inc()
}

func (u *UniversalSymlinkWatcher) GetWatcher() (func(ctx context.Context), error) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("failed to create a new watcher: %w", err)
	}

	if err = w.Add(filepath.Dir(u.WatchPath)); err != nil {
		w.Close()
		return nil, fmt.Errorf("failed to add %s to watcher: %w", u.WatchPath, err)
	}
	return func(ctx context.Context) {
		for {
			select {
			case <-ctx.Done():
				if err := w.Close(); err != nil {
					u.ErrCh <- fmt.Errorf("failed to close fsnotify watcher for directory %s: %w", u.WatchPath, err)
				}
				return
			case event := <-w.Events:
				if event.Name != u.WatchPath {
					continue
				}
				newDestination, err := os.Readlink(event.Name)
				if err != nil {
					logrus.WithError(err).Errorf("couldn't read the destination link of %s", event.Name)
				} else {
					logrus.Infof("Loading configs from %s", newDestination)
					logrus.Info("Sending event to channel")
					u.EventCh <- event
				}
			case err := <-w.Errors:
				u.ErrCh <- fmt.Errorf("received fsnotify error %w", err)
			}
		}
	}, nil
}
