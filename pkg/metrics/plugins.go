package metrics

// Plugin is responsible for collecting and flushing one category of events.
type Plugin interface {
	Name() string
	Record(ev MetricsEvent)
	Events() []MetricsEvent
}
