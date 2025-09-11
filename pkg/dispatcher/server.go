package dispatcher

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/sirupsen/logrus"

	"github.com/openshift/ci-tools/pkg/controller/ephemeralcluster"
)

type Server struct {
	pjs      *Prowjobs
	ecd      *ephemeralClusterScheduler
	dispatch func(bool)
}

func NewServer(jobs *Prowjobs, ecd *ephemeralClusterScheduler, dispatch func(bool)) *Server {
	return &Server{
		pjs:      jobs,
		ecd:      ecd,
		dispatch: dispatch,
	}
}

// SchedulingRequest represents the incoming request structure
type SchedulingRequest struct {
	Job string `json:"job"`
}

// Response represents the response structure
type SchedulingResponse struct {
	Cluster string `json:"cluster"`
}

func removeRehearsePrefix(jobName string) string {
	re := regexp.MustCompile(`^rehearse-\d+-`)

	if re.MatchString(jobName) {
		return re.ReplaceAllString(jobName, "")
	}
	return jobName
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
	if strings.HasPrefix(req.Job, ephemeralcluster.ProwJobNamePrefix) {
		cluster, err = s.ecd.Dispatch(req.Job)
		if err != nil {
			http.Error(w, "Failed to get the cluster", http.StatusInternalServerError)
			return
		}
	} else {
		cluster = s.pjs.GetCluster(removeRehearsePrefix(req.Job))
	}

	if cluster == "" {
		http.Error(w, "Cluster not found", http.StatusNotFound)
		return
	}

	response := SchedulingResponse{Cluster: cluster}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		logrus.WithError(err).WithField("response", response).Error("failed to encode response")
	}
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
