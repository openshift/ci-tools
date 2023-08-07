package backporter

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httputil"
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// bugzillaCache tests
func TestBugzillaCacheSet(t *testing.T) {
	testcases := []struct {
		name         string
		cache        map[string][]byte
		key          string
		value        []byte
		postSetCache map[string][]byte
	}{
		{
			name:         "Entry is already present",
			cache:        map[string][]byte{"key": []byte("Test string")},
			key:          "key",
			value:        []byte("New data"),
			postSetCache: map[string][]byte{"key": []byte("New data")},
		},
		{
			name:         "New entry",
			cache:        map[string][]byte{},
			key:          "key",
			value:        []byte("New data"),
			postSetCache: map[string][]byte{"key": []byte("New data")},
		},
	}
	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			bc := newBugzillaCache()
			bc.cache = tc.cache
			bc.set(tc.key, tc.value)
			if diff := cmp.Diff(tc.postSetCache, bc.cache); diff != "" {
				t.Errorf("cached entry mismatch: %v", diff)
			}
		})
	}

}
func TestBugzillaCacheGet(t *testing.T) {
	testcases := []struct {
		name          string
		cache         map[string][]byte
		key           string
		expectedValue []byte
		isCached      bool
	}{
		{
			name:          "Key is present",
			cache:         map[string][]byte{"key": []byte("Test string")},
			key:           "key",
			expectedValue: []byte("Test string"),
			isCached:      true,
		},
		{
			name:     "Key is absent",
			cache:    map[string][]byte{"key": []byte("Test string")},
			key:      "absent",
			isCached: false,
		},
	}
	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			bc := newBugzillaCache()
			bc.cache = tc.cache
			cachedVal, isCached := bc.get(tc.key)
			if tc.isCached != isCached {
				t.Errorf("entry mismatch -  expected: %v, got: %v", tc.isCached, isCached)
			}
			if isCached {
				if !bytes.Equal(tc.expectedValue, cachedVal) {
					t.Errorf("cached value wrong expected: %s, got: %s", tc.expectedValue, cachedVal)
				}
			}
		})
	}
}

type fakeTransport struct {
	response *http.Response
	wait     func()
	err      error
}

func (t fakeTransport) RoundTrip(req *http.Request) (resp *http.Response, err error) {
	// We must guarantee that we don't return until after the cache request in order to keep tests stable
	if t.wait != nil {
		t.wait()
	}
	return t.response, t.err
}

func TestRoundTrip(t *testing.T) {
	bodyText := "body text"
	resp := &http.Response{
		Status:     http.StatusText(http.StatusOK),
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewBuffer([]byte(bodyText))),
	}
	body, err := httputil.DumpResponse(resp, true)
	if err != nil {
		t.Fatalf("failed to serialize dummy response: %v", err)
	}
	testcases := []struct {
		name     string
		fake     fakeTransport
		cache    map[string][]byte
		isCached bool
	}{
		{
			name: "Get payload directly from server",
			fake: fakeTransport{
				response: resp,
			},
			cache:    map[string][]byte{},
			isCached: false,
		},
		{
			name: "Get cached response",
			fake: fakeTransport{
				response: resp,
			},
			cache:    map[string][]byte{"http://somewhere.com/": body},
			isCached: true,
		},
		{
			name: "Error request to fake server",
			fake: fakeTransport{
				err: errors.New("fake error"),
			},
			cache:    map[string][]byte{},
			isCached: false,
		},
	}
	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			r, err := http.NewRequest("GET", "http://somewhere.com/", nil)
			if err != nil {
				t.Fatalf("failed to make request to fake server: %v", err)
			}
			cache := &accessTrackingCache{
				lock:  &sync.Mutex{},
				cache: &bugzillaCache{cache: tc.cache},
			}
			tc.fake.wait = func() {
				for {
					cache.lock.Lock()
					hadAccess := cache.accessCounter > 0
					cache.lock.Unlock()
					if hadAccess {
						return
					}
				}
			}
			tp := cachingTransport{
				cache:     cache,
				transport: tc.fake,
			}
			resp, err := tp.RoundTrip(r)
			if err != tc.fake.err {
				t.Errorf("wrong error - expected %v, got %v", tc.fake.err, err)
			}
			if err != nil {
				return
			}
			if resp.StatusCode != tc.fake.response.StatusCode {
				t.Errorf("incorrect status code - expected %v, got %v", tc.fake.response.StatusCode, resp.StatusCode)
			}
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatalf("failed to parse response body: %v", err)
			}
			if string(body) != bodyText {
				t.Errorf("incorrect body - expected %v, got %v", bodyText, string(body))
			}

			if isRespFromCache := resp.Header.Get(xFromCache) == "1"; isRespFromCache != tc.isCached {
				t.Errorf("expected resp to be from cache: %t, response was from cache: %t", tc.isCached, isRespFromCache)
			}

		})
	}

}

type accessTrackingCache struct {
	cache
	lock          *sync.Mutex
	accessCounter int
}

func (c *accessTrackingCache) get(key string) ([]byte, bool) {
	c.lock.Lock()
	defer c.lock.Unlock()
	c.accessCounter++
	return c.cache.get(key)
}
