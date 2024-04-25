package retester

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/sirupsen/logrus"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
	b.logger.WithField("backOffCache", "fileBackoffCache").Info("Loading the cache file ...")
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
	bytes, err := os.ReadFile(b.file)
	if err != nil {
		return fmt.Errorf("failed to read file %s: %w", b.file, err)
	}
	cache, err := loadAndDelete(bytes, b.logger, now, b.cacheRecordAge)
	if err != nil {
		return err
	}
	b.cache = cache
	return nil
}

// loadAndDelete loads content into cache and deletes old records from cache
func loadAndDelete(content []byte, logger *logrus.Entry, now time.Time, cacheRecordAge time.Duration) (map[string]*pullRequest, error) {
	cache := map[string]*pullRequest{}
	if err := yaml.Unmarshal(content, &cache); err != nil {
		return nil, fmt.Errorf("failed to unmarshal: %w", err)
	}
	for key, pr := range cache {
		if age := now.Sub(pr.LastConsideredTime.Time); age > cacheRecordAge {
			logger.WithField("key", key).WithField("LastConsideredTime", pr.LastConsideredTime).
				WithField("age", age).Info("deleting old record from cache")
			delete(cache, key)
		}
	}
	return cache, nil
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
	tmpFile, err := os.CreateTemp(filepath.Dir(b.file), "tmp-backoff-cache")
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

	if err := os.WriteFile(tmp, bytes, 0644); err != nil {
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

// check updates the cache and returns a retestBackoffAction according to baseSha, policy, and number of retests performed for the PR.
func check(cache *map[string]*pullRequest, pr tide.PullRequest, baseSha string, policy RetesterPolicy) (retestBackoffAction, string) {
	key := prKey(&pr)
	if _, has := (*cache)[key]; !has {
		(*cache)[key] = &pullRequest{}
	}
	record := (*cache)[key]
	record.LastConsideredTime = metav1.Now()
	if currentPRSha := string(pr.HeadRefOID); record.PRSha != currentPRSha {
		record.PRSha = currentPRSha
		record.RetestsForPrSha = 0
		record.RetestsForBaseSha = 0
	}
	if record.BaseSha != baseSha {
		record.BaseSha = baseSha
		record.RetestsForBaseSha = 0
	}

	if record.RetestsForPrSha == policy.MaxRetestsForSha {
		record.RetestsForPrSha = 0
		record.RetestsForBaseSha = 0
		return retestBackoffHold, fmt.Sprintf("Revision %s was retested %d times: holding", record.PRSha, policy.MaxRetestsForSha)
	}

	if record.RetestsForBaseSha == policy.MaxRetestsForShaAndBase {
		return retestBackoffPause, fmt.Sprintf("Revision %s was retested %d times against base HEAD %s: pausing", record.PRSha, policy.MaxRetestsForShaAndBase, record.BaseSha)
	}

	record.RetestsForBaseSha++
	record.RetestsForPrSha++

	return retestBackoffRetest, fmt.Sprintf("Remaining retests: %d against base HEAD %s and %d for PR HEAD %s in total", policy.MaxRetestsForShaAndBase-record.RetestsForBaseSha, record.BaseSha, policy.MaxRetestsForSha-record.RetestsForPrSha, record.PRSha)
}
