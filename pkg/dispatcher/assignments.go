package dispatcher

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"
)

// AssignmentResponse is the bulk placement view consumed by the EFS cluster writer.
// It is deliberately filtered by job config root so the writer never asks for the
// full (six-figure) assignment map.
type AssignmentResponse struct {
	Generation  uint64                        `json:"generation"`
	Digest      string                        `json:"digest"`
	Assignments map[string]SnapshotAssignment `json:"assignments"`
	Inventory   ClusterMap                    `json:"inventory"`
	Blocked     []string                      `json:"blocked,omitempty"`
}

const assignmentLongPollInterval = 500 * time.Millisecond

// AssignmentsHandler serves GET /assignments?root=efs&after=N.
//
// Without "after" it returns the current immutable generation immediately. With
// "after" it long-polls until a newer generation is published or the request is
// cancelled. The response is built from the in-memory snapshot; nothing is read
// from disk and placement is not recomputed.
func (s *Server) AssignmentsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Invalid request method", http.StatusMethodNotAllowed)
		return
	}
	root := r.URL.Query().Get("root")
	after, err := parseAfter(r.URL.Query().Get("after"))
	if err != nil {
		http.Error(w, "Invalid after parameter", http.StatusBadRequest)
		return
	}

	snapshot, ok := s.waitForGeneration(w, r, after)
	if !ok {
		return
	}
	writeAssignments(w, snapshot, root)
}

func parseAfter(raw string) (uint64, error) {
	if raw == "" {
		return 0, nil
	}
	return strconv.ParseUint(raw, 10, 64)
}

// waitForGeneration returns the snapshot to serve. When after is set and the
// current generation is not newer, it blocks until a newer one is published or
// the request is cancelled. A false return means a response was already written.
func (s *Server) waitForGeneration(w http.ResponseWriter, r *http.Request, after uint64) (*PolicySnapshot, bool) {
	if s.snapshots == nil {
		http.Error(w, "assignments are not available without a policy snapshot", http.StatusServiceUnavailable)
		return nil, false
	}
	snapshot := s.snapshots.servingSnapshot()
	if snapshot == nil {
		http.Error(w, "no valid policy generation loaded", http.StatusServiceUnavailable)
		return nil, false
	}
	if after == 0 || snapshot.Generation > after {
		return snapshot, true
	}
	ticker := time.NewTicker(assignmentLongPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return nil, false
		case <-ticker.C:
			snapshot = s.snapshots.servingSnapshot()
			if snapshot == nil {
				http.Error(w, "no valid policy generation loaded", http.StatusServiceUnavailable)
				return nil, false
			}
			if snapshot.Generation > after {
				return snapshot, true
			}
		}
	}
}

func writeAssignments(w http.ResponseWriter, snapshot *PolicySnapshot, root string) {
	response := AssignmentResponse{
		Generation:  snapshot.Generation,
		Digest:      snapshot.InputDigest,
		Assignments: make(map[string]SnapshotAssignment),
		Inventory:   snapshot.Inventory,
		Blocked:     snapshot.Blocked,
	}
	for name, assignment := range snapshot.Assignments {
		if root != "" && assignment.Root != root {
			continue
		}
		response.Assignments[name] = assignment
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Dispatcher-Policy-Generation", strconv.FormatUint(snapshot.Generation, 10))
	if err := json.NewEncoder(w).Encode(response); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// RequireBearerToken gates an already-built handler behind the dispatcher
// control token. A nil token factory means no token exists to check against, so
// the request is refused: caller routes must not be mounted in that case.
func RequireBearerToken(token func() []byte, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !bearerTokenAuthorized(r, token) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// NewRescanHandler exposes an authenticated request to run the delta dispatch now.
// The writer calls it after a publish so a new job does not wait for the periodic
// five-minute delta scan.
func NewRescanHandler(rescan func() error, token func() []byte) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Invalid request method", http.StatusMethodNotAllowed)
			return
		}
		if !bearerTokenAuthorized(r, token) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if err := rescan(); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	})
}
