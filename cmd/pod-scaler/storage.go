package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"time"

	"cloud.google.com/go/storage"
	"github.com/sirupsen/logrus"

	kerrors "k8s.io/apimachinery/pkg/util/errors"
	"sigs.k8s.io/prow/pkg/interrupts"

	pod_scaler "github.com/openshift/ci-tools/pkg/pod-scaler"
)

// cache closes over how we interact with cached data
type cache interface {
	loader
	storer
	attributeResolver
}

// loader closes over how we load cached data
type loader interface {
	load(ctx context.Context, name string) (io.ReadCloser, error)
}

// storer closes over how we store cached data
type storer interface {
	store(ctx context.Context, name string) (io.WriteCloser, error)
}

// attributeResolver closes over how we store cached data
type attributeResolver interface {
	lastUpdated(ctx context.Context, name string) (time.Time, error)
}

type bucketCache struct {
	bucket *storage.BucketHandle
}

var _ cache = &bucketCache{}

func (b *bucketCache) load(ctx context.Context, name string) (io.ReadCloser, error) {
	handle := b.bucket.Object(name)
	rc, err := handle.NewReader(ctx)
	if errors.Is(err, storage.ErrObjectNotExist) {
		err = notExist{wrapped: err}
	}
	return rc, err
}

func (b *bucketCache) store(ctx context.Context, name string) (io.WriteCloser, error) {
	handle := b.bucket.Object(name)
	return handle.NewWriter(ctx), nil
}

func (b *bucketCache) lastUpdated(ctx context.Context, name string) (time.Time, error) {
	handle := b.bucket.Object(name)
	attrs, err := handle.Attrs(ctx)
	if err != nil {
		return time.Time{}, fmt.Errorf("could not query cache for attributes: %w", err)
	}
	return attrs.Updated, nil
}

type localCache struct {
	dir string
}

var _ cache = &localCache{}

func (l *localCache) load(_ context.Context, name string) (io.ReadCloser, error) {
	rc, err := os.Open(path.Join(l.dir, name))
	if os.IsNotExist(err) {
		err = notExist{wrapped: err}
	}
	return rc, err
}

func (l *localCache) store(_ context.Context, name string) (io.WriteCloser, error) {
	cachePath := path.Join(l.dir, name)
	if err := os.MkdirAll(filepath.Dir(cachePath), 0777); err != nil {
		return nil, err
	}
	return os.Create(cachePath)
}

func (l *localCache) lastUpdated(_ context.Context, name string) (time.Time, error) {
	info, err := os.Stat(path.Join(l.dir, name))
	if err != nil {
		return time.Time{}, fmt.Errorf("could not query cache for attributes: %w", err)
	}
	return info.ModTime(), nil
}

// notExist closes over the different ways in which storage libraries may expose a nonexistent file
type notExist struct {
	wrapped error
}

func (e notExist) Error() string {
	return e.wrapped.Error()
}

func (e notExist) Is(err error) bool {
	_, ok := err.(notExist)
	return ok // we don't care what we're wrapping, all notExist are equivalent
}

func (e notExist) Unwrap() error {
	return e.wrapped
}

// loadCache loads cached query data from the given storage loader.
func loadCache(loader loader, metricName string, logger *logrus.Entry) (*pod_scaler.CachedQuery, error) {
	readStart := time.Now()
	logger.Info("Reading Prometheus data from cache.")
	logger.Debug("Loading Prometheus data from storage.")
	var data []byte
	for i := 0; i < 5; i++ {
		var readErr error
		data, readErr = loadFrom(loader, metricName)
		if errors.Is(readErr, context.DeadlineExceeded) {
			logger.Debug("Failed to load data before deadline, trying again.")
			continue
		}
		if readErr != nil {
			return nil, fmt.Errorf("could not read cached data: %w", readErr)
		}
		break
	}
	logger.Debugf("Read Prometheus data from storage after %s.", time.Since(readStart).Round(time.Second))
	var cache pod_scaler.CachedQuery
	if err := json.Unmarshal(data, &cache); err != nil {
		return nil, fmt.Errorf("could not unmarshal cached data: %w", err)
	}
	logger.Infof("Loaded %d distributions for %d identifiers after %s.", len(cache.Data), len(cache.DataByMetaData), time.Since(readStart).Round(time.Second))
	return &cache, nil
}

func loadFrom(loader loader, metricName string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(interrupts.Context(), 15*time.Minute)
	defer func() { cancel() }()
	reader, err := loader.load(ctx, metricName+".json")
	if err != nil && !errors.Is(err, context.DeadlineExceeded) {
		return nil, err
	}
	data, readErr := io.ReadAll(reader)
	if err := reader.Close(); err != nil {
		readErr = kerrors.NewAggregate([]error{readErr, fmt.Errorf("could not close reader for cached data: %w", err)})
	}
	return data, readErr
}

// storeCache prunes and stores cached query data to the given storage storer.
func storeCache(storer storer, metricName string, data *pod_scaler.CachedQuery, logger *logrus.Entry) error {
	pruneStart := time.Now()
	logger.Debug("Pruning cached Prometheus data.")
	data.Prune()
	logger.Debugf("Pruned cached Prometheus data after %s.", time.Since(pruneStart).Round(time.Second))

	flushStart := time.Now()
	logger.Info("Flushing Prometheus data to cache.")
	raw, err := json.Marshal(&data)
	if err != nil {
		return fmt.Errorf("could not marshal cached data: %w", err)
	}
	for i := 0; i < 5; i++ {
		storeErr := storeTo(storer, metricName, raw)
		if errors.Is(storeErr, context.DeadlineExceeded) {
			logger.Debug("Failed to store data before deadline, trying again.")
			continue
		}
		if storeErr != nil {
			return fmt.Errorf("could not write cached data: %w", storeErr)
		}
		break
	}
	logger.Infof("Flushed Prometheus data to cache after %s.", time.Since(flushStart).Round(time.Second))
	return nil
}

func storeTo(storer storer, metricName string, data []byte) error {
	ctx, cancel := context.WithTimeout(interrupts.Context(), 30*time.Minute)
	defer func() { cancel() }()
	writer, err := storer.store(ctx, metricName+".json")
	if err != nil {
		return fmt.Errorf("could open cache for writing: %w", err)
	}
	var errs []error
	if _, err := writer.Write(data); err != nil {
		errs = append(errs, fmt.Errorf("could not write cached data: %w", err))
	}
	if err := writer.Close(); err != nil {
		errs = append(errs, fmt.Errorf("could not close writer for cached data: %w", err))
	}
	return kerrors.NewAggregate(errs)
}

// lastUpdated determines the time at which the cache for this metric was last updated
func lastUpdated(resolver attributeResolver, metricName string) (time.Time, error) {
	return resolver.lastUpdated(interrupts.Context(), metricName+".json")
}
