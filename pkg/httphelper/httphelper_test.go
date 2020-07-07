package httphelper

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestWriteHeader(t *testing.T) {
	testcases := []struct {
		name       string
		statusCode int
	}{
		{
			"StatusOK",
			http.StatusOK,
		},
		{
			"StatusNotFound",
			http.StatusNotFound,
		},
	}
	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			trw := &TraceResponseWriter{ResponseWriter: rr, statusCode: http.StatusOK}
			trw.WriteHeader(tc.statusCode)
			if rr.Code != tc.statusCode {
				t.Errorf("mismatch between expected and actual response headers: expected %s, got %s", http.StatusText(tc.statusCode), http.StatusText(rr.Code))
			}
			if trw.statusCode != tc.statusCode {
				t.Errorf("mismatch between expected and actual TraceResponseWriter headers: expected %s, got %s", http.StatusText(tc.statusCode), http.StatusText(trw.statusCode))
			}
		})

	}

}

func TestWrite(t *testing.T) {
	testcases := []struct {
		name         string
		responseBody string
	}{
		{
			"SimpleText",
			"Simple text to test respone size",
		},
	}
	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			trw := &TraceResponseWriter{ResponseWriter: rr, statusCode: http.StatusOK}
			resp := []byte(tc.responseBody)
			trw.Write(resp)
			if rr.Body.String() != tc.responseBody {
				t.Errorf("mismatch between expected and actual response body: expected %s, got %s", tc.responseBody, rr.Body.String())
			}
			if trw.size != len(resp) {
				t.Errorf("mismatch between expected and actual TraceResponseWriter size: expected %d, got %d", len(resp), trw.size)
			}
		})
	}
}

var metrics = NewMetrics("testnamespace")

