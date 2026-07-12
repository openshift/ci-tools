package main

import (
	"context"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"sigs.k8s.io/prow/pkg/interrupts"
	"sigs.k8s.io/prow/pkg/pjutil"

	podscaler "github.com/openshift/ci-tools/pkg/pod-scaler"
)

const initialCacheLoadAttempts = 3

func newReloader(name string, cache Cache) *cacheReloader {
	reloader := &cacheReloader{
		name:  name,
		cache: cache,
		logger: logrus.WithFields(logrus.Fields{
			"component": "pod-scaler reloader",
			"metric":    name,
		}),
		lock: &sync.RWMutex{},
	}
	interrupts.TickLiteral(reloader.reload, time.Hour)
	return reloader
}

type cacheReloader struct {
	name   string
	cache  Cache
	logger *logrus.Entry

	lock        *sync.RWMutex
	lastUpdated time.Time
	pending     *podscaler.CachedQuery
	subscribers []chan<- *podscaler.CachedQuery
}

func (c *cacheReloader) subscribe(out chan<- *podscaler.CachedQuery) {
	c.lock.Lock()
	defer c.lock.Unlock()
	c.subscribers = append(c.subscribers, out)
	c.logger.Debugf("new subscriber, subscriber count now: %d", len(c.subscribers))
	if c.pending != nil {
		out <- c.pending
		c.pending = nil
	}
}

func emptyCachedQuery() *podscaler.CachedQuery {
	return &podscaler.CachedQuery{}
}

func cachedQueryEmpty(q *podscaler.CachedQuery) bool {
	if q == nil {
		return true
	}
	return len(q.Data) == 0 && len(q.DataByMetaData) == 0
}

