package watcher

import (
	"context"
	"fmt"
	"io/ioutil"
	"path/filepath"

	"github.com/fsnotify/fsnotify"
	"github.com/sirupsen/logrus"
)

// GetWatchCMMount returns a function that watches the provided path mounted from a
// configmap and runs the eventFunc function whenever the configmap is updated
// until the provided context is cancelled. If an error occurs, errFunc is run.
// An error is returned if a watcher could not be created or set to watch the
// provided path or if the parovided path is not a configmap mount.
func GetWatchCMMount(path string, eventFunc func() error, errFunc func(error, string)) (func(ctx context.Context), error) {
	// Verify that this is the root of a configmap mount
	files, err := ioutil.ReadDir(path)
	if err != nil {
		return nil, fmt.Errorf("Could not read provided directory %s: %v", path, err)
	}
	isCMMount := false
	for _, file := range files {
		if file.Name() == "..data" {
			isCMMount = true
		}
	}
	if !isCMMount {
		return nil, fmt.Errorf("Provided directory %s is not a configmap directory", path)
	}
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	err = w.Add(path)
	if err != nil {
		return nil, err
	}
	logrus.Debugf("Watching %s", path)
	dataPath := filepath.Join(path, "..data")
	return func(ctx context.Context) {
		for {
			select {
			case <-ctx.Done():
				if err := w.Close(); err != nil {
					errFunc(err, "failed to close fsnotify watcher")
				}
				return
			case event := <-w.Events:
				if event.Name == dataPath && event.Op == fsnotify.Create {
					err := eventFunc()
					if err != nil {
						errFunc(err, "coalescer function failed")
					}
				}
			case err := <-w.Errors:
				errFunc(err, "received fsnotify error")
			}
		}
	}, nil
}