func TestRecordError(t *testing.T) {
	testcases := []struct {
		name          string
		expectedError string
		expectedCount int
		expectedOut   string
	}{
		{
			name:          "Simple Error String",
			expectedError: "sample error message to ensure proper working",
			expectedOut: `# HELP testnamespace_error_rate number of errors, sorted by label/type
					   # TYPE testnamespace_error_rate counter
					   testnamespace_error_rate{error="sample error message to ensure proper working"} 1
					   `,
		},
	}
	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			if metrics.ErrorRate == nil {
				t.Errorf("errorRate not initialized")
			}
			metrics.RecordError(tc.expectedError)
			if err := testutil.CollectAndCompare(metrics.ErrorRate, strings.NewReader(tc.expectedOut)); err != nil {
				t.Errorf("unexpected metrics for ErrorRate:\n%s", err)
			}

		})
	}
}
func oneByteWriter(w http.ResponseWriter, r *http.Request) {
	oneByteLength := []byte{'1'}
	w.Write(oneByteLength)
}
func halfSecLatency(_ time.Time) time.Duration {
	return time.Millisecond * 500
}
func TestHandleWithMetricsCustomTimer(t *testing.T) {
	testcases := []struct {
		name                    string
		customTimer             func(time.Time) time.Duration
		dummyWriter             func(w http.ResponseWriter, r *http.Request)
		expectedResponseTimeOut string
		expectedResponseSizeOut string
	}{
		{
			name:        "Simple Error String",
			customTimer: halfSecLatency,
			dummyWriter: oneByteWriter,
			expectedResponseTimeOut: `# HELP testnamespace_http_request_duration_seconds http request duration in seconds
            # TYPE testnamespace_http_request_duration_seconds histogram
            testnamespace_http_request_duration_seconds_bucket{path="",status="200",le="0.0005"} 0
            testnamespace_http_request_duration_seconds_bucket{path="",status="200",le="0.001"} 0
            testnamespace_http_request_duration_seconds_bucket{path="",status="200",le="0.0025"} 0
            testnamespace_http_request_duration_seconds_bucket{path="",status="200",le="0.005"} 0
            testnamespace_http_request_duration_seconds_bucket{path="",status="200",le="0.01"} 0
            testnamespace_http_request_duration_seconds_bucket{path="",status="200",le="0.025"} 0
            testnamespace_http_request_duration_seconds_bucket{path="",status="200",le="0.05"} 0
            testnamespace_http_request_duration_seconds_bucket{path="",status="200",le="0.1"} 0
            testnamespace_http_request_duration_seconds_bucket{path="",status="200",le="0.25"} 0
            testnamespace_http_request_duration_seconds_bucket{path="",status="200",le="0.5"} 1
            testnamespace_http_request_duration_seconds_bucket{path="",status="200",le="1"} 1
            testnamespace_http_request_duration_seconds_bucket{path="",status="200",le="2"} 1
            testnamespace_http_request_duration_seconds_bucket{path="",status="200",le="+Inf"} 1
            testnamespace_http_request_duration_seconds_sum{path="",status="200"} 0.5
            testnamespace_http_request_duration_seconds_count{path="",status="200"} 1
					  `,
			expectedResponseSizeOut: `
			# HELP testnamespace_http_response_size_bytes http response size in bytes
            # TYPE testnamespace_http_response_size_bytes histogram
            testnamespace_http_response_size_bytes_bucket{path="",status="200",le="256"} 1
            testnamespace_http_response_size_bytes_bucket{path="",status="200",le="512"} 1
            testnamespace_http_response_size_bytes_bucket{path="",status="200",le="1024"} 1
            testnamespace_http_response_size_bytes_bucket{path="",status="200",le="2048"} 1
            testnamespace_http_response_size_bytes_bucket{path="",status="200",le="4096"} 1
            testnamespace_http_response_size_bytes_bucket{path="",status="200",le="6144"} 1
            testnamespace_http_response_size_bytes_bucket{path="",status="200",le="8192"} 1
            testnamespace_http_response_size_bytes_bucket{path="",status="200",le="10240"} 1
            testnamespace_http_response_size_bytes_bucket{path="",status="200",le="12288"} 1
            testnamespace_http_response_size_bytes_bucket{path="",status="200",le="16384"} 1
            testnamespace_http_response_size_bytes_bucket{path="",status="200",le="24576"} 1
            testnamespace_http_response_size_bytes_bucket{path="",status="200",le="32768"} 1
            testnamespace_http_response_size_bytes_bucket{path="",status="200",le="40960"} 1
            testnamespace_http_response_size_bytes_bucket{path="",status="200",le="49152"} 1
            testnamespace_http_response_size_bytes_bucket{path="",status="200",le="57344"} 1
            testnamespace_http_response_size_bytes_bucket{path="",status="200",le="65536"} 1
            testnamespace_http_response_size_bytes_bucket{path="",status="200",le="+Inf"} 1
            testnamespace_http_response_size_bytes_sum{path="",status="200"} 1
            testnamespace_http_response_size_bytes_count{path="",status="200"} 1
			`,
		},
	}
	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			if metrics.HTTPResponseSize == nil {
				t.Errorf("HTTPResponseSize not initialized")
			}
			if metrics.HTTPRequestDuration == nil {
				t.Errorf("HTTPRequestDuration not initialized")
			}
			handler := metrics.HandleWithMetricsCustomTimer(tc.dummyWriter, tc.customTimer)
			rr := httptest.NewRecorder()
			req, err := http.NewRequest("GET", "http://example.com", nil)
			if err != nil {
				t.Errorf("error while creating dummy request: %w", err)
			}
			handler(rr, req)
			if err := testutil.CollectAndCompare(metrics.HTTPResponseSize, strings.NewReader(tc.expectedResponseSizeOut)); err != nil {
				t.Errorf("unexpected metrics for HTTPResponseSize:\n%s", err)
			}
			if err := testutil.CollectAndCompare(metrics.HTTPRequestDuration, strings.NewReader(tc.expectedResponseTimeOut)); err != nil {
				t.Errorf("unexpected metrics for HTTPRequestDuration:\n%s", err)
			}
		})
	}
}
