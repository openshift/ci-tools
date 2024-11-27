package http

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"slices"
	"sync"

	"sigs.k8s.io/yaml"
)

var (
	trcker          tracker
	loadTrackerOnce = sync.OnceValue(loadTracker)
)

// replayTransport is an http transport layer intended to facilitate integration tests.
// Every time an http.request comes along, it checks whether a http.response has been previously cached and then:
// - returns the cached response if it exists;
// - forward the request to the inner transport if it is functioning in 'rw' mode, returns an error otherwise.
//
// When in 'rw' mode and upon a response has been received, the transport updates the cache and persists it
// on the storage.
type replayTransport struct {
	passthrough bool
	inner       http.RoundTripper
}

func (rt *replayTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := loadTrackerOnce(); err != nil {
		return nil, fmt.Errorf("load tracker: %w", err)
	}

	bodyBytes := []byte{}
	if req.Body != nil {
		body, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, fmt.Errorf("read body: %w", err)
		}
		if err := req.Body.Close(); err != nil {
			return nil, fmt.Errorf("close body: %w", err)
		}
		req.Body = io.NopCloser(bytes.NewReader(body))
		bodyBytes = body
	}

	replay := trcker.get(req, bodyBytes)
	if replay != nil {
		return &http.Response{
			StatusCode: replay.Response.Status,
			Body:       io.NopCloser(bytes.NewReader(replay.Response.Body)),
		}, nil
	}

	if !rt.passthrough {
		return nil, errors.New("passthrough: replay not found")
	}

	res, err := rt.inner.RoundTrip(req)
	if err := trcker.add(req, bodyBytes, res); err != nil {
		return nil, fmt.Errorf("update records: %w", err)
	}

	if err := trcker.store(); err != nil {
		return nil, fmt.Errorf("dump replays: %w", err)
	}

	return res, err
}

func newReplayTransport(inner http.RoundTripper, passthrough bool) *replayTransport {
	return &replayTransport{
		inner:       inner,
		passthrough: passthrough,
	}
}

type request struct {
	Method string `json:"method,omitempty"`
	Body   []byte `json:"body,omitempty"`
}

type response struct {
	Status int    `json:"status,omitempty"`
	Body   []byte `json:"body,omitempty"`
}

// replay represents a single http round-trip
type replay struct {
	Request  request  `json:"request,omitempty"`
	Response response `json:"response,omitempty"`
}

func loadTracker() error {
	replayFile, ok := os.LookupEnv("CITOOLS_REPLAYTRANSPORT_TRACKER")
	if !ok {
		return errors.New("CITOOLS_REPLAYTRANSPORT_TRACKER is missing")
	}
	if err := createReplayFile(replayFile); err != nil {
		return err
	}
	yamlBytes, err := os.ReadFile(replayFile)
	if err != nil {
		return fmt.Errorf("read file %s: %w", replayFile, err)
	}
	trcker.replayFile = replayFile
	trcker.replays = map[string]map[string][]replay{}
	if err := yaml.Unmarshal(yamlBytes, &trcker.replays); err != nil {
		return fmt.Errorf("unmarshal replays: %w", err)
	}
	return nil
}

// tracker is a singleton, thread safe cache for the replayTransport
type tracker struct {
	// replays stores requests and responses by using the following hierarchy:
	//
	// api-server.ci.openshift.com:6443:
	//   /api/v1/namespaces/ci/pods/foo-pod?bar-querystring#super-fragment:
	//     - request:
	//         method: GET
	//         body:   nil
	//       response:
	//         status: 200
	//         body:   { ... }
	//     - request:
	//         method: POST
	//         body:   { ... }
	//       response:
	//         status: 200
	//         body:   { ... }
	//
	// For each "hostname:port", for each "path?querystring#fragment", it stores a list of
	// request-response that serves as a cache.
	replays    map[string]map[string][]replay
	m          sync.RWMutex
	replayFile string
}

func (t *tracker) get(request *http.Request, body []byte) *replay {
	if request.URL == nil {
		return nil
	}

	t.m.RLock()
	defer t.m.RUnlock()

	host := request.URL.Host
	pathToReplays, ok := t.replays[host]
	if !ok {
		return nil
	}

	path := composePath(request.URL)
	replays, ok := pathToReplays[path]
	if !ok {
		return nil
	}

	for i := range replays {
		replay := &replays[i]
		if replay.Request.Method == request.Method && slices.Compare(replay.Request.Body, body) == 0 {
			return replay
		}
	}

	return nil
}

func (t *tracker) add(req *http.Request, reqBody []byte, res *http.Response) error {
	if req.URL == nil {
		return nil
	}

	host := req.URL.Host
	path := composePath(req.URL)

	resBody, err := io.ReadAll(res.Body)
	if err != nil {
		return fmt.Errorf("read response body: %w", err)
	}
	if err := res.Body.Close(); err != nil {
		return fmt.Errorf("close response body: %w", err)
	}
	res.Body = io.NopCloser(bytes.NewReader(resBody))

	rplay := replay{
		Request:  request{Method: req.Method, Body: reqBody},
		Response: response{Status: res.StatusCode, Body: resBody},
	}

	t.m.Lock()
	defer t.m.Unlock()

	pathToReplays, ok := t.replays[host]
	if !ok {
		t.replays[host] = map[string][]replay{path: {rplay}}
		return nil
	}

	replays, ok := pathToReplays[path]
	if !ok {
		pathToReplays[path] = []replay{rplay}
	}

	for i := range replays {
		replay := &replays[i]
		if replay.Request.Method == req.Method && slices.Compare(replay.Request.Body, reqBody) == 0 {
			return nil
		}
	}

	pathToReplays[path] = append(replays, rplay)
	return nil
}

func (t *tracker) store() error {
	t.m.RLock()
	defer t.m.RUnlock()
	bytes, err := yaml.Marshal(t.replays)
	if err != nil {
		return fmt.Errorf("marshal replays: %w", err)
	}
	return os.WriteFile(t.replayFile, bytes, 0644)
}

func composePath(u *url.URL) string {
	p := u.Path
	if u.RawQuery != "" {
		p += "?" + u.RawQuery
	}
	if u.RawFragment != "" {
		p += "#" + u.RawFragment
	}
	return p
}

func createReplayFile(file string) error {
	_, err := os.Stat(file)
	if err != nil && os.IsNotExist(err) {
		f, err := os.Create(file)
		if err != nil {
			return fmt.Errorf("create %s: %w", file, err)
		}
		if err := f.Close(); err != nil {
			return fmt.Errorf("close %s: %w", file, err)
		}
		return nil
	}
	return err
}

func ReplayTransport(inner http.RoundTripper) *replayTransport {
	if inner == nil {
		return nil
	}

	clientMode, ok := os.LookupEnv("CITOOLS_REPLAYTRANSPORT_MODE")
	if !ok {
		clientMode = "read"
	}

	switch clientMode {
	case "rw":
		return newReplayTransport(inner, true)
	default: // case "read":
		return newReplayTransport(inner, false)
	}
}
