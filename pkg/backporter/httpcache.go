package backporter

import (
	"bufio"
	"bytes"
	"fmt"
	"net/http"
	"net/http/httputil"
	"sync"

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
func newBugzillaCache() *bugzillaCache {
	return &bugzillaCache{cache: map[string][]byte{}}
}

// CachedTransport is an implementation http.RoundTripper
// which first checks for cached values
type CachedTransport struct {
	mirror    *bugzillaCache
	Transport http.RoundTripper
}

// RoundTrip will first check if there are any cached responses and return that
// if not it will make an HTTP call using the DefaultTransport
func (t *CachedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method != "GET" {
		return http.DefaultTransport.RoundTrip(req)
	}
	var err error
	var resp *http.Response
	g := new(errgroup.Group)
	var wg sync.WaitGroup
	wg.Add(1)
	g.Go(func() error {
		defer wg.Done()
		resp, err = http.DefaultTransport.RoundTrip(req)
		if err != nil {
			return err
		}
		body, err := httputil.DumpResponse(resp, true)
		if err != nil {
			return fmt.Errorf("err while prepping response to cache: %v", err)
		}
		t.mirror.Set(req.URL.String(), body)
		return nil
	})

	if cachedVal, isCached := t.mirror.Get(req.URL.String()); isCached {
		b := bytes.NewBuffer(cachedVal)
		return http.ReadResponse(bufio.NewReader(b), req)
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	return resp, nil
}

// Client returns an HTTP Client with the CachedTransport as the Transport
func (t *CachedTransport) Client() *http.Client {
	return &http.Client{
		Transport: t,
	}
}

// NewCachedTransport is a constructor for CachedTransport
func NewCachedTransport() *CachedTransport {
	return &CachedTransport{
		mirror: newBugzillaCache(),
	}
}
