package main

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"sort"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/util/workqueue"
)

type repoKey struct {
	org, repo, source string
}

func (k repoKey) String() string { return fmt.Sprintf("%s/%s@%s", k.org, k.repo, k.source) }

type desiredState struct {
	mu       sync.RWMutex
	targets  map[repoKey]sets.Set[string]
	keyLocks map[repoKey]*sync.Mutex
}

func newDesiredState() *desiredState {
	return &desiredState{targets: map[repoKey]sets.Set[string]{}, keyLocks: map[repoKey]*sync.Mutex{}}
}

func (s *desiredState) keyLockLocked(key repoKey) *sync.Mutex {
	if s.keyLocks[key] == nil {
		s.keyLocks[key] = &sync.Mutex{}
	}
	return s.keyLocks[key]
}

func cloneTargets(in map[repoKey]sets.Set[string]) map[repoKey]sets.Set[string] {
	out := make(map[repoKey]sets.Set[string], len(in))
	for key, targets := range in {
		out[key] = targets.Clone()
	}
	return out
}

// replace installs a new desired state and returns keys whose configuration changed.
func (s *desiredState) replace(next map[repoKey]sets.Set[string]) []repoKey {
	s.mu.Lock()
	allKeys := sets.New[repoKey]()
	for key := range s.targets {
		allKeys.Insert(key)
	}
	for key := range next {
		allKeys.Insert(key)
	}
	keys := allKeys.UnsortedList()
	sort.Slice(keys, func(i, j int) bool { return keys[i].String() < keys[j].String() })
	locks := make([]*sync.Mutex, 0, len(keys))
	for _, key := range keys {
		locks = append(locks, s.keyLockLocked(key))
	}
	s.mu.Unlock()
	for _, lock := range locks {
		lock.Lock()
	}
	defer func() {
		for i := len(locks) - 1; i >= 0; i-- {
			locks[i].Unlock()
		}
	}()

	s.mu.Lock()
	defer s.mu.Unlock()
	changed := sets.New[repoKey]()
	for key, targets := range next {
		if old, ok := s.targets[key]; !ok || !old.Equal(targets) {
			changed.Insert(key)
		}
	}
	s.targets = cloneTargets(next)
	return changed.UnsortedList()
}

func (s *desiredState) get(key repoKey) (sets.Set[string], bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	targets, ok := s.targets[key]
	if !ok {
		return nil, false
	}
	return targets.Clone(), true
}

func (s *desiredState) matching(org, repo, branch string) []repoKey {
	s.mu.RLock()
	defer s.mu.RUnlock()
	key := repoKey{org: org, repo: repo, source: branch}
	if _, ok := s.targets[key]; ok {
		return []repoKey{key}
	}
	return nil
}

// ifTargetConfigured serializes mutations with desired-state replacement for
// this repository. Reloads wait for an already-started mutation; mutations
// starting after a removal observe the new state and are skipped.
func (s *desiredState) ifTargetConfigured(key repoKey, target string, fn func() error) (bool, error) {
	s.mu.Lock()
	keyLock := s.keyLockLocked(key)
	s.mu.Unlock()
	keyLock.Lock()
	defer keyLock.Unlock()
	s.mu.RLock()
	targets, ok := s.targets[key]
	configured := ok && targets.Has(target)
	s.mu.RUnlock()
	if !configured {
		return false, nil
	}
	return true, fn()
}

func (s *desiredState) keys() []repoKey {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]repoKey, 0, len(s.targets))
	for key := range s.targets {
		result = append(result, key)
	}
	return result
}

type refClient interface {
	getRef(context.Context, string, string, string) (sha string, exists bool, err error)
	createRef(context.Context, string, string, string, string) error
	updateRef(context.Context, string, string, string, string) error
}

type controller struct {
	refs                refClient
	state               *desiredState
	issues              *branchIssueStore
	queue               workqueue.TypedRateLimitingInterface[repoKey]
	maxRetries          int
	retryExhaustedDelay time.Duration
}

type permanentError struct{ error }

func jitterDuration(duration time.Duration, factor float64) time.Duration {
	if duration <= 0 || factor <= 0 {
		return duration
	}
	multiplier := 1 - factor + rand.Float64()*(2*factor)
	return time.Duration(float64(duration) * multiplier)
}

type reconciliationErrors struct {
	retryable []error
	permanent []error
}

func (e *reconciliationErrors) Error() string {
	return errors.Join(append(append([]error{}, e.retryable...), e.permanent...)...).Error()
}

func (e *reconciliationErrors) Unwrap() []error {
	return append(append([]error{}, e.retryable...), e.permanent...)
}

func newController(refs refClient, state *desiredState, maxRetries int, retryExhaustedDelay ...time.Duration) *controller {
	delay := 15 * time.Minute
	if len(retryExhaustedDelay) > 0 {
		delay = retryExhaustedDelay[0]
	}
	return &controller{
		refs:                refs,
		state:               state,
		queue:               workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[repoKey]()),
		maxRetries:          maxRetries,
		retryExhaustedDelay: delay,
	}
}

func (c *controller) enqueue(keys ...repoKey) {
	for _, key := range keys {
		c.queue.Add(key)
	}
	queueDepth.Set(float64(c.queue.Len()))
}

func (c *controller) runWorker(ctx context.Context) {
	for c.processNext(ctx) {
	}
}

