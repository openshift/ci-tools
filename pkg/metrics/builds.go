package metrics

import (
	"context"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	controllerruntime "sigs.k8s.io/controller-runtime/pkg/client"

	buildapi "github.com/openshift/api/build/v1"
)

const (
	BuildsPluginName = "openshift_builds"
)

// BuildEvent defines a build event for the metrics system.
type BuildEvent struct {
	Namespace         string         `json:"namespace"`
	Name              string         `json:"name"`
	StartTime         time.Time      `json:"start_time"`
	CompletionTime    time.Time      `json:"completion_time"`
	DurationSeconds   int            `json:"duration_seconds"`
	Status            string         `json:"status"`
	Reason            string         `json:"reason,omitempty"`
	OutputImage       string         `json:"output_image,omitempty"`
	AdditionalContext map[string]any `json:"additional_context,omitempty"`
	Timestamp         time.Time      `json:"timestamp"`
	ForImage          string         `json:"for_image,omitempty"`
}

// SetTimestamp sets the timestamp of the event.
func (be *BuildEvent) SetTimestamp(t time.Time) {
	be.Timestamp = t
}

// buildPlugin manages the build events.
type buildPlugin struct {
	mu     sync.Mutex
	ctx    context.Context
	logger *logrus.Entry
	client controllerruntime.Client
	events []MetricsEvent
}

func newBuildPlugin(ctx context.Context, logger *logrus.Entry, client controllerruntime.Client) *buildPlugin {
	return &buildPlugin{ctx: ctx, client: client, logger: logger.WithField("plugin", BuildsPluginName)}
}

func (p *buildPlugin) Name() string { return BuildsPluginName }

// Record populates and records a BuildEvent.
func (p *buildPlugin) Record(ev MetricsEvent) {
	be, ok := ev.(*BuildEvent)
	if !ok {
		return
	}

	var build buildapi.Build
	if err := p.client.Get(p.ctx, controllerruntime.ObjectKey{Namespace: be.Namespace, Name: be.Name}, &build); err != nil {
		logrus.WithError(err).Warnf("failed to get build %q; aborting event recording", be.Name)
		return
	}

	be.Namespace = build.Namespace
	be.Name = build.Name
	start := build.Status.StartTimestamp.Time
	comp := build.Status.CompletionTimestamp.Time
	be.StartTime = start
	be.CompletionTime = comp
	if !start.IsZero() && !comp.IsZero() {
		be.DurationSeconds = int(comp.Sub(start).Seconds())
	}
	be.Status = string(build.Status.Phase)
	be.Reason = string(build.Status.Reason)
	be.OutputImage = build.Spec.Output.To.Name

	if be.Timestamp.IsZero() {
		be.Timestamp = time.Now()
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	p.logger.WithField("event", be).Debug("Recording build event")
	p.events = append(p.events, be)
}

func (p *buildPlugin) Events() []MetricsEvent {
	return p.events
}

// NewBuildEvent constructs a BuildEvent.
func NewBuildEvent(name, namespace, forImage string) *BuildEvent {
	return &BuildEvent{
		Namespace: namespace,
		Name:      name,
		ForImage:  forImage,
	}
}
