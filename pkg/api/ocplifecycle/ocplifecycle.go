package ocplifecycle

import (
	"fmt"
	"io/ioutil"
	"sort"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v2"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
)

// LoadConfig loads the lifecycle configuration from a given localtion.
func LoadConfig(path string) (Config, error) {
	configBytes, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read lifecycle config from path %s: %w", path, err)
	}

	var lifecycleConfig Config
	if err := yaml.Unmarshal(configBytes, &lifecycleConfig); err != nil {
		return nil, fmt.Errorf("failed to deserialize the lifecycle config: %w", err)
	}

	return lifecycleConfig, nil
}

type TimelineOptions struct {
	OnlyEvents sets.String
}

func mergeOptions(opts []TimelineOptions) TimelineOptions {
	ret := TimelineOptions{}
	onlyEvents := sets.NewString()

	for _, opt := range opts {
		onlyEvents = onlyEvents.Union(opt.OnlyEvents)
	}

	ret.OnlyEvents = onlyEvents

	return ret
}

// Event holds information about the product version and the lifecycle phase.
type Event struct {
	ProductVersion string
	LifecyclePhase LifecyclePhase
}

// Timeline is a list of events.
type Timeline []Event

// TimelineByProduct is a list of events mapped by version
type TimelineByVersion map[string]Timeline

// DeterminePlaceInTime returns the the previous and the next event based on the given point in time.
func (t Timeline) DeterminePlaceInTime(now time.Time) (Event, Event) {
	before := Event{}
	after := Event{
		ProductVersion: "*",
		LifecyclePhase: LifecyclePhase{When: &metav1.Time{Time: now}},
	}

	for _, event := range t {
		lifecyclePhase := event.LifecyclePhase
		if now.Before(lifecyclePhase.When.Time) {
			after = event
			break
		}
		before = event
	}

	return before, after
}

// DeterminePlaceInTime returns pointer to the exact lifecycle phase by comparing dates
func (t Timeline) GetExactLifecyclePhase(now time.Time) *Event {
	for _, e := range t {
		lifecycleTime := e.LifecyclePhase.When.Time
		if now.Day() == lifecycleTime.Day() &&
			now.Month() == lifecycleTime.Month() &&
			now.Year() == lifecycleTime.Year() {
			return &e
		}
	}
	return nil
}

// Config is an OCP lifecycle config. It holds a top-level product key (e.G. OCP)
// that maps to versions (e.G. 4.8) and those finally include the lifecycle phases.
type Config map[string]map[string][]LifecyclePhase

// GetTimeline returns a list of events in chronological order for a given product name.
func (c Config) GetTimeline(product string, opts ...TimelineOptions) Timeline {
	options := mergeOptions(opts)

	var timeline Timeline
	for productVersion, phases := range c[product] {
		for _, phase := range phases {
			if phase.When != nil {

				if !options.OnlyEvents.Has(string(phase.Event)) {
					continue
				}

				timeline = append(timeline, Event{
					ProductVersion: productVersion,
					LifecyclePhase: phase,
				})
			}
		}
	}
	// Sort timeline from past to future events
	sort.Slice(timeline, func(i, j int) bool {
		return timeline[i].LifecyclePhase.When.Time.Before(timeline[j].LifecyclePhase.When.Time)
	})
	return timeline
}

// GetTimeline returns a list of events in chronological order for a given product name mapped by version.
func (c Config) GetTimelinesByVersion(product string) TimelineByVersion {
	timelineByVersion := make(TimelineByVersion)
	for productVersion, phases := range c[product] {
		timeline := Timeline{}
		for _, phase := range phases {
			if phase.When != nil {
				timeline = append(timeline, Event{
					ProductVersion: productVersion,
					LifecyclePhase: phase,
				})
			}
		}
		// Sort timeline from past to future events
		sort.Slice(timeline, func(i, j int) bool {
			return timeline[i].LifecyclePhase.When.Time.Before(timeline[j].LifecyclePhase.When.Time)
		})
		timelineByVersion[productVersion] = timeline
	}

	return timelineByVersion
}