func loadCacheWithRetries(cache Cache, name string, logger *logrus.Entry, attempts int) (*podscaler.CachedQuery, error) {
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(attempt) * time.Second)
			logger.WithField("attempt", attempt+1).Debug("Retrying initial cache load.")
		}
		data, err := LoadCache(cache, name, logger)
		if err == nil {
			return data, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

func (c *cacheReloader) reload() {
	// technically this can race as we read the attribute and data from the handle at
	// different times, but there doesn't seem to be an atomic call to GCS for that anyway
	lastUpdated, lastUpdatedErr := LastUpdated(c.cache, c.name)
	c.lock.RLock()
	lastSeen := c.lastUpdated
	c.lock.RUnlock()
	logger := c.logger.WithFields(logrus.Fields{
		"last_update_seen": lastSeen.Format(time.RFC3339),
	})
	if lastUpdatedErr != nil {
		logger.WithError(lastUpdatedErr).Warn("Failed to query for last cache update time.")
		if !lastSeen.IsZero() {
			logger.Warn("Skipping reload because freshness is unknown and cache was loaded previously.")
			return
		}
		logger.Warn("Attempting initial cache load without last-updated metadata.")
	} else {
		logger = logger.WithField("last_update", lastUpdated.Format(time.RFC3339))
		if !lastSeen.IsZero() && lastUpdated == lastSeen {
			logger.Debug("Last updated time on cloud artifacts matches our last load, won't reload this tick.")
			return
		}
		logger.Debug("Newer update available in cloud storage, reloading data.")
	}

	var (
		data    *podscaler.CachedQuery
		loadErr error
	)
	if lastSeen.IsZero() {
		data, loadErr = loadCacheWithRetries(c.cache, c.name, c.logger, initialCacheLoadAttempts)
	} else {
		data, loadErr = LoadCache(c.cache, c.name, c.logger)
	}
	if loadErr != nil {
		logger.WithError(loadErr).Warn("Failed to read cached data.")
		if !lastSeen.IsZero() {
			logger.Warn("Keeping previously loaded cache.")
			return
		}
		logger.Warn("Serving with empty cache after failed initial load.")
		data = emptyCachedQuery()
	}
	c.deliverLoadedCache(logger, lastSeen, lastUpdated, lastUpdatedErr, loadErr, data)
}

func (c *cacheReloader) deliverLoadedCache(logger *logrus.Entry, lastSeen, lastUpdated time.Time, lastUpdatedErr, loadErr error, data *podscaler.CachedQuery) {
	if loadErr == nil && !lastSeen.IsZero() && cachedQueryEmpty(data) {
		logger.Warn("Ignoring empty cache reload; keeping previously loaded cache.")
		return
	}
	if lastUpdatedErr == nil && loadErr == nil {
		afterLoad, err := LastUpdated(c.cache, c.name)
		if err == nil && !afterLoad.Equal(lastUpdated) {
			logger.WithField("last_update_after_load", afterLoad.Format(time.RFC3339)).Warn("Cache object changed during load; keeping previous cache.")
			if !lastSeen.IsZero() {
				return
			}
		}
	}

	updatedAt := lastUpdated
	if lastUpdatedErr != nil {
		updatedAt = time.Now()
	}

	c.lock.Lock()
	defer c.lock.Unlock()
	initialLoadFailed := loadErr != nil && lastSeen.IsZero()
	if !initialLoadFailed {
		c.lastUpdated = updatedAt
	}
	if len(c.subscribers) == 0 {
		logger.Warn("no subscribers yet, deferring cache delivery until subscription")
		c.pending = data
		if lastUpdatedErr != nil && loadErr != nil {
			logger.Debug("Initial empty cache deferred.")
		} else {
			logger.Debug("Newer update deferred.")
		}
		return
	}
	for _, subscriber := range c.subscribers {
		subscriber <- data
	}
	if lastUpdatedErr != nil && loadErr != nil {
		logger.Debug("Initial empty cache loaded.")
	} else {
		logger.Debug("Newer update loaded.")
	}
}

func digestAll(data map[string][]*cacheReloader, digesters map[string]digester, health *pjutil.Health, logger *logrus.Entry) {
	var infos []digestInfo
	for id, d := range digesters {
		for _, item := range data[id] {
			s := make(chan *podscaler.CachedQuery, 1)
			item.subscribe(s)
			infos = append(infos, digestInfo{
				name:         item.name,
				data:         item,
				digest:       d,
				subscription: s,
			})
		}
	}
	logger.Debugf("digesting %d infos.", len(infos))
	loadDone := digest(logger, infos...)
	// Now that the initial subscriptions are completed, lets make sure they are updated
	for _, info := range infos {
		info.data.reload()
	}
	interrupts.Run(func(ctx context.Context) {
		select {
		case <-ctx.Done():
			logger.Debug("Waiting for readiness cancelled.")
			return
		case <-loadDone:
			logger.Debug("Ready to serve.")
			health.ServeReady()
		}
	})
}

type digester func(query *podscaler.CachedQuery)

type digestInfo struct {
	name         string
	data         *cacheReloader
	digest       digester
	subscription chan *podscaler.CachedQuery
}

func digest(logger *logrus.Entry, infos ...digestInfo) <-chan interface{} {
	var loaded int
	loadDone := make(chan interface{})
	loadLock := &sync.Mutex{}
	update := func() {
		loadLock.Lock()
		defer loadLock.Unlock()
		if loaded != len(infos)-1 {
			loaded += 1
			logger.Debugf("Now loaded %d info(s) out of %d", loaded, len(infos))
		} else {
			logger.Debugf("Now loaded all %d info(s)", len(infos))
			loadDone <- struct{}{}
		}
	}
	for i := range infos {
		info := infos[i]
		thisOnce := &sync.Once{}
		interrupts.Run(func(ctx context.Context) {
			subLogger := logger.WithField("subscription", info.name)
			subLogger.Debug("Starting subscription.")
			for {
				select {
				case <-ctx.Done():
					subLogger.Debug("Subscription cancelled.")
					return
				case data := <-info.subscription:
					subLogger.Debug("Digesting new data from subscription.")
					info.digest(data)
					thisOnce.Do(update)
				}
			}
		})
	}
	return loadDone
}
