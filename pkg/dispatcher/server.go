package dispatcher

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
)

// defaultAggregateTemplate is the release-controller's default aggregator periodic.
const defaultAggregateTemplate = "release-openshift-release-analysis-aggregator"

type Server struct {
	pjs       *Prowjobs
	ecd       *ephemeralClusterScheduler
	dispatch  func(bool)
	snapshots *SnapshotManager
}

func NewServer(jobs *Prowjobs, ecd *ephemeralClusterScheduler, dispatch func(bool)) *Server {
	return &Server{
		pjs:      jobs,
		ecd:      ecd,
		dispatch: dispatch,
	}
}

// NewSnapshotServer creates a server that performs normal lookups from immutable snapshots.
func NewSnapshotServer(jobs *Prowjobs, ecd *ephemeralClusterScheduler, dispatch func(bool), snapshots *SnapshotManager) *Server {
	server := NewServer(jobs, ecd, dispatch)
	server.snapshots = snapshots
	return server
}

// SchedulingRequest represents the incoming request structure
type SchedulingRequest struct {
	Job string `json:"job"`
}

// Response represents the response structure
type SchedulingResponse struct {
	Cluster          string     `json:"cluster"`
	Source           string     `json:"source,omitempty"`
	PolicyGeneration uint64     `json:"policyGeneration,omitempty"`
	PolicyDigest     string     `json:"policyDigest,omitempty"`
	OverrideID       string     `json:"overrideID,omitempty"`
	ValidUntil       *time.Time `json:"validUntil,omitempty"`
	Explanation      string     `json:"explanation,omitempty"`
}

// RequestHandler handles scheduling requests for jobs
func (s *Server) RequestHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Invalid request method", http.StatusMethodNotAllowed)
		return
	}

	if r.URL.Path != "/" {
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}

	var req SchedulingRequest
	decoder := json.NewDecoder(r.Body)
	err := decoder.Decode(&req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	cluster := ""
	response := SchedulingResponse{}
	if s.ecd.ShouldHandle(req.Job) {
		cluster, err = s.ecd.Dispatch(req.Job)
		if err != nil {
			http.Error(w, "Failed to get the cluster", http.StatusInternalServerError)
			return
		}
		response = SchedulingResponse{Cluster: cluster, Source: "ephemeral-cluster"}
	} else {
		decision, found := s.lookup(req.Job, time.Now())
		if found {
			cluster = decision.Cluster
			if s.snapshots != nil {
				observeDecision(decision)
			}
			var validUntil *time.Time
			if !decision.ValidUntil.IsZero() {
				deadline := decision.ValidUntil
				validUntil = &deadline
			}
			response = SchedulingResponse{
				Cluster: decision.Cluster, Source: decision.Source, PolicyGeneration: decision.PolicyGeneration,
				PolicyDigest: decision.PolicyDigest, OverrideID: decision.OverrideID,
				ValidUntil: validUntil, Explanation: decision.Explanation,
			}
		}
	}

	if cluster == "" {
		http.Error(w, "Cluster not found", http.StatusNotFound)
		return
	}

	if response.PolicyGeneration != 0 {
		w.Header().Set("X-Dispatcher-Policy-Generation", strconv.FormatUint(response.PolicyGeneration, 10))
		w.Header().Set("X-Dispatcher-Policy-Digest", response.PolicyDigest)
		w.Header().Set("Cache-Control", "no-store")
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		logrus.WithError(err).WithField("response", response).Error("failed to encode response")
	}
}

// lookup tries the original identity in both stores before falling back to the
// default aggregate template. It reuses policy expiry and override semantics;
// custom templates and names with an empty verification prefix are not aliases.
func (s *Server) lookup(job string, now time.Time) (Decision, bool) {
	names := []string{job}
	suffix := "-" + defaultAggregateTemplate
	if strings.HasSuffix(job, suffix) && len(job) > len(suffix) {
		names = append(names, defaultAggregateTemplate)
	}
	for _, name := range names {
		if s.snapshots != nil {
			if decision, found := s.snapshots.Lookup(name, now); found {
				return decision, true
			}
		}
		if cluster := s.pjs.GetCluster(name); cluster != "" {
			decision := Decision{Cluster: cluster}
			if s.snapshots != nil {
				decision.Source = "legacy-gob-fallback"
			}
			return decision, true
		}
	}
	return Decision{}, false
}

// HealthHandler reports process liveness without claiming policy readiness.
func (s *Server) HealthHandler(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
}

// ReadyHandler reports ready only after a complete valid snapshot is loaded.
func (s *Server) ReadyHandler(w http.ResponseWriter, _ *http.Request) {
	if s.snapshots == nil || !s.snapshots.Ready() {
		http.Error(w, "no valid policy generation loaded", http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// EventHandler handles the /event route with dispatch logic
func (s *Server) EventHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Invalid request method", http.StatusMethodNotAllowed)
		return
	}

	if r.URL.Path != "/event" {
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}

	dispatchParam := r.URL.Query().Get("dispatch")
	if dispatchParam == "" {
		http.Error(w, "Missing dispatch parameter", http.StatusBadRequest)
		return
	}

	dispatch, err := strconv.ParseBool(dispatchParam)
	if err != nil {
		http.Error(w, "Invalid dispatch parameter", http.StatusBadRequest)
		return
	}

	if dispatch {
		s.dispatch(true)
	}
}
