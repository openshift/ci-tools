package metrics

import (
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"k8s.io/apimachinery/pkg/api/meta"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
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

// ObjectRef is a minimal, typed reference for a k8s object.
type ObjectRef struct {
	Kind      string `json:"kind"`
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	UID       string `json:"uid"`
}

// BuildObjectRefs converts a list of objects into stable references using
// metadata accessors and kind from GVK/TypeAccessor with a safe fallback.
func BuildObjectRefs(objs []ctrlruntimeclient.Object) []ObjectRef {
	refs := make([]ObjectRef, 0, len(objs))
	for _, o := range objs {
		m, err := meta.Accessor(o)
		if err != nil {
			continue
		}
		// We set Kind where we construct/record the objects; just read it here.
		ta, err := meta.TypeAccessor(o)
		if err != nil || ta.GetKind() == "" {
			continue
		}
		refs = append(refs, ObjectRef{
			Kind:      ta.GetKind(),
			Namespace: m.GetNamespace(),
			Name:      m.GetName(),
			UID:       string(m.GetUID()),
		})
	}
	return refs
}
