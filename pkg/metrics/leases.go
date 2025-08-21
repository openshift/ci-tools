package metrics

import (
	"regexp"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/openshift/ci-tools/pkg/lease"
)

// LeaseMetricEvent is the event for a single lease acquisition.
type LeaseMetricEvent struct {
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
func (l *LeaseMetricEvent) Name() string {
	return l.LeaseName
}

// SetTimestamp sets the event's timestamp.
func (l *LeaseMetricEvent) SetTimestamp(t time.Time) {
	l.Timestamp = t
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
	mu     sync.RWMutex
	logger *logrus.Entry
	events []LeaseMetricEvent
	client lease.Client
}

// newLeasesPlugin creates a new lease metrics plugin.
func newLeasesPlugin(logger *logrus.Entry) *leasesPlugin {
	return &leasesPlugin{
		logger: logger.WithField("plugin", "leases"),
		events: make([]LeaseMetricEvent, 0),
	}
}

// Name returns the name of this plugin.
func (lp *leasesPlugin) Name() string {
	return "leases"
}

func (lp *leasesPlugin) Record(ev MetricsEvent) {
	le, ok := ev.(*LeaseMetricEvent)
	if !ok {
		return
	}

	lp.mu.RLock()
	client := lp.client
	lp.mu.RUnlock()

	if client == nil {
		return
	}

	le.Region, le.LeaseName, le.Slice = parseLeaseEventName(le.RawLeaseName)
	metricsData, err := client.Metrics(le.LeaseName)
	if err != nil {
		logrus.WithError(err).Debugf("failed to retrieve metrics for lease %q; skipping numeric updates", le.RawLeaseName)
	} else {
		le.LeasesRemainingAtAcquisition = metricsData.Free
		le.LeasesTotal = metricsData.Free + metricsData.Leased
	}

	lp.mu.Lock()
	lp.logger.WithField("event", le).Debug("Recording lease metrics event")
	lp.events = append(lp.events, *le)
	lp.mu.Unlock()
}

// Events returns a slice of MetricsEvent interfaces for the stored events.
func (lp *leasesPlugin) Events() []MetricsEvent {
	lp.mu.Lock()
	defer lp.mu.Unlock()

	events := make([]MetricsEvent, 0, len(lp.events))
	for i := range lp.events {
		events = append(events, &lp.events[i])
	}

	return events
}

func (lp *leasesPlugin) SetClient(client lease.Client) {
	lp.mu.Lock()
	defer lp.mu.Unlock()
	lp.client = client
}
