package main

import (
	"context"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"k8s.io/test-infra/prow/interrupts"
	"k8s.io/test-infra/prow/pjutil"

	pod_scaler "github.com/openshift/ci-tools/pkg/pod-scaler"
)

func newReloader(name string, cache cache) *cacheReloader {
	reloader := &cacheReloader{
		name:  name,
		cache: cache,
		logger: logrus.WithFields(logrus.Fields{
			"component": "pod-scaler reloader",
			"metric":    name,
		}),
		lock: &sync.RWMutex{},
	}
	interrupts.TickLiteral(reloader.reload, 10*time.Minute)
	return reloader
}

type cacheReloader struct {
	name   string
	cache  cache
	logger *logrus.Entry

	lock        *sync.RWMutex
	lastUpdated time.Time
	lastLoaded  *pod_scaler.CachedQuery
	subscribers []chan<- interface{}
}

func (c *cacheReloader) subscribe(out chan<- interface{}) {
	c.lock.Lock()
	c.subscribers = append(c.subscribers, out)
	// if a subscriber is added and we already have data, let them know
	// so they don't need to wait for the next tick to figure it out
	if c.lastLoaded != nil {
		c.logger.Warn("subscriber after data")
		out <- struct{}{}
	}
	c.lock.Unlock()
}

func (c *cacheReloader) reload() {
	// technically this can race as we read the attribute and data from the handle at
	// different times, but there doesn't seem to be an atomic call to GCS for that anyway
	lastUpdated, err := lastUpdated(c.cache, c.name)
	if err != nil {
		c.logger.WithError(err).Warn("Failed to query for last cache update time, won't reload this tick.")
		return
	}
	c.lock.RLock()
	lastSeen := c.lastUpdated
	c.lock.RUnlock()
	logger := c.logger.WithFields(logrus.Fields{
		"last_update_seen": lastSeen.Format(time.RFC3339),
		"last_update":      lastUpdated.Format(time.RFC3339),
	})

	if lastUpdated == lastSeen {
		logger.Debug("Last updated time on cloud artifacts matches our last load, won't reload this tick.")
		return
	}
	logger.Debug("Newer update available in cloud storage, reloading data.")

	data, err := loadCache(c.cache, c.name, c.logger)
	if err != nil {
		logger.WithError(err).Warn("Failed to read cached data, won't reload this tick.")
		return
	}
	c.lock.Lock()
	c.lastUpdated = lastUpdated
	c.lastLoaded = data
	for _, subscriber := range c.subscribers {
		subscriber <- struct{}{}
	}
	c.lock.Unlock()
	logger.Debug("Newer update loaded.")
}

func (c *cacheReloader) data() *pod_scaler.CachedQuery {
	c.lock.RLock()
	defer c.lock.RUnlock()
	return c.lastLoaded
}

func digestAll(data map[string][]*cacheReloader, digesters map[string]digester, health *pjutil.Health, logger *logrus.Entry) {
	var infos []digestInfo
	for id, d := range digesters {
		for _, item := range data[id] {
			infos = append(infos, digestInfo{name: item.name, data: item, digest: d})
		}
	}
	loadDone := digest(logger, infos...)
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

type digester func(query *pod_scaler.CachedQuery)

type digestInfo struct {
	name   string
	data   *cacheReloader
	digest digester
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
		} else {
			loadDone <- struct{}{}
		}
	}
	for i := range infos {
		info := infos[i]
		thisOnce := &sync.Once{}
		interrupts.Run(func(ctx context.Context) {
			subLogger := logger.WithField("subscription", info.name)
			subscription := make(chan interface{}, 1)
			info.data.subscribe(subscription)
			subLogger.Debug("Starting subscription.")
			for {
				select {
				case <-ctx.Done():
					subLogger.Debug("Subscription cancelled.")
					return
				case <-subscription:
				}
				subLogger.Debug("Digesting new data from subscription.")
				info.digest(info.data.data())
				thisOnce.Do(update)
			}
		})
	}
	return loadDone
}
