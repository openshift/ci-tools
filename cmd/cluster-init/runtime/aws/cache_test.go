package aws

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

type fakeTransport struct {
	response *http.Response
	err      error
}

func (ft *fakeTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return ft.response, ft.err
}

func TestCacheTransport(t *testing.T) {
	newRequest := func(method, url string, body io.Reader) *http.Request {
		req, _ := http.NewRequest(method, url, body)
		return req
	}

	tests := []struct {
		name       string
		req        *http.Request
		body       string
		statusCode int
		wantErr    error
	}{
		{
			name:       "Cache a successful GET request",
			req:        newRequest("GET", "http://foo.bar/super", nil),
			statusCode: http.StatusOK,
			body:       "response body",
		},
		{
			name:       "Cache a successful POST request",
			req:        newRequest("POST", "http://foo.bar/super", bytes.NewBufferString("request body")),
			statusCode: http.StatusCreated,
			body:       "created body",
		},
		{
			name:       "Do not cache a failed request",
			req:        newRequest("GET", "http://foo.bar/super", nil),
			statusCode: http.StatusInternalServerError,
			body:       "error body",
		},
		{
			name:    "Error during RoundTrip",
			req:     newRequest("GET", "http://foo.bar/super", nil),
			wantErr: http.ErrHandlerTimeout,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := &fakeTransport{
				response: &http.Response{StatusCode: tt.statusCode, Body: io.NopCloser(strings.NewReader(tt.body))},
				err:      tt.wantErr,
			}

			ct := CacheTransport(mock)
			resp, err := ct.RoundTrip(tt.req)

			if err != nil && tt.wantErr == nil {
				t.Fatalf("want err nil but got: %v", err)
			}
			if err == nil && tt.wantErr != nil {
				t.Fatalf("want err %v but nil", tt.wantErr)
			}
			if err != nil && tt.wantErr != nil {
				if tt.wantErr.Error() != err.Error() {
					t.Fatalf("expect error %q but got %q", tt.wantErr.Error(), err.Error())
				}
				return
			}

			respBody, _ := io.ReadAll(resp.Body)
			if err := resp.Body.Close(); err != nil {
				t.Fatalf("close response body: %s", err)
			}

			if diff := cmp.Diff([]byte(tt.body), respBody); diff != "" {
				t.Errorf("body differs:\n%s", diff)
			}

			if tt.statusCode != resp.StatusCode {
				t.Errorf("expected %d status code but got %d", tt.statusCode, resp.StatusCode)
			}
		})
	}
}
