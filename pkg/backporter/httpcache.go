package backporter

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httputil"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/sirupsen/logrus"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
)

const (
	xFromCache       = "X-From-Cache"
	cacheRefreshFreq = 10 * time.Minute
)

type bugzillaCache struct {
	lock  sync.Mutex
	cache map[string][]byte
}

func (bc *bugzillaCache) get(key string) ([]byte, bool) {
	bc.lock.Lock()
	defer bc.lock.Unlock()
	cachedVal, ok := bc.cache[key]
	return cachedVal, ok
}

func (bc *bugzillaCache) set(key string, respBytes []byte) {
	bc.lock.Lock()
	defer bc.lock.Unlock()
	bc.cache[key] = respBytes
}

var _ cache = &bugzillaCache{}

func newBugzillaCache() *bugzillaCache {
	return &bugzillaCache{cache: map[string][]byte{}}
}

type cache interface {
	get(string) ([]byte, bool)
	set(string, []byte)
}

// cachingTransport is an implementation http.RoundTripper
// which first checks for cached values
type cachingTransport struct {
	cache     cache
	transport http.RoundTripper
}

// RoundTrip will first check if there are any cached responses and return that
// if not it will make an HTTP call using the DefaultTransport
func (t *cachingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method != "GET" {
		return t.transport.RoundTrip(req)
	}
	var resp *http.Response
	g := errgroup.Group{}
	g.Go(func() error {
		var err error
		// Disable the bodyclose linter, in this particular case the caller is responsible
		// for closing the body.
		// nolint:bodyclose
		resp, err = t.transport.RoundTrip(req)
		if err != nil {
			return err
		}
		if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNotFound {
			body, err := httputil.DumpResponse(resp, true)
			if err != nil {
				return fmt.Errorf("err while serializing response to cache: %w", err)
			}
			t.cache.set(req.URL.String(), body)
		}

		return nil
	})

	if cachedVal, isCached := t.cache.get(req.URL.String()); isCached {
		b := bytes.NewBuffer(cachedVal)
		cachedResp, err := http.ReadResponse(bufio.NewReader(b), req)
		if err != nil {
			return nil, err
		}
		cachedResp.Header.Set(xFromCache, "1")
		return cachedResp, nil
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	return resp, nil
}

func refreshCache(bc *bugzillaCache, m prometheus.Gauge) {
	var sem = semaphore.NewWeighted(int64(10))
	ctx := context.Background()
	logrus.WithField("cache_entries", len(bc.cache)).Info("Refreshing cache")
	m.Set(float64(len(bc.cache)))
	for url := range bc.cache {
		if err := sem.Acquire(ctx, 1); err != nil {
			logrus.WithError(fmt.Errorf("failed to acquire semaphore for key %s: %w", url, err))
		}
		url := url
		go func() {
			defer sem.Release(1)
			resp, err := http.Get(url)
			if err != nil {
				logrus.WithError(fmt.Errorf("cache refresh error - failed to fetch %s: %w", url, err))
				return
			}
			defer resp.Body.Close()
			if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNotFound {
				body, err := httputil.DumpResponse(resp, true)
				if err != nil {
					logrus.WithError(fmt.Errorf("cache refresh error - DumpResponse failed %s: %w", url, err))
					return
				}
				bc.set(url, body)
			}
		}()
	}

}

// NewCachingTransport is a constructor for cachingTransport
// If an entry is present in the cache, it is immediately returned
// while also generating an async HTTP call to the bugzilla server to get the latest value
// which is stored in the cache.
// Therefore this cache does *NOT* reduce the HTTP traffic, and is only used to speed up the response.
func NewCachingTransport() http.RoundTripper {
	t := cachingTransport{
		cache:     newBugzillaCache(),
		transport: http.DefaultTransport,
	}
	cacheRefreshMetrics := prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "bugzilla_backporter_cached_bugs",
			Help: "bugs in cache to be refreshed",
		},
	)
	prometheus.MustRegister(cacheRefreshMetrics)
	ticker := time.NewTicker(cacheRefreshFreq)
	go func() {
		defer ticker.Stop()
		for {
			<-ticker.C
			refreshCache(t.cache.(*bugzillaCache), cacheRefreshMetrics)
		}
	}()
	return &t
}
