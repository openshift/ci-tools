package load

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/fsnotify/fsnotify"
	"github.com/sirupsen/logrus"
	log "github.com/sirupsen/logrus"

	"github.com/openshift/ci-tools/pkg/coalescer"
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
