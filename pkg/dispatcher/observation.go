package dispatcher

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/util/sets"

	dispatcherv1 "github.com/openshift/ci-tools/pkg/api/dispatcher/v1"
)

// OverrideReader exposes durable records without invoking control reconciliation.
type OverrideReader interface {
	Overrides(context.Context) ([]dispatcherv1.DispatchOverride, error)
}

// ObservationServer exposes authenticated, read-only views of the serving policy.
type ObservationServer struct {
	manager   *SnapshotManager
	overrides OverrideReader
	token     func() []byte
	now       func() time.Time
}

// NewObservationServer creates an observer with its own read token. A nil overrides
// reader supports deployments without runtime controls; state and jobs still work.
func NewObservationServer(manager *SnapshotManager, overrides OverrideReader, token func() []byte) *ObservationServer {
	return &ObservationServer{manager: manager, overrides: overrides, token: token, now: time.Now}
}

// observationPolicy identifies the immutable generation used throughout a response.
type observationPolicy struct {
	Generation  uint64    `json:"generation"`
	Digest      string    `json:"digest"`
	Checksum    string    `json:"checksum"`
	GeneratedAt time.Time `json:"generatedAt"`
	OverrideIDs []string  `json:"overrideIDs"`
}

// observationMetadata distinguishes server observation time from policy change time.
type observationMetadata struct {
	Ready      bool               `json:"ready"`
	ObservedAt time.Time          `json:"observedAt"`
	Policy     *observationPolicy `json:"policy,omitempty"`
}

// observationLoad summarizes effective placement, including expired-policy fallback.
type observationLoad struct {
	Jobs   int     `json:"jobs"`
	Demand float64 `json:"demand"`
}

// observationCluster retains inventory entries even when no jobs use them.
type observationCluster struct {
	Name               string   `json:"name"`
	InventoryKnown     bool     `json:"inventoryKnown"`
	Provider           string   `json:"provider"`
	Capabilities       []string `json:"capabilities"`
	ConfiguredCapacity int      `json:"configuredCapacity"`
	Blocked            bool     `json:"blocked"`
	observationLoad
}

// observationState reports inventory and effective placement without consulting controls.
type observationState struct {
	observationMetadata
	observationLoad
	Clusters []observationCluster `json:"clusters"`
}

// observationJob pairs baseline job requirements with the decision currently served.
type observationJob struct {
	Name            string   `json:"name"`
	Group           string   `json:"group"`
	Capabilities    []string `json:"capabilities"`
	Demand          float64  `json:"demand"`
	BaselineCluster string   `json:"baselineCluster"`
	Decision        Decision `json:"decision"`
}

// observationJobs uses the last returned name and generation for bounded pagination.
type observationJobs struct {
	observationMetadata
	Jobs      []observationJob `json:"jobs"`
	NextAfter string           `json:"nextAfter,omitempty"`
}

// observationOverride separates durable desired/status data from actual publication.
type observationOverride struct {
	Record      dispatcherv1.DispatchOverride `json:"record"`
	Published   bool                          `json:"published"`
	ServingJobs int                           `json:"servingJobs"`
}

// observationOverrides retains publication metadata when durable listing is available.
type observationOverrides struct {
	observationMetadata
	Overrides []observationOverride `json:"overrides"`
}

// observedMetadata uses only the captured snapshot, never a second manager read.
func observedMetadata(snapshot *PolicySnapshot, now time.Time) observationMetadata {
	result := observationMetadata{Ready: snapshot != nil, ObservedAt: now}
	if snapshot != nil {
		result.Policy = &observationPolicy{Generation: snapshot.Generation, Digest: snapshot.InputDigest, Checksum: snapshot.Checksum, GeneratedAt: snapshot.GeneratedAt, OverrideIDs: snapshot.OverrideIDs}
	}
	return result
}

// ServeHTTP authenticates all routes and rejects every method that could mutate state.
func (s *ObservationServer) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	if !bearerTokenAuthorized(request, s.token) {
		writeControlError(writer, http.StatusUnauthorized, errors.New("unauthorized"))
		return
	}
	if request.Method != http.MethodGet {
		writer.Header().Set("Allow", http.MethodGet)
		writeControlError(writer, http.StatusMethodNotAllowed, errors.New("only GET is supported"))
		return
	}
	snapshot, now := s.manager.servingSnapshot(), s.now().UTC()
	switch request.URL.Path {
	case "/observe/v1/state":
		writeJSON(writer, http.StatusOK, observeState(snapshot, now))
	case "/observe/v1/jobs":
		if snapshot == nil {
			writeControlError(writer, http.StatusServiceUnavailable, errors.New("serving policy is not ready"))
			return
		}
		s.serveJobs(writer, request, snapshot, now)
	case "/observe/v1/overrides":
		s.serveOverrides(writer, request, snapshot, now)
	default:
		writeControlError(writer, http.StatusNotFound, errors.New("unknown observation route"))
	}
}

