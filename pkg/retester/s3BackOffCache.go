package retester

import (
	"bytes"
	"fmt"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/sirupsen/logrus"
	"io"
	"k8s.io/test-infra/prow/tide"
	"sigs.k8s.io/yaml"
	"time"
)

const (
	retesterBucket = "prow-retester"
)

type s3BackOffCache struct {
	cache          map[string]*pullRequest
	file           string
	cacheRecordAge time.Duration
	logger         *logrus.Entry

	awsClient *s3.S3
}

func (b *s3BackOffCache) load() error {
	return b.loadFromAwsNow(time.Now())
}

// loadFromAwsNow gets the backoff cache file from AWS S3 bucket and marshals its content into the s3BackOffCache
func (b *s3BackOffCache) loadFromAwsNow(now time.Time) error {
	if b.file == "" {
		return nil
	}

	result, err := b.awsClient.GetObject(&s3.GetObjectInput{
		Bucket: aws.String(retesterBucket),
		Key:    aws.String(b.file),
	})
	if err != nil {
		return fmt.Errorf("couldn't get %s file from aws s3 bucket %s: %w", b.file, retesterBucket, err)
	}

	content, err := io.ReadAll(result.Body)
	if err != nil {
		return fmt.Errorf("failed to read file %s: %w", b.file, err)
	}

	cache, err := loadAndDelete(content, b.logger, now, b.cacheRecordAge)
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

// save uploads the contents of s3BackOffCache to the retester AWS S3 bucket
func (b *s3BackOffCache) save() error {
	content, err := yaml.Marshal(b.cache)
	if err != nil {
		return fmt.Errorf("failed to marshal: %w", err)
	}

	_, err = b.awsClient.PutObject(&s3.PutObjectInput{
		Bucket: aws.String(retesterBucket),
		Key:    aws.String(b.file),
		Body:   bytes.NewReader(content),
	})
	if err != nil {
		return fmt.Errorf("failed to upload file %s into %s bucket: %w", b.file, retesterBucket, err)
	}

	return nil
}

func (b *s3BackOffCache) check(pr tide.PullRequest, baseSha string, policy RetesterPolicy) (retestBackoffAction, string) {
	return check(&b.cache, pr, baseSha, policy)
}