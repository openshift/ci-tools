package retester

import (
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"time"

	"github.com/sirupsen/logrus"

	"k8s.io/test-infra/prow/tide"
	"sigs.k8s.io/yaml"
)

type fileBackoffCache struct {
	cache          map[string]*pullRequest
	file           string
	cacheRecordAge time.Duration
	logger         *logrus.Entry
}

func (b *fileBackoffCache) load() error {
	return b.loadFromDiskNow(time.Now())
}

func (b *fileBackoffCache) loadFromDiskNow(now time.Time) error {
	if b.file == "" {
		return nil
	}
	if _, err := os.Stat(b.file); errors.Is(err, os.ErrNotExist) {
		b.logger.WithField("file", b.file).Info("cache file does not exit")
		return nil
	}
	bytes, err := ioutil.ReadFile(b.file)
	if err != nil {
		return fmt.Errorf("failed to read file %s: %w", b.file, err)
	}
	cache := map[string]*pullRequest{}
	if err := yaml.Unmarshal(bytes, &cache); err != nil {
		return fmt.Errorf("failed to unmarshal: %w", err)
	}
	for key, pr := range cache {
		if age := now.Sub(pr.LastConsideredTime.Time); age > b.cacheRecordAge {
			b.logger.WithField("key", key).WithField("LastConsideredTime", pr.LastConsideredTime).
				WithField("age", age).Info("deleting old record from cache")
			delete(cache, key)
		}
	}
	b.cache = cache
	return nil
}

func (b *fileBackoffCache) save() (ret error) {
	if b.file == "" {
		return nil
	}
	bytes, err := yaml.Marshal(b.cache)
	if err != nil {
		return fmt.Errorf("failed to marshal: %w", err)
	}
	// write to a temp file and rename it to the cache file to ensure "atomic write":
	// either it is complete or nothing
	tmpFile, err := ioutil.TempFile(filepath.Dir(b.file), "tmp-backoff-cache")
	if err != nil {
		return fmt.Errorf("failed to create a temp file: %w", err)
	}
	tmp := tmpFile.Name()
	defer func() {
		// do nothing when the file does not exist, e.g., write failed, or it has been renamed.
		if _, err := os.Stat(tmp); errors.Is(err, os.ErrNotExist) {
			return
		}
		if err := os.Remove(tmp); err != nil {
			ret = fmt.Errorf("failed to delete file %s: %w", tmp, err)
		}
	}()

	if err := ioutil.WriteFile(tmp, bytes, 0644); err != nil {
		return fmt.Errorf("failed to write file %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, b.file); err != nil {
		return fmt.Errorf("failed to rename file from %s to %s: %w", tmp, b.file, err)
	}
	return ret
}

func (b *fileBackoffCache) check(pr tide.PullRequest, baseSha string, policy RetesterPolicy) (retestBackoffAction, string) {
	return check(&b.cache, pr, baseSha, policy)
}
