package agents

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/fsnotify/fsnotify"
	"github.com/sirupsen/logrus"
	log "github.com/sirupsen/logrus"
	"k8s.io/test-infra/prow/interrupts"

	"github.com/openshift/ci-tools/pkg/coalescer"
	"github.com/openshift/ci-tools/pkg/watcher"
)

func populateWatcher(watcher *fsnotify.Watcher, root string) error {
	return filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		// We only need to watch directories as creation, deletion, and writes
		// for files in a directory trigger events for the directory
		if info != nil && info.IsDir() {
			log.Tracef("Adding %s to watch list", path)
			err = watcher.Add(path)
			if err != nil {
				return fmt.Errorf("Failed to add watch on directory %s: %v", path, err)
			}
		}
		return nil
	})
}

// containsCMMount checks if there are any configmap mounted directories in the provided path
func containsCMMount(root string) (bool, error) {
	cmMount := false
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if info != nil && strings.HasPrefix(info.Name(), "..") {
			cmMount = true
			return filepath.SkipDir
		}
		return nil
	})
	return cmMount, err
}

func watchCMs(root string, eventFunc func() error, errFunc func(error, string)) error {
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if info != nil && strings.HasPrefix(info.Name(), "..") {
			reloaderFunc, err := watcher.GetWatchCMMount(filepath.Dir(path), eventFunc, errFunc)
			if err != nil {
				return fmt.Errorf("Failed to get configmap watcher: %v", err)
			}
			interrupts.Run(reloaderFunc)
			// now that the directory is being watched, the other files don't matter
			return filepath.SkipDir
		}
		return nil
	})
	return err
}

func reloadWatcher(ctx context.Context, w *fsnotify.Watcher, root string, errFunc func(string), c coalescer.Coalescer) {
	for {
		select {
		case <-ctx.Done():
			if err := w.Close(); err != nil {
				log.WithError(err).Error("Failed to close fsnotify watcher")
			}
			return
		case event := <-w.Events:
			log.Tracef("Received %v event for %s", event.Op, event.Name)
			go func() {
				err := c.Run()
				if err != nil {
					logrus.WithError(err).Errorf("Coalescer function failed")
				}
			}()
			// add new files to be watched; if a watch already exists on a file, the
			// watch is simply updated
			if err := populateWatcher(w, root); err != nil {
				errFunc("failed to update watcher")
				log.WithError(err).Error("Failed to update fsnotify watchlist")
			}
		case err := <-w.Errors:
			errFunc("received fsnotify error")
			log.WithError(err).Errorf("Received fsnotify error")
		}
	}
}

func startWatchers(path string, c coalescer.Coalescer, recordError func(string)) error {
	cmMount, err := containsCMMount(path)
	if err != nil {
		return fmt.Errorf("Failed to walk directory %s: %v", path, err)
	}
	if cmMount {
		errFunc := func(err error, msg string) {
			recordError(msg)
			log.WithError(err).Error(msg)
		}
		err := watchCMs(path, c.Run, errFunc)
		if err != nil {
			return fmt.Errorf("Failed to watch configmaps: %v", err)
		}
	} else {
		// fsnotify reload
		configWatcher, err := fsnotify.NewWatcher()
		if err != nil {
			return fmt.Errorf("Failed to create new watcher: %v", err)
		}
		err = populateWatcher(configWatcher, path)
		if err != nil {
			return fmt.Errorf("Failed to populate watcher: %v", err)
		}
		interrupts.Run(func(ctx context.Context) {
			reloadWatcher(ctx, configWatcher, path, recordError, c)
		})
	}
	return nil
}