// LifecyclePhase describes a phase in the release lifecycle for a version of a product.
type LifecyclePhase struct {
	// Event is the name of this phase.
	Event LifecycleEvent `json:"event"`
	// When is the moment in time when this phase begins. Optional.
	When *metav1.Time `json:"when,omitempty"`
}

// LifecycleEvent is an event in the lifecycle of a version of a product.
type LifecycleEvent string

const (
	// LifecycleEventOpen marks the moment that development branches open for changes.
	LifecycleEventOpen LifecycleEvent = "open"
	// LifecycleEventFeatureFreeze marks the moment that development branches close for new features.
	// At this point it is expected that only stabilizing bug-fixes land in the branches.
	LifecycleEventFeatureFreeze LifecycleEvent = "feature-freeze"
	// LifecycleEventCodeFreeze marks the moment that development branches close for contribution.
	// At this point it is expected that only urgent stabilizing bug-fixes land in the branches.
	LifecycleEventCodeFreeze LifecycleEvent = "code-freeze"
	// LifecycleEventGenerallyAvailable marks the moment that a version is available and the development
	// branches begin to track the next release.
	LifecycleEventGenerallyAvailable LifecycleEvent = "generally-available"
	// LifecycleEventEndOfLife marks the moment that a version is no longer supported and release branches
	// close for good for this version.
	LifecycleEventEndOfLife LifecycleEvent = "end-of-life"
	// LifecycleEventEndOfFullSupport marks the moment that a version is no longer supported fully.
	LifecycleEventEndOfFullSupport LifecycleEvent = "end-of-full-support"
	// LifecycleEventEndOfMaintenanceSupport marks the moment that a version is no longer supported.
	LifecycleEventEndOfMaintenanceSupport LifecycleEvent = "end-of-maintenance-support"
)

func (le LifecycleEvent) Validate() error {
	events := sets.NewString([]string{
		string(LifecycleEventOpen),
		string(LifecycleEventFeatureFreeze),
		string(LifecycleEventCodeFreeze),
		string(LifecycleEventGenerallyAvailable),
		string(LifecycleEventEndOfLife),
		string(LifecycleEventEndOfFullSupport),
		string(LifecycleEventEndOfMaintenanceSupport),
	}...)

	if !events.Has(string(le)) {
		return fmt.Errorf("unknown event: %s", le)
	}
	return nil
}

type MajorMinor struct {
	Major int `json:"major"`
	Minor int `json:"minor"`
}

func (m MajorMinor) Less(other MajorMinor) bool {
	if m.Major < other.Major {
		return true
	} else if m.Major > other.Major {
		return false
	}
	return m.Minor < other.Minor
}

func (m MajorMinor) GetVersion() string {
	return fmt.Sprintf("%d.%d", m.Major, m.Minor)
}

func (m MajorMinor) GetFutureVersion() string {
	return fmt.Sprintf("%d.%d", m.Major, m.Minor+1)
}

func (m MajorMinor) WithIncrementedMinor(increment int) MajorMinor {
	return MajorMinor{Major: m.Major, Minor: m.Minor + increment}
}

func (m MajorMinor) String() string {
	return fmt.Sprintf("%d.%d", m.Major, m.Minor)
}

func ParseMajorMinor(version string) (*MajorMinor, error) {
	dotSplit := strings.Split(version, ".")
	if len(dotSplit) != 2 {
		return nil, fmt.Errorf("version %s split by dot doesn't have two elements, can't be in major.minor format", version)
	}
	parsedMajor, err := strconv.ParseInt(dotSplit[0], 10, 32)
	if err != nil {
		return nil, fmt.Errorf("failed to parse %s as 32 bit base 10 int: %w", dotSplit[0], err)
	}
	parsedMinor, err := strconv.ParseInt(dotSplit[1], 10, 32)
	if err != nil {
		return nil, fmt.Errorf("failed to parse %s as 32 bit base 10 int: %w", dotSplit[1], err)
	}

	return &MajorMinor{Major: int(parsedMajor), Minor: int(parsedMinor)}, nil
}
