package metrics

import (
	"regexp"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/openshift/ci-tools/pkg/lease"
)

type leaseEvent interface {
	MetricsEvent

	GetRawLeaseName() string
	SetParsedName(region, leaseName, slice string)
	SetPoolMetrics(free, total int)
	Store(lp *leasesPlugin)
}

// LeaseAcquisitionMetricEvent is the event for a single lease acquisition.
type LeaseAcquisitionMetricEvent struct {
	LeaseName                    string    `json:"name"`
	Slice                        string    `json:"slice,omitempty"`
	Region                       string    `json:"region,omitempty"`
	RawLeaseName                 string    `json:"raw_lease_name,omitempty"`
	AcquisitionDurationSeconds   float64   `json:"acquisition_duration_seconds"`
	LeasesRemainingAtAcquisition int       `json:"leases_remaining_at_acquisition"`
	LeasesTotal                  int       `json:"leases_total"`
	Timestamp                    time.Time `json:"timestamp"`
}

// Name returns the name of the event.
func (l *LeaseAcquisitionMetricEvent) Name() string {
	return l.LeaseName
}

// SetTimestamp sets the event's timestamp.
func (l *LeaseAcquisitionMetricEvent) SetTimestamp(t time.Time) {
	l.Timestamp = t
}

// GetRawLeaseName returns the raw lease name.
func (l *LeaseAcquisitionMetricEvent) GetRawLeaseName() string {
	return l.RawLeaseName
}

// SetParsedName sets the parsed lease name components.
func (l *LeaseAcquisitionMetricEvent) SetParsedName(region, leaseName, slice string) {
	l.Region, l.LeaseName, l.Slice = region, leaseName, slice
}

// SetPoolMetrics sets the pool metrics.
func (l *LeaseAcquisitionMetricEvent) SetPoolMetrics(free, total int) {
	l.LeasesRemainingAtAcquisition = free
	l.LeasesTotal = total
}

// Store persists the acquisition event on the plugin
func (l *LeaseAcquisitionMetricEvent) Store(lp *leasesPlugin) {
	lp.events = append(lp.events, *l)
}

// LeaseReleaseMetricEvent is the event for a single lease release.
type LeaseReleaseMetricEvent struct {
	LeaseName                string    `json:"name"`
	Slice                    string    `json:"slice,omitempty"`
	Region                   string    `json:"region,omitempty"`
	RawLeaseName             string    `json:"raw_lease_name,omitempty"`
	ReleaseDurationSeconds   float64   `json:"release_duration_seconds"`
	LeasesAvailableAtRelease int       `json:"leases_available_at_release"`
	LeasesTotal              int       `json:"leases_total"`
	Released                 bool      `json:"released"`
	Error                    string    `json:"error,omitempty"`
	Timestamp                time.Time `json:"timestamp"`
}

// Name returns the name of the event.
func (l *LeaseReleaseMetricEvent) Name() string {
	return l.LeaseName
}

// SetTimestamp sets the event's timestamp.
func (l *LeaseReleaseMetricEvent) SetTimestamp(t time.Time) {
	l.Timestamp = t
}

// GetRawLeaseName returns the raw lease name.
func (l *LeaseReleaseMetricEvent) GetRawLeaseName() string {
	return l.RawLeaseName
}

// SetParsedName sets the parsed lease name components.
func (l *LeaseReleaseMetricEvent) SetParsedName(region, leaseName, slice string) {
	l.Region, l.LeaseName, l.Slice = region, leaseName, slice
}

// SetPoolMetrics sets the pool metrics.
func (l *LeaseReleaseMetricEvent) SetPoolMetrics(free, total int) {
	l.LeasesAvailableAtRelease = free
	l.LeasesTotal = total
}

// Store persists the release event on the plugin
func (l *LeaseReleaseMetricEvent) Store(lp *leasesPlugin) {
	lp.releaseEvents = append(lp.releaseEvents, *l)
}

// Group 1: region (non-greedy up to the literal "--")
// Group 2: canonical name (greedy until the last hyphen)
// Group 3: slice (digit(s)), at the end.
var leaseNameRegexp = regexp.MustCompile(`^(.+?)--(.+)-(\d+)$`)

func parseLeaseEventName(raw string) (string, string, string) {
	matches := leaseNameRegexp.FindStringSubmatch(raw)
	if len(matches) == 4 {
		return matches[1], matches[2], matches[3]
	}
	return "", raw, ""
}

// leasesPlugin implements the Plugin interface for lease-specific metrics.
type leasesPlugin struct {
	mu            sync.RWMutex
	logger        *logrus.Entry
	events        []LeaseAcquisitionMetricEvent
	releaseEvents []LeaseReleaseMetricEvent
	client        lease.Client
}

// newLeasesPlugin creates a new lease metrics plugin.
func newLeasesPlugin(logger *logrus.Entry) *leasesPlugin {
	return &leasesPlugin{
		logger:        logger.WithField("plugin", "leases"),
		events:        make([]LeaseAcquisitionMetricEvent, 0),
		releaseEvents: make([]LeaseReleaseMetricEvent, 0),
	}
}

// Name returns the name of this plugin.
func (lp *leasesPlugin) Name() string {
	return "leases"
}

func (lp *leasesPlugin) Record(ev MetricsEvent) {
	if lp == nil {
		return
	}
	lp.mu.RLock()
	client := lp.client
	lp.mu.RUnlock()

	if client == nil {
		return
	}

	switch e := ev.(type) {
	case leaseEvent:
		rawLeaseName := e.GetRawLeaseName()
		region, leaseName, slice := parseLeaseEventName(rawLeaseName)
		e.SetParsedName(region, leaseName, slice)

		metricsData, err := client.Metrics(leaseName)
		if err != nil {
			logrus.WithError(err).Debugf("failed to retrieve metrics for lease %q; skipping numeric updates", rawLeaseName)
		} else {
			e.SetPoolMetrics(metricsData.Free, metricsData.Free+metricsData.Leased)
		}

		lp.mu.Lock()
		defer lp.mu.Unlock()
		e.Store(lp)
		lp.logger.WithField("event", e).Debug("Recording lease metrics event")
	}
}

// Events returns a slice of MetricsEvent interfaces for the stored events.
func (lp *leasesPlugin) Events() []MetricsEvent {
	lp.mu.Lock()
	defer lp.mu.Unlock()

	events := make([]MetricsEvent, 0, len(lp.events)+len(lp.releaseEvents))
	for i := range lp.events {
		events = append(events, &lp.events[i])
	}
	for i := range lp.releaseEvents {
		events = append(events, &lp.releaseEvents[i])
	}

	return events
}

func (lp *leasesPlugin) SetClient(client lease.Client) {
	lp.mu.Lock()
	defer lp.mu.Unlock()
	lp.client = client
}
