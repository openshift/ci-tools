package backporter

import (
	"bufio"
	"bytes"
	"fmt"
	"net/http"
	"net/http/httputil"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	"golang.org/x/sync/errgroup"
)

type bugzillaCache struct {
	lock  sync.Mutex
	cache map[string][]byte
}

func (bc *bugzillaCache) Get(key string) ([]byte, bool) {
	bc.lock.Lock()
	defer bc.lock.Unlock()
	cachedVal, ok := bc.cache[key]
	return cachedVal, ok
}

func (bc *bugzillaCache) Set(key string, respBytes []byte) {
	bc.lock.Lock()
	defer bc.lock.Unlock()
	bc.cache[key] = respBytes
}

func (bc *bugzillaCache) Delete(key string) {
	bc.lock.Lock()
	defer bc.lock.Unlock()
	delete(bc.cache, key)
}
func newBugzillaCache() *bugzillaCache {
	return &bugzillaCache{cache: map[string][]byte{}}
}

// cachingTransport is an implementation http.RoundTripper
// which first checks for cached values
type cachingTransport struct {
	cache     *bugzillaCache
	transport http.RoundTripper
}

// RoundTrip will first check if there are any cached responses and return that
// if not it will make an HTTP call using the DefaultTransport
func (t *cachingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method != "GET" {
		return t.transport.RoundTrip(req)
	}
	var err error
	var resp *http.Response
	g := new(errgroup.Group)
	g.Go(func() error {
		resp, err = t.transport.RoundTrip(req)
		if err != nil {
			t.cache.Delete(req.URL.String())
			return err
		}
		body, err := httputil.DumpResponse(resp, true)
		if err != nil {
			t.cache.Delete(req.URL.String())
			return fmt.Errorf("err while prepping response to cache: %w", err)
		}
		if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNotFound {
			t.cache.Set(req.URL.String(), body)
		} else {
			t.cache.Delete(req.URL.String())
		}
		return nil
	})

	if cachedVal, isCached := t.cache.Get(req.URL.String()); isCached {
		b := bytes.NewBuffer(cachedVal)
		return http.ReadResponse(bufio.NewReader(b), req)
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	return resp, nil
}

func refreshCache(bc *bugzillaCache) {
	concurrencyLimiter := make(chan string, 10)
	for url := range bc.cache {
		concurrencyLimiter <- url
		go func() {
			url := <-concurrencyLimiter
			resp, err := http.Get(url)
			if err != nil {
				bc.Delete(url)
				logrus.WithError(fmt.Errorf("cache refresh error - failed to fetch %s: %w", url, err))
				return
			}
			defer resp.Body.Close()
			body, err := httputil.DumpResponse(resp, true)
			if err != nil {
				bc.Delete(url)
				logrus.WithError(fmt.Errorf("cache refresh error - DumpResponse failed %s: %w", url, err))
				return
			}
			if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNotFound {
				bc.Set(url, body)
			} else {
				bc.Delete(url)
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
	ticker := time.NewTicker(10 * time.Minute)
	go func() {
		defer ticker.Stop()
		for {
			<-ticker.C
			refreshCache(t.cache)
		}
	}()
	return &t
}
