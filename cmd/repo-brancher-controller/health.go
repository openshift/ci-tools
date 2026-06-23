package main

import (
	"sync/atomic"
	"time"
)

type runtimeHealth struct {
	serverHealthy      atomic.Bool
	lastConfigSuccess  atomic.Int64
	lastGitHubSuccess  atomic.Int64
	maxConfigStaleness time.Duration
}

func (h *runtimeHealth) configSucceeded() {
	now := time.Now().Unix()
	h.lastConfigSuccess.Store(now)
	lastSuccessfulConfigReload.Set(float64(now))
}

func (h *runtimeHealth) githubSucceeded() {
	now := time.Now().Unix()
	h.lastGitHubSuccess.Store(now)
	lastSuccessfulGitHubRequest.Set(float64(now))
}

func (h *runtimeHealth) ready(now time.Time) bool {
	if !h.serverHealthy.Load() {
		return false
	}
	lastConfigSuccess := h.lastConfigSuccess.Load()
	if lastConfigSuccess == 0 || now.Sub(time.Unix(lastConfigSuccess, 0)) > h.maxConfigStaleness {
		return false
	}
	lastGitHubSuccess := h.lastGitHubSuccess.Load()
	if lastGitHubSuccess == 0 || now.Sub(time.Unix(lastGitHubSuccess, 0)) > h.maxConfigStaleness {
		return false
	}
	return true
}
