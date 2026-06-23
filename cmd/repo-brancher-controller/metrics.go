package main

import "github.com/prometheus/client_golang/prometheus"

var (
	reconciliationTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "repo_brancher_reconciliations_total",
		Help: "Repository reconciliation outcomes.",
	}, []string{"result"})
	queueDepth = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "repo_brancher_queue_depth",
		Help: "Current number of repository keys waiting in the work queue.",
	})
	lastSuccessfulConfigReload = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "repo_brancher_last_successful_config_reload_timestamp_seconds",
		Help: "Unix timestamp of the last successful desired-state reload.",
	})
	lastFullResync = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "repo_brancher_last_full_resync_timestamp_seconds",
		Help: "Unix timestamp of the last full reconciliation enqueue.",
	})
	lastSuccessfulGitHubRequest = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "repo_brancher_last_successful_github_request_timestamp_seconds",
		Help: "Unix timestamp of the last successful GitHub API request.",
	})
	webhooksTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "repo_brancher_webhooks_total",
		Help: "Webhook requests by result.",
	}, []string{"result"})
)

func init() {
	prometheus.MustRegister(
		reconciliationTotal,
		queueDepth,
		lastSuccessfulConfigReload,
		lastFullResync,
		lastSuccessfulGitHubRequest,
		webhooksTotal,
	)
}
