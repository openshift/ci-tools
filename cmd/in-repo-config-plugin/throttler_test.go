package main

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
)

func TestWebhookThrottler_ExecutesHandler(t *testing.T) {
	throttler := newWebhookThrottler(2, 5, time.Second, time.Second)
	var ran atomic.Bool
	throttler.handle(logrus.NewEntry(logrus.StandardLogger()), func() {
		ran.Store(true)
	})
	if !ran.Load() {
		t.Error("expected handler to run")
	}
}

func TestWebhookThrottler_LimitsConcurrency(t *testing.T) {
	throttler := newWebhookThrottler(1, 5, 2*time.Second, 5*time.Second)

	var active atomic.Int32
	var maxActive atomic.Int32
	var wg sync.WaitGroup
	for range 3 {
		wg.Go(func() {
			throttler.handle(logrus.NewEntry(logrus.StandardLogger()), func() {
				cur := active.Add(1)
				if cur > maxActive.Load() {
					maxActive.Store(cur)
				}
				time.Sleep(10 * time.Millisecond)
				active.Add(-1)
			})
		})
	}
	wg.Wait()

	if maxActive.Load() > 1 {
		t.Errorf("expected max concurrency of 1, got %d", maxActive.Load())
	}
}

func TestWebhookThrottler_DropsWhenQueueFull(t *testing.T) {
	throttler := newWebhookThrottler(1, 1, time.Second, 5*time.Second)

	blocker := make(chan struct{})
	var started sync.WaitGroup
	started.Add(1)

	go throttler.handle(logrus.NewEntry(logrus.StandardLogger()), func() {
		started.Done()
		<-blocker
	})
	started.Wait()

	var ran atomic.Int32
	var wg sync.WaitGroup
	for range 5 {
		wg.Go(func() {
			throttler.handle(logrus.NewEntry(logrus.StandardLogger()), func() {
				ran.Add(1)
			})
		})
	}

	time.Sleep(50 * time.Millisecond)
	close(blocker)
	wg.Wait()

	if ran.Load() > 2 {
		t.Errorf("expected at most 2 handlers to run (1 queued + 1 after blocker), got %d", ran.Load())
	}
}
