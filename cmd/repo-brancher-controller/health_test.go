package main

import (
	"testing"
	"time"
)

func TestRuntimeHealthReadiness(t *testing.T) {
	now := time.Now()
	health := &runtimeHealth{maxConfigStaleness: time.Minute}
	if health.ready(now) {
		t.Fatal("new health state must not be ready")
	}
	health.serverHealthy.Store(true)
	health.configSucceeded()
	health.githubSucceeded()
	if !health.ready(time.Now()) {
		t.Fatal("healthy runtime should be ready")
	}
	if health.ready(time.Now().Add(2 * time.Minute)) {
		t.Fatal("stale configuration and GitHub success must fail readiness")
	}

	health.lastConfigSuccess.Store(now.Unix())
	health.lastGitHubSuccess.Store(now.Add(-2 * time.Minute).Unix())
	if health.ready(now) {
		t.Fatal("stale GitHub success must fail readiness")
	}
}
