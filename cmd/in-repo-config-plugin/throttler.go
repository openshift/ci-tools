package main

import (
	"runtime"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/sirupsen/logrus"
)

var (
	concurrentHandlersInFlight = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "in_repo_config_concurrent_handlers",
		Help: "Number of webhook handlers currently executing",
	})
	droppedHandlerRequests = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "in_repo_config_dropped_requests_total",
		Help: "Number of webhook requests dropped",
	}, []string{"reason"})
)

func init() {
	prometheus.MustRegister(concurrentHandlersInFlight)
	prometheus.MustRegister(droppedHandlerRequests)
}

type webhookThrottler struct {
	executionSlots   chan struct{}
	queueSlots       chan struct{}
	queueTimeout     time.Duration
	executionTimeout time.Duration
}

func newWebhookThrottler(maxConcurrent, maxQueued int, queueTimeout, executionTimeout time.Duration) *webhookThrottler {
	return &webhookThrottler{
		executionSlots:   make(chan struct{}, maxConcurrent),
		queueSlots:       make(chan struct{}, maxQueued),
		queueTimeout:     queueTimeout,
		executionTimeout: executionTimeout,
	}
}

func (t *webhookThrottler) handle(logger *logrus.Entry, handler func()) {
	select {
	case t.executionSlots <- struct{}{}:
		t.run(logger, handler)
		return
	default:
	}

	select {
	case t.queueSlots <- struct{}{}:
	default:
		droppedHandlerRequests.WithLabelValues("queue_full").Inc()
		logger.Warn("dropping webhook request: handler queue is full")
		return
	}

	select {
	case t.executionSlots <- struct{}{}:
		<-t.queueSlots
		t.run(logger, handler)
	case <-time.After(t.queueTimeout):
		<-t.queueSlots
		droppedHandlerRequests.WithLabelValues("queue_timeout").Inc()
		logger.Warn("dropping webhook request: waited too long in queue")
	}
}

func (t *webhookThrottler) run(logger *logrus.Entry, handler func()) {
	done := make(chan struct{})
	go func() {
		concurrentHandlersInFlight.Inc()
		defer concurrentHandlersInFlight.Dec()
		defer func() { <-t.executionSlots }()
		defer close(done)
		defer func() {
			if r := recover(); r != nil {
				stack := make([]byte, 8192)
				stack = stack[:runtime.Stack(stack, false)]
				logger.Errorf("webhook handler panicked: %v\n%s", r, stack)
			}
		}()
		handler()
	}()

	select {
	case <-done:
	case <-time.After(t.executionTimeout):
		logger.Warn("webhook handler exceeded execution timeout, still running in background")
	}
}