// observeState aggregates effective decisions at one time across all inventory clusters.
func observeState(snapshot *PolicySnapshot, now time.Time) observationState {
	result := observationState{observationMetadata: observedMetadata(snapshot, now), Clusters: []observationCluster{}}
	if snapshot == nil {
		return result
	}
	loads := make(map[string]observationLoad)
	jobs := make([]string, 0, len(snapshot.Assignments))
	for name := range snapshot.Assignments {
		jobs = append(jobs, name)
	}
	sort.Strings(jobs)
	for _, name := range jobs {
		assignment := snapshot.Assignments[name]
		decision, _ := snapshotDecision(snapshot, name, now)
		load := loads[decision.Cluster]
		load.Jobs++
		load.Demand += effectiveDemand(assignment.Demand)
		loads[decision.Cluster] = load
		result.Jobs++
		result.Demand += effectiveDemand(assignment.Demand)
	}
	names := sets.New[string]()
	for name := range snapshot.Inventory {
		names.Insert(name)
	}
	for name := range loads {
		names.Insert(name)
	}
	blocked := sets.New[string](snapshot.Blocked...)
	for _, name := range sets.List(names) {
		info, known := snapshot.Inventory[name]
		result.Clusters = append(result.Clusters, observationCluster{Name: name, InventoryKnown: known, Provider: info.Provider, Capabilities: info.Capabilities, ConfiguredCapacity: info.Capacity, Blocked: blocked.Has(name), observationLoad: loads[name]})
	}
	return result
}

// serveJobs validates a small generation/name continuation and filters effective decisions.
func (s *ObservationServer) serveJobs(writer http.ResponseWriter, request *http.Request, snapshot *PolicySnapshot, now time.Time) {
	query := request.URL.Query()
	limit := 100
	if value := query.Get("limit"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 || parsed > 500 {
			writeControlError(writer, http.StatusBadRequest, errors.New("limit must be between 1 and 500"))
			return
		}
		limit = parsed
	}
	after := query.Get("after")
	generation := query.Get("generation")
	if after != "" && generation == "" {
		writeControlError(writer, http.StatusBadRequest, errors.New("generation is required with after"))
		return
	}
	if generation != "" {
		parsed, err := strconv.ParseUint(generation, 10, 64)
		if err != nil {
			writeControlError(writer, http.StatusBadRequest, errors.New("invalid generation"))
			return
		}
		if parsed != snapshot.Generation {
			writeControlError(writer, http.StatusConflict, errors.New("serving policy generation changed; restart pagination"))
			return
		}
	}
	result := observationJobs{observationMetadata: observedMetadata(snapshot, now), Jobs: []observationJob{}}
	names := sortedProwJobNames(snapshot.Baseline)
	for _, name := range names {
		if name <= after || !strings.Contains(name, query.Get("name")) {
			continue
		}
		job := snapshot.Baseline[name]
		decision, exists := snapshotDecision(snapshot, name, now)
		if !exists || query.Get("group") != "" && job.Group != query.Get("group") || query.Get("cluster") != "" && decision.Cluster != query.Get("cluster") {
			continue
		}
		if len(result.Jobs) == limit {
			result.NextAfter = result.Jobs[len(result.Jobs)-1].Name
			break
		}
		result.Jobs = append(result.Jobs, observationJob{Name: name, Group: job.Group, Capabilities: job.Capabilities, Demand: effectiveDemand(job.Demand), BaselineCluster: job.Cluster, Decision: decision})
	}
	writeJSON(writer, http.StatusOK, result)
}

// serveOverrides lists durable records without reconciling or changing their status.
func (s *ObservationServer) serveOverrides(writer http.ResponseWriter, request *http.Request, snapshot *PolicySnapshot, now time.Time) {
	if s.overrides == nil {
		writeControlError(writer, http.StatusServiceUnavailable, errors.New("runtime override observation is unavailable"))
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), 5*time.Second)
	defer cancel()
	records, err := s.overrides.Overrides(ctx)
	if err != nil {
		writeControlError(writer, http.StatusServiceUnavailable, errors.New("could not list runtime overrides"))
		return
	}
	serving := make(map[string]int)
	published := sets.New[string]()
	if snapshot != nil {
		published.Insert(snapshot.OverrideIDs...)
		for name := range snapshot.Assignments {
			decision, _ := snapshotDecision(snapshot, name, now)
			if decision.OverrideID != "" {
				serving[decision.OverrideID]++
			}
		}
	}
	// Sort a separate slice so even a reader retaining its slice is not mutated.
	rows := make([]observationOverride, 0, len(records))
	for _, record := range records {
		rows = append(rows, observationOverride{Record: record, Published: published.Has(record.Spec.ID), ServingJobs: serving[record.Spec.ID]})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Record.Spec.ID < rows[j].Record.Spec.ID })
	writeJSON(writer, http.StatusOK, observationOverrides{observationMetadata: observedMetadata(snapshot, now), Overrides: rows})
}
