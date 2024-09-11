package main

import (
	"encoding/json"
	"net/http"
	"regexp"

	"github.com/sirupsen/logrus"
)

type Server struct {
	pjs *prowjobs
}

func newServer(jobs *prowjobs) *Server {
	return &Server{
		pjs: jobs,
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

func (s *Server) requestHandler(w http.ResponseWriter, r *http.Request) {
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

	cluster := s.pjs.get(removeRehearsePrefix(req.Job))
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
