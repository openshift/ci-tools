package metrics

import (
	"context"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	ImagesPluginName = "images"
)

// ImageStreamEvent defines an image stream creation/update event
type ImageStreamEvent struct {
	Namespace          string         `json:"namespace"`
	ImageStreamName    string         `json:"image_stream_name"`
	FullName           string         `json:"full_name"`
	Success            bool           `json:"success"`
	Error              string         `json:"error,omitempty"`
	ImageStreamDetails map[string]any `json:"image_stream_details,omitempty"`
	AdditionalContext  map[string]any `json:"additional_context,omitempty"`
	Timestamp          time.Time      `json:"timestamp"`
}

// TagImportEvent defines a tag import event
type TagImportEvent struct {
	Namespace         string         `json:"namespace"`
	ImageStreamName   string         `json:"image_stream_name"`
	TagName           string         `json:"tag_name"`
	FullTagName       string         `json:"full_tag_name"`
	SourceImage       string         `json:"source_image"`
	SourceImageKind   string         `json:"source_image_kind"`
	StartTime         time.Time      `json:"start_time"`
	CompletionTime    time.Time      `json:"completion_time"`
	DurationSeconds   float64        `json:"duration_seconds"`
	RetryCount        int            `json:"retry_count"`
	Success           bool           `json:"success"`
	Error             string         `json:"error,omitempty"`
	AdditionalContext map[string]any `json:"additional_context,omitempty"`
	Timestamp         time.Time      `json:"timestamp"`
}

func (ise *ImageStreamEvent) SetTimestamp(t time.Time) {
	ise.Timestamp = t
}

func (tie *TagImportEvent) SetTimestamp(t time.Time) {
	tie.Timestamp = t
}

type imagesPlugin struct {
	mu     sync.Mutex
	ctx    context.Context
	logger *logrus.Entry
	events []MetricsEvent
	client ctrlruntimeclient.Client
}

func newImagesPlugin(ctx context.Context, logger *logrus.Entry, client ctrlruntimeclient.Client) *imagesPlugin {
	return &imagesPlugin{
		ctx:    ctx,
		logger: logger.WithField("plugin", ImagesPluginName),
		client: client,
	}
}

func (p *imagesPlugin) Name() string { return ImagesPluginName }

func (p *imagesPlugin) Record(ev MetricsEvent) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if _, ok := ev.(*ImageStreamEvent); ok {
		p.events = append(p.events, ev)
		return
	}
	if _, ok := ev.(*TagImportEvent); ok {
		p.events = append(p.events, ev)
	}
}

func (p *imagesPlugin) Events() []MetricsEvent {
	return p.events
}