func (c *controller) processNext(ctx context.Context) bool {
	key, shutdown := c.queue.Get()
	if shutdown {
		return false
	}
	queueDepth.Set(float64(c.queue.Len()))
	defer c.queue.Done(key)

	if ctx.Err() != nil {
		c.queue.Forget(key)
		return false
	}
	if err := c.reconcile(ctx, key); err != nil {
		logger := logrus.WithField("repo", key.String()).WithError(err)
		if ctx.Err() != nil {
			reconciliationTotal.WithLabelValues("shutdown").Inc()
			logger.Info("reconciliation stopped during shutdown")
			c.queue.Forget(key)
			return false
		}
		var aggregate *reconciliationErrors
		if errors.As(err, &aggregate) {
			if len(aggregate.permanent) > 0 {
				c.reportPermanentError(key, logger)
			}
			if len(aggregate.retryable) > 0 {
				c.retry(key, logger)
				return true
			}
			return true
		}
		var permanent permanentError
		if errors.As(err, &permanent) {
			c.reportPermanentError(key, logger)
			return true
		}
		c.retry(key, logger)
		return true
	}
	reconciliationTotal.WithLabelValues("success").Inc()
	c.queue.Forget(key)
	return true
}

func (c *controller) reportPermanentError(key repoKey, logger *logrus.Entry) {
	logger.Error("reconciliation requires manual intervention")
	reconciliationTotal.WithLabelValues("permanent_error").Inc()
	c.queue.Forget(key)
}

func (c *controller) retry(key repoKey, logger *logrus.Entry) {
	if c.queue.NumRequeues(key) < c.maxRetries {
		reconciliationTotal.WithLabelValues("retry").Inc()
		logger.Warn("reconciliation failed; retrying")
		c.queue.AddRateLimited(key)
	} else {
		logger.WithField("retry_after", c.retryExhaustedDelay).Error("fast retry limit reached; scheduling slow retry")
		reconciliationTotal.WithLabelValues("retry_exhausted").Inc()
		c.queue.Forget(key)
		c.queue.AddAfter(key, jitterDuration(c.retryExhaustedDelay, 0.2))
	}
}

func (c *controller) recordTargetIssue(key repoKey, target, reason, sourceSHA, targetSHA string, err error, permanent bool) {
	issueKey := branchIssueKey{org: key.org, repo: key.repo, source: key.source, target: target}
	if c.issues != nil {
		c.issues.upsert(branchIssue{
			key:       issueKey,
			reason:    reason,
			sourceSHA: sourceSHA,
			targetSHA: targetSHA,
			lastError: err.Error(),
		})
	}
	logger := logrus.WithFields(logrus.Fields{
		"org":                 key.org,
		"repo":                key.repo,
		"source_branch":       key.source,
		"target_branch":       target,
		"source_sha":          sourceSHA,
		"target_sha":          targetSHA,
		"reason":              reason,
		"github_compare_url":  issueKey.compareURL(),
		"manual_intervention": permanent,
	}).WithError(err)
	if permanent {
		logger.Error("branch fast-forward requires manual intervention")
		return
	}
	logger.Warn("branch fast-forward failed")
}

func (c *controller) resolveTargetIssue(key repoKey, target string) {
	if c.issues == nil {
		return
	}
	c.issues.resolve(branchIssueKey{org: key.org, repo: key.repo, source: key.source, target: target})
}

func (c *controller) reconcile(ctx context.Context, key repoKey) error {
	targetSet, configured := c.state.get(key)
	if !configured {
		return nil
	}
	sourceSHA, exists, err := c.refs.getRef(ctx, key.org, key.repo, key.source)
	if err != nil {
		return fmt.Errorf("get source ref: %w", err)
	}
	if !exists {
		return fmt.Errorf("source branch %q does not exist", key.source)
	}

	targets := targetSet.UnsortedList()
	sort.Strings(targets)
	aggregate := &reconciliationErrors{}
	for _, target := range targets {
		targetSHA, targetExists, err := c.refs.getRef(ctx, key.org, key.repo, target)
		if err != nil {
			c.recordTargetIssue(key, target, "get_target_ref_failed", sourceSHA, "", err, false)
			aggregate.retryable = append(aggregate.retryable, fmt.Errorf("get target %s: %w", target, err))
			continue
		}
		if targetExists && targetSHA == sourceSHA {
			c.resolveTargetIssue(key, target)
			continue
		}
		performed, err := c.state.ifTargetConfigured(key, target, func() error {
			if !targetExists {
				return c.refs.createRef(ctx, key.org, key.repo, target, sourceSHA)
			}
			return c.refs.updateRef(ctx, key.org, key.repo, target, sourceSHA)
		})
		if !performed {
			continue
		}
		if err != nil {
			wrapped := fmt.Errorf("fast-forward %s: %w", target, err)
			var permanent permanentError
			if errors.As(err, &permanent) {
				reason := "non_fast_forward"
				if !targetExists {
					reason = "ref_creation_rejected"
				}
				c.recordTargetIssue(key, target, reason, sourceSHA, targetSHA, err, true)
				aggregate.permanent = append(aggregate.permanent, wrapped)
			} else {
				reason := "update_ref_failed"
				if !targetExists {
					reason = "create_ref_failed"
				}
				c.recordTargetIssue(key, target, reason, sourceSHA, targetSHA, err, false)
				aggregate.retryable = append(aggregate.retryable, wrapped)
			}
			continue
		}
		logrus.WithFields(logrus.Fields{"repo": key.org + "/" + key.repo, "source": key.source, "target": target, "sha": sourceSHA}).Info("fast-forwarded branch")
		c.resolveTargetIssue(key, target)
	}
	if len(aggregate.retryable) == 0 && len(aggregate.permanent) == 0 {
		return nil
	}
	return aggregate
}
