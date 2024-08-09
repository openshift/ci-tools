package main

import (
	"context"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"sigs.k8s.io/prow/pkg/interrupts"
	"sigs.k8s.io/prow/pkg/pjutil"

	v2 "github.com/openshift/ci-tools/cmd/pod-scaler/v2"
	podscalerv2 "github.com/openshift/ci-tools/pkg/pod-scaler/v2"
)

func newReloader(name string, cache v2.Cache) *cacheReloader {
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
	cache  v2.Cache
	logger *logrus.Entry

	lock        *sync.RWMutex
	lastUpdated time.Time
	subscribers []chan<- *podscalerv2.CachedQuery
}

func (c *cacheReloader) subscribe(out chan<- *podscalerv2.CachedQuery) {
	c.lock.Lock()
	c.subscribers = append(c.subscribers, out)
	c.logger.Debugf("new subscriber, subscriber count now: %d", len(c.subscribers))
	c.lock.Unlock()
}

func (c *cacheReloader) reload() {
	// technically this can race as we read the attribute and data from the handle at
	// different times, but there doesn't seem to be an atomic call to GCS for that anyway
	lastUpdated, err := v2.LastUpdated(c.cache, c.name)
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

	data, err := v2.LoadCache(c.cache, c.name, c.logger)
	if err != nil {
		logger.WithError(err).Warn("Failed to read cached data, won't reload this tick.")
		return
	}
	c.lock.Lock()
	if len(c.subscribers) > 0 {
		c.lastUpdated = lastUpdated
		for _, subscriber := range c.subscribers {
			subscriber <- data
		}
	} else {
		logger.Warn("no subscribers yet, won't mark as loaded")
	}
	c.lock.Unlock()
	logger.Debug("Newer update loaded.")
}

func digestAll(data map[string][]*cacheReloader, digesters map[string]digester, health *pjutil.Health, logger *logrus.Entry) {
	var infos []digestInfo
	for id, d := range digesters {
		for _, item := range data[id] {
			s := make(chan *podscalerv2.CachedQuery, 1)
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

type digester func(query *podscalerv2.CachedQuery)

type digestInfo struct {
	name         string
	data         *cacheReloader
	digest       digester
	subscription chan *podscalerv2.CachedQuery
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
