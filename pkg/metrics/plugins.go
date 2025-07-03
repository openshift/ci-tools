package metrics

const (
	InsightsPluginName = "test_platform_insights"
	BuildsPluginName   = "openshift_builds"
)

// Plugin is responsible for collecting and flushing one category of events.
type Plugin interface {
	Name() string
	Record(ev MetricsEvent)
	Events() []MetricsEvent
}
