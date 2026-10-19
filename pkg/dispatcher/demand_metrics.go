package dispatcher

import "github.com/prometheus/client_golang/prometheus"

// Demand timestamps expose the age of successful query inputs independently of plan age.
var (
	demandRefreshTimestampMetric = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "prowjob_dispatcher_demand_refresh_timestamp_seconds",
		Help: "Unix time of the last successful demand refresh; zero means no successful query.",
	})
	demandEvaluationTimestampMetric = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "prowjob_dispatcher_demand_evaluation_timestamp_seconds",
		Help: "Evaluation time of the last successful seven-day demand query, including the configured historical offset.",
	})
	demandSamplesMetric = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "prowjob_dispatcher_demand_observed_jobs",
		Help: "Jobs with usable samples in the last successful demand query, before configured-job filtering.",
	})
	demandQueryErrorsMetric = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "prowjob_dispatcher_demand_query_errors_total",
		Help: "Demand refreshes that failed instead of publishing new inputs.",
	})
)

func init() {
	prometheus.MustRegister(demandRefreshTimestampMetric, demandEvaluationTimestampMetric, demandSamplesMetric, demandQueryErrorsMetric)
}
