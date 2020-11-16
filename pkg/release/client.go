package release

import (
	"net/http"
)

type HTTPClient interface {
	Do(*http.Request) (*http.Response, error)
}

type HTTPHandler func(*http.Request) (*http.Response, error)

func NewFakeHTTPClient(h HTTPHandler) HTTPClient {
	return &fakeHTTPClient{handler: h}
}

type fakeHTTPClient struct {
	handler HTTPHandler
}

func (f *fakeHTTPClient) Do(req *http.Request) (*http.Response, error) {
	return f.handler(req)
}
