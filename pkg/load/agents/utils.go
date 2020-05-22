package agents

import (
	"context"
	"fmt"

	"github.com/sirupsen/logrus"
	log "github.com/sirupsen/logrus"
	"gopkg.in/fsnotify.v1"
	"k8s.io/test-infra/prow/config"
	"k8s.io/test-infra/prow/interrupts"

	"github.com/openshift/ci-tools/pkg/coalescer"
)

func startWatchers(path string, c coalescer.Coalescer, recordError func(string)) error {
	cms, dirs, err := config.ListCMsAndDirs(path)
	if err != nil {
		return err
	}
	errFunc := func(err error, msg string) {
		recordError(msg)
		log.WithError(err).Error(msg)
	}
	var watchers []func(context.Context)
	for cm := range cms {
		watcher, err := config.GetCMMountWatcher(c.Run, errFunc, cm)
		if err != nil {
			return err
		}
		watchers = append(watchers, watcher)
	}
	if len(dirs) != 0 {
		eventFunc := func(w *fsnotify.Watcher) error {
			go func() {
				err := c.Run()
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
					return fmt.Errorf("Failed to update watcher: %v", err)
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
	for _, watcher := range watchers {
		interrupts.Run(watcher)
	}
	return nil
}
