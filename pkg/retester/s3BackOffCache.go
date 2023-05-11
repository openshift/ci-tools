package retester

import (
	"bytes"
	"fmt"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/sirupsen/logrus"
	"gopkg.in/yaml.v2"
	"io"
	"k8s.io/test-infra/prow/tide"
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

	awsSession *session.Session
}

func (b *s3BackOffCache) load() error {
	return b.loadFromAwsNow(time.Now())
}

// loadFromAwsNow gets the backoff cache file from AWS S3 bucket and marshals its content into the s3BackOffCache
func (b *s3BackOffCache) loadFromAwsNow(now time.Time) error {
	if b.file == "" {
		return nil
	}

	sess, err := session.NewSession(&aws.Config{
		Region: aws.String("us-east-1"),
	})
	if err != nil {
		return fmt.Errorf("couldn't create new aws session")
	}
	b.awsSession = sess

	_, err = sess.Config.Credentials.Get()
	if err != nil {
		return fmt.Errorf("credentials for aws not found")
	}

	svc := s3.New(b.awsSession)
	result, err := svc.GetObject(&s3.GetObjectInput{
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

	cache := map[string]*pullRequest{}
	if err := yaml.Unmarshal(content, &cache); err != nil {
		return fmt.Errorf("failed to unmarshal %s: %w", b.file, err)
	}
	b.cache = cache
	b.deleteOldRecords(&cache, now)
	return nil
}

// deleteOldRecords deletes old records from cache
func (b *s3BackOffCache) deleteOldRecords(cache *map[string]*pullRequest, time time.Time) {
	for key, pr := range *cache {
		if age := time.Sub(pr.LastConsideredTime.Time); age > b.cacheRecordAge {
			b.logger.WithField("key", key).WithField("LastConsideredTime", pr.LastConsideredTime).
				WithField("age", age).Info("deleting old record from cache")
			delete(*cache, key)
		}
	}
}

// save uploads the contents of s3BackOffCache to the retester AWS S3 bucket
func (b *s3BackOffCache) save() error {
	content, err := yaml.Marshal(b.cache)
	if err != nil {
		return fmt.Errorf("failed to marshal: %w", err)
	}

	svc := s3.New(b.awsSession)
	_, err = svc.PutObject(&s3.PutObjectInput{
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
