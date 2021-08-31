/*
Copyright 2018 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Package reporter implements a reporter interface for github
// TODO(krzyzacy): move logic from report.go here
package github

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	"golang.org/x/sync/semaphore"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	v1 "k8s.io/test-infra/prow/apis/prowjobs/v1"
	"k8s.io/test-infra/prow/config"
	"k8s.io/test-infra/prow/gerrit/client"
	"k8s.io/test-infra/prow/github/report"
)

const (
	// GitHubReporterName is the name for github reporter
	GitHubReporterName = "github-reporter"
)

// Client is a github reporter client
type Client struct {
	gc          report.GitHubClient
	config      config.Getter
	reportAgent v1.ProwJobAgent
	prLocks     *shardedLock
}

type simplePull struct {
	org, repo string
	number    int
}

type shardedLock struct {
	mapLock *semaphore.Weighted
	locks   map[simplePull]*semaphore.Weighted
}

func (s *shardedLock) getLock(ctx context.Context, key simplePull) (*semaphore.Weighted, error) {
	if err := s.mapLock.Acquire(ctx, 1); err != nil {
		return nil, err
	}
	defer s.mapLock.Release(1)
	if _, exists := s.locks[key]; !exists {
		s.locks[key] = semaphore.NewWeighted(1)
	}
	return s.locks[key], nil
}

// cleanup deletes all locks by acquiring first
// the mapLock and then each individual lock before
// deleting it. The individual lock must be acquired
// because otherwise it may be held, we delete it from
// the map, it gets recreated and acquired and two
// routines report in parallel for the same job.
// Note that while this function is running, no new
// presubmit reporting can happen, as we hold the mapLock.
func (s *shardedLock) cleanup() {
	ctx := context.Background()
	s.mapLock.Acquire(ctx, 1)
	defer s.mapLock.Release(1)

	for key, lock := range s.locks {
		lock.Acquire(ctx, 1)
		delete(s.locks, key)
		lock.Release(1)
	}
}

// runCleanup asynchronously runs the cleanup once per hour.
func (s *shardedLock) runCleanup() {
	go func() {
		for range time.Tick(time.Hour) {
			logrus.Debug("Starting to clean up presubmit locks")
			startTime := time.Now()
			s.cleanup()
			logrus.WithField("duration", time.Since(startTime).String()).Debug("Finished cleaning up presubmit locks")
		}
	}()
}

// NewReporter returns a reporter client
func NewReporter(gc report.GitHubClient, cfg config.Getter, reportAgent v1.ProwJobAgent) *Client {
	c := &Client{
		gc:          gc,
		config:      cfg,
		reportAgent: reportAgent,
		prLocks: &shardedLock{
			mapLock: semaphore.NewWeighted(1),
			locks:   map[simplePull]*semaphore.Weighted{},
		},
	}
	c.prLocks.runCleanup()
	return c
}

// GetName returns the name of the reporter
func (c *Client) GetName() string {
	return GitHubReporterName
}

// ShouldReport returns if this prowjob should be reported by the github reporter
func (c *Client) ShouldReport(_ context.Context, _ *logrus.Entry, pj *v1.ProwJob) bool {
	if !pj.Spec.Report {
		return false
	}

	switch {
	case pj.Labels[client.GerritReportLabel] != "":
		return false // TODO(fejta): opt-in to github reporting
	case pj.Spec.Type != v1.PresubmitJob && pj.Spec.Type != v1.PostsubmitJob:
		return false // Report presubmit and postsubmit github jobs for github reporter
	case c.reportAgent != "" && pj.Spec.Agent != c.reportAgent:
		return false // Only report for specified agent
	}

	return true
}

// Report will report via reportlib
func (c *Client) Report(ctx context.Context, log *logrus.Entry, pj *v1.ProwJob) ([]*v1.ProwJob, *reconcile.Result, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	// The github comment create/update/delete done for presubmits
	// needs pr-level locking to avoid racing when reporting multiple
	// jobs in parallel.
	if pj.Spec.Type == v1.PresubmitJob {
		key, err := lockKeyForPJ(pj)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to get lockkey for job: %v", err)
		}
		lock, err := c.prLocks.getLock(ctx, *key)
		if err != nil {
			return nil, nil, err
		}
		if err := lock.Acquire(ctx, 1); err != nil {
			return nil, nil, err
		}
		defer lock.Release(1)
	}

	// TODO(krzyzacy): ditch ReportTemplate, and we can drop reference to config.Getter
	err := report.Report(ctx, c.gc, c.config().Plank.ReportTemplateForRepo(pj.Spec.Refs), *pj, c.config().GitHubReporter.JobTypesToReport)
	if err != nil {
		if strings.Contains(err.Error(), "This SHA and context has reached the maximum number of statuses") {
			// This is completely unrecoverable, so just swallow the error to make sure we wont retry, even when crier gets restarted.
			log.WithError(err).Debug("Encountered an error, skipping retries")
			err = nil
		} else if strings.Contains(err.Error(), "\"message\":\"Not Found\"") || strings.Contains(err.Error(), "\"message\":\"No commit found for SHA:") {
			// "message":"Not Found" error occurs when someone force push, which is not a crier error
			log.WithError(err).Debug("Could not find PR commit, skipping retries")
			err = nil
		}
	}

	return []*v1.ProwJob{pj}, nil, err
}

func lockKeyForPJ(pj *v1.ProwJob) (*simplePull, error) {
	if pj.Spec.Type != v1.PresubmitJob {
		return nil, fmt.Errorf("can only get lock key for presubmit jobs, was %q", pj.Spec.Type)
	}
	if pj.Spec.Refs == nil {
		return nil, errors.New("pj.Spec.Refs is nil")
	}
	if n := len(pj.Spec.Refs.Pulls); n != 1 {
		return nil, fmt.Errorf("prowjob doesn't have one but %d pulls", n)
	}
	return &simplePull{org: pj.Spec.Refs.Org, repo: pj.Spec.Refs.Repo, number: pj.Spec.Refs.Pulls[0].Number}, nil
}
