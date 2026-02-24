package proxy

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/sirupsen/logrus"

	"github.com/openshift/ci-tools/pkg/lease"
)

const (
	acquireEndpoint = "/lease/acquire"
	releaseEndpoint = "/lease/release"
)

type acquireParams struct {
	rtype string
	count uint
}

type AcquireResponse struct {
	Names []string `json:"names"`
}

func parseAcquireParams(r *http.Request) (acquireParams, error) {
	params := acquireParams{}

	rtype := r.URL.Query().Get("type")
	if rtype == "" {
		return params, errors.New("type is required")
	}
	params.rtype = rtype

	count := r.URL.Query().Get("count")
	if count == "" {
		params.count = 1
	} else {
		c, err := strconv.ParseUint(count, 10, strconv.IntSize)
		if err != nil {
			return params, fmt.Errorf("parameter \"count\" is not valid: %s", count)
		}
		params.count = uint(c)
	}

	return params, nil
}

func parseReleaseParams(r *http.Request) (string, error) {
	name := r.URL.Query().Get("name")
	if name == "" {
		return "", errors.New("name is required")
	}
	return name, nil
}

type NewLeaseClientFunc func() lease.Client

// Proxy is a proxy that forwards requests to the real lease server.
// This serve as mean for the multistage steps to deals with leases.
// It is supposed to be a quite dumb server, the actual work is delegated
// to the lease client.
type Proxy struct {
	logger          *logrus.Entry
	leaseClientFunc NewLeaseClientFunc
}

func (p *Proxy) acquire(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		p.logger.Warnf("Lease acquire method %s is not allowed", r.Method)
		msg := fmt.Sprintf("Method %v not allowed, POST requests only.", r.Method)
		http.Error(w, msg, http.StatusMethodNotAllowed)
		return
	}

	params, err := parseAcquireParams(r)
	if err != nil {
		p.logger.WithError(err).Warn("Failed to parse lease acquire params")
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if params.count == 0 {
		p.writeAcquireResponse(w, []string{})
		return
	}

	c := p.leaseClientFunc()
	if c == nil {
		p.logger.Error("Failed to get lease client")
		http.Error(w, "Failed to get lease client", http.StatusInternalServerError)
		return
	}

	var names []string
	names, err = c.Acquire(params.rtype, params.count, r.Context(), func() {})
	if err != nil {
		p.logger.WithError(err).Warnf("Failed to acquire lease %q", params.rtype)
		code := http.StatusInternalServerError
		if errors.Is(err, lease.ErrTypeNotFound) {
			code = http.StatusNotFound
		}
		msg := fmt.Sprintf("Failed to acquire lease %q: %s", params.rtype, err.Error())
		http.Error(w, msg, code)
		return
	}

	p.writeAcquireResponse(w, names)
}

func (p *Proxy) writeAcquireResponse(w http.ResponseWriter, names []string) {
	namesBytes, err := json.Marshal(AcquireResponse{Names: names})
	if err != nil {
		p.logger.WithError(err).Warnf("Failed to marshal the response %s", err)
		msg := fmt.Sprintf("Failed to marshal the response %s", err.Error())
		http.Error(w, msg, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if _, err := w.Write(namesBytes); err != nil {
		p.logger.WithError(err).Warn("Failed to write leases response")
		http.Error(w, "Failed to write response", http.StatusInternalServerError)
		return
	}
}

func (p *Proxy) release(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		p.logger.Warnf("Lease release method %s is not allowed", r.Method)
		msg := fmt.Sprintf("Method %v not allowed, POST requests only.", r.Method)
		http.Error(w, msg, http.StatusMethodNotAllowed)
		return
	}

	name, err := parseReleaseParams(r)
	if err != nil {
		p.logger.WithError(err).Warn("Failed to parse lease release params")
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	c := p.leaseClientFunc()
	if c == nil {
		p.logger.Error("Failed to get lease client")
		http.Error(w, "Failed to get lease client", http.StatusInternalServerError)
		return
	}

	if err := c.Release(name); err != nil {
		p.logger.WithError(err).Warnf("Failed to release lease %q", name)
		msg := fmt.Sprintf("Failed to release lease %q: %s", name, err.Error())
		http.Error(w, msg, http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// RegisterHandlers adds to the multiplexer the HTTP endpoints served by this proxy.
func (p *Proxy) RegisterHandlers(srvMux *http.ServeMux) {
	srvMux.Handle(acquireEndpoint, http.HandlerFunc(p.acquire))
	srvMux.Handle(releaseEndpoint, http.HandlerFunc(p.release))
}

func New(logger *logrus.Entry, leaseClientFunc NewLeaseClientFunc) *Proxy {
	return &Proxy{
		logger:          logger,
		leaseClientFunc: leaseClientFunc,
	}
}
