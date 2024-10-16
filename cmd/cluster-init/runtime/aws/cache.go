package aws

import (
	"bytes"
	"crypto/sha512"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"k8s.io/utils/ptr"
)

type entry struct {
	res  *http.Response
	body []byte
}

type cacheTransport struct {
	cache      map[string]entry
	downstream http.RoundTripper
}

func (ct *cacheTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	key, err := cacheKey(req)
	if err != nil {
		return nil, fmt.Errorf("cache key: %w", err)
	}
	if ct.exists(key) {
		return ct.resolve(key), nil
	}
	res, err := ct.downstream.RoundTrip(req)
	// Save into the cache only if the request has succeeded
	if err == nil {
		return res, ct.cacheResponse(key, res)
	}
	return res, err
}

func (ct *cacheTransport) exists(key string) bool {
	_, found := ct.cache[key]
	return found
}

func (ct *cacheTransport) resolve(key string) *http.Response {
	entry := ct.cache[key]
	if entry.body != nil {
		entry.res.Body = io.NopCloser(bytes.NewReader(entry.body))
	}
	return entry.res
}

func (ct *cacheTransport) cacheResponse(key string, res *http.Response) error {
	var body []byte
	if res.Body != nil {
		bodyBytes, err := io.ReadAll(res.Body)
		if err != nil {
			return fmt.Errorf("read body: %w", err)
		}
		if err := res.Body.Close(); err != nil {
			return fmt.Errorf("close body: %w", err)
		}
		body = bodyBytes
		res.Body = io.NopCloser(bytes.NewReader(bodyBytes))
	}
	ct.cache[key] = entry{res: res, body: body}
	return nil
}

func cacheKey(req *http.Request) (string, error) {
	url := ptr.Deref(req.URL, url.URL{})
	payload := []byte(req.Host + url.RawPath + url.RawQuery + url.RawQuery)
	if req.Method == http.MethodPost || req.Method == http.MethodPatch || req.Method == http.MethodPut {
		if req.Body != nil {
			bodyBytes, err := io.ReadAll(req.Body)
			if err != nil {
				return "", fmt.Errorf("read body: %w", err)
			}
			if err := req.Body.Close(); err != nil {
				return "", fmt.Errorf("close body: %w", err)
			}
			payload = append(payload, bodyBytes...)
			req.Body = io.NopCloser(bytes.NewReader(bodyBytes))
		}
	}
	return fmt.Sprintf("%x", sha512.Sum512(payload)), nil
}

func CacheTransport(downstream http.RoundTripper) *cacheTransport {
	return &cacheTransport{
		cache:      make(map[string]entry),
		downstream: downstream,
	}
}
