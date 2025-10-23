package metrics

import (
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

const (
	EventsPluginName = "events"
)

type EventLevel string

const (
	EventLevelInfo    EventLevel = "Info"
	EventLevelWarning EventLevel = "Warning"
	EventLevelError   EventLevel = "Error"
)

type Event struct {
	Level     EventLevel   `json:"level"`
	Source    string       `json:"source"`
	Locator   EventLocator `json:"locator"`
	Message   EventMessage `json:"message"`
	From      time.Time    `json:"from"`
	To        time.Time    `json:"to"`
	Timestamp time.Time    `json:"timestamp"`
}

type EventLocator struct {
	Type      string         `json:"type"`
	Name      string         `json:"name"`
	Container *string        `json:"container,omitempty"`
	Keys      map[string]any `json:"keys,omitempty"`
}

type EventMessage struct {
	Reason       string         `json:"reason"`
	Cause        string         `json:"cause"`
	HumanMessage string         `json:"humanMessage"`
	Annotations  map[string]any `json:"annotations,omitempty"`
}

func (e *Event) SetTimestamp(t time.Time) {
	e.Timestamp = t
}

func NewEvent(level EventLevel, locator EventLocator, message EventMessage, source string, from, to time.Time) *Event {
	return &Event{Level: level, Source: source, From: from, To: to, Locator: locator, Message: message}
}

type eventsPlugin struct {
	mu     sync.Mutex
	logger *logrus.Entry
	events []MetricsEvent
}

func newEventsPlugin(logger *logrus.Entry) *eventsPlugin {
	return &eventsPlugin{logger: logger.WithField("plugin", EventsPluginName)}
}

func (p *eventsPlugin) Name() string { return EventsPluginName }

func (p *eventsPlugin) Record(ev MetricsEvent) {
	if _, ok := ev.(*Event); !ok {
		return
	}
	p.mu.Lock()
	p.events = append(p.events, ev)
	p.mu.Unlock()
}

func (p *eventsPlugin) Events() []MetricsEvent {
	return p.events
}
