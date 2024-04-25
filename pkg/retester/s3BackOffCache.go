package retester

import (
	"bytes"
	"fmt"
	"io"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/sirupsen/logrus"

	"k8s.io/test-infra/prow/tide"
	"sigs.k8s.io/yaml"
)

const (
	retesterBucket = "prow-retester"
)

type s3Client interface {
	PutObject(input *s3.PutObjectInput) (*s3.PutObjectOutput, error)
	GetObject(input *s3.GetObjectInput) (*s3.GetObjectOutput, error)
}

type s3BackOffCache struct {
	cache          map[string]*pullRequest
	file           string
	cacheRecordAge time.Duration
	logger         *logrus.Entry

	awsClient s3Client
}

func (b *s3BackOffCache) load() error {
	b.logger.WithField("backOffCache", "s3BackOffCache").Info("Loading the cache file ...")
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
		if awsErr, ok := err.(awserr.Error); ok && awsErr.Code() == s3.ErrCodeNoSuchKey {
			b.logger.WithField("file", b.file).Info("file doesn't exist in the s3 bucket")
			return nil
		}
		return fmt.Errorf("error getting %s file from aws s3 bucket %s: %w", b.file, retesterBucket, err)
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
