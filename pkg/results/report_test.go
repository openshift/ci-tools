package results

import (
	"crypto/tls"
	"errors"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	v1 "k8s.io/test-infra/prow/apis/prowjobs/v1"
	"k8s.io/test-infra/prow/pod-utils/downwardapi"

	"github.com/openshift/ci-tools/pkg/api"
)

func TestOptions_Validate(t *testing.T) {
	var testCases = []struct {
		name     string
		options  Options
		expected error
	}{
		{
			name: "nothing set is valid",
		},
		{
			name:    "only address set is valid",
			options: Options{address: "dotcom.com"},
		},
		{
			name: "everything set is valid",
			options: Options{
				address:  "dotcom.com",
				certFile: "cert.pem",
				keyFile:  "key.pem",
				caFile:   "cacert.pem",
			},
		},
		{
			name: "subset is not valid",
			options: Options{
				address:  "dotcom.com",
				certFile: "cert.pem",
				caFile:   "cacert.pem",
			},
			expected: errors.New("--report-{cert|key|cacert}-file must be set together or not at all"),
		},
	}
	for _, testCase := range testCases {
		if actual, expected := testCase.options.Validate(), testCase.expected; !reflect.DeepEqual(actual, expected) {
			t.Errorf("%s: got incorrect error from validate: expected %q got %q", testCase.name, expected, actual)
		}
	}
}

func TestReporter_Report(t *testing.T) {
	var testCases = []struct {
		name        string
		spec        *api.JobSpec
		consoleHost string
		err         error
		expected    string
	}{
		{
			name:        "nil err reports success",
			spec:        &api.JobSpec{JobSpec: downwardapi.JobSpec{Job: "runme", Type: v1.PresubmitJob}},
			consoleHost: "foo.com",
			err:         nil,
			expected:    `{"job_name":"runme","type":"presubmit","cluster":"foo.com","state":"succeeded","reason":"unknown"}`,
		},
		{
			name:        "unknown err reports failure with unknown reason",
			spec:        &api.JobSpec{JobSpec: downwardapi.JobSpec{Job: "runme", Type: v1.PresubmitJob}},
			consoleHost: "foo.com",
			err:         errors.New("something"),
			expected:    `{"job_name":"runme","type":"presubmit","cluster":"foo.com","state":"failed","reason":"unknown"}`,
		},
		{
			name:        "reasoned err reports failure with specific reason",
			spec:        &api.JobSpec{JobSpec: downwardapi.JobSpec{Job: "runme", Type: v1.PresubmitJob}},
			consoleHost: "foo.com",
			err:         ForReason("because").ForError(errors.New("oops")),
			expected:    `{"job_name":"runme","type":"presubmit","cluster":"foo.com","state":"failed","reason":"because"}`,
		},
		{
			name:        "nested reasoned err reports failure with specific reason",
			spec:        &api.JobSpec{JobSpec: downwardapi.JobSpec{Job: "runme", Type: v1.PresubmitJob}},
			consoleHost: "foo.com",
			err:         ForReason("because").WithError(ForReason("something").ForError(errors.New("oops"))).Errorf("argh"),
			expected:    `{"job_name":"runme","type":"presubmit","cluster":"foo.com","state":"failed","reason":"because:something"}`,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			testServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Content-Type") != "application/json" {
					t.Error("did not correctly set content-type header for JSON")
					http.Error(w, "403 Forbidden", http.StatusForbidden)
					return
				}
				if r.Method != http.MethodPost {
					t.Errorf("incorrect method to update a bug: %s", r.Method)
					http.Error(w, "400 Bad Request", http.StatusBadRequest)
					return
				}
				if !strings.HasPrefix(r.URL.Path, "/result") {
					t.Errorf("incorrect path to update a bug: %s", r.URL.Path)
					http.Error(w, "400 Bad Request", http.StatusBadRequest)
					return
				}

				raw, err := ioutil.ReadAll(r.Body)
				if err != nil {
					t.Errorf("failed to read update body: %v", err)
				}
				if actual, expected := string(raw), testCase.expected; actual != expected {
					t.Errorf("got incorrect udpate: expected %v, got %v", expected, actual)
				}
			}))
			defer testServer.Close()

			reporter := reporter{
				client: &http.Client{
					Transport: &http.Transport{
						TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
					},
				},
				address:     testServer.URL,
				spec:        testCase.spec,
				consoleHost: testCase.consoleHost,
			}
			reporter.Report(testCase.err)
		})
	}
}

func TestOptions_Reporter(t *testing.T) {
	// this simulates the flow for ci-operator while we migrate to using the tool
	options := Options{} // no flags set
	reporter, err := options.Reporter(&api.JobSpec{JobSpec: downwardapi.JobSpec{Job: "runme", Type: v1.PresubmitJob}}, "http.com")
	if err != nil {
		t.Errorf("should not get an error creating a reporter, but got: %v", err)
	}

	// neither of these should not fail
	reporter.Report(nil)
	reporter.Report(ForReason("foo").ForError(errors.New("oops")))
}
