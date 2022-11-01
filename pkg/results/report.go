package results

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"net/http"
	"strings"

	"github.com/sirupsen/logrus"

	"github.com/openshift/ci-tools/pkg/api"
)

const (
	// reportAddress is the default result aggregator address in app.ci
	reportAddress      = "https://result-aggregator-ci.apps.ci.l2s4.p1.openshiftapps.com"
	unknownConsoleHost = "unknown"
)

// Options holds the configuration options for connecting to the remote aggregation server
type Options struct {
	address     string
	credentials string
}

// Bind adds flags for the options
func (o *Options) Bind(flag *flag.FlagSet) {
	flag.StringVar(&o.address, "report-address", reportAddress, "Address of the aggregate reporting server.")
	flag.StringVar(&o.credentials, "report-credentials-file", "", "File holding the <username>:<password> for the aggregate reporting server.")
}

// Validate checks if the Options elements are empty
func (o *Options) Validate() error {
	if o.address == "" {
		return errors.New("report-address is required")
	}
	if o.credentials == "" {
		return errors.New("report-credentials-file is required")
	}
	return nil
}

func getUsernameAndPassword(credentials string) (string, string, error) {
	raw, err := ioutil.ReadFile(credentials)
	if err != nil {
		return "", "", fmt.Errorf("failed to read credentials file %q: %w", credentials, err)
	}
	splits := strings.Split(string(raw), ":")
	if len(splits) != 2 {
		return "", "", fmt.Errorf("got invalid content of report credentials file which must be of the form '<username>:<passwrod>'")
	}
	return strings.TrimSpace(splits[0]), strings.Trim(splits[1], "\n "), nil
}

// Client returns an HTTP or HTTPs client, based on the options
func (o *Options) Reporter(spec *api.JobSpec, consoleHost string) (Reporter, error) {
	if o.address == "" || o.credentials == "" {
		return &noopReporter{}, nil
	}

	if consoleHost == "" {
		consoleHost = unknownConsoleHost
	}

	username, password, err := getUsernameAndPassword(o.credentials)
	if err != nil {
		return nil, fmt.Errorf("failed to get username and password: %w", err)
	}

	return &reporter{
		spec:        spec,
		address:     o.address,
		consoleHost: consoleHost,
		client:      &http.Client{},
		username:    username,
		password:    password,
	}, nil
}

// Request holds the data used to report a result to an aggregation server
type Request struct {
	// JobName is the name of the job for which a result is being reported
	JobName string `json:"job_name"`
	// Type is the type of job ("presubmit", "postsubmit", "periodic" or "batch")
	Type string `json:"type"`
	// Cluster is the cluster's console hostname
	Cluster string `json:"cluster"`
	// State is "succeeded" or "failed"
	State string `json:"state"`
	// Reason is a colon-delimited list of reasons for failure
	Reason string `json:"reason"`
}

// PodScalerRequest holds the data from pod-scaler used to report a result to an aggregation server
type PodScalerRequest struct {
	WorkloadName     string
	WorkloadType     string
	ConfiguredMemory string
	DeterminedMemory string
}

const (
	StateSucceeded string = "succeeded"
	StateFailed    string = "failed"
)

type Reporter interface {
	// Report sends a report for this error to an aggregation server.
	// This action is best-effort and errors are logged but not exposed.
	// Err may be nil in which case a success is reported.
	Report(err error)
}

type noopReporter struct{}

func (r *noopReporter) Report(err error) {}

type reporter struct {
	client             *http.Client
	username, password string
	address            string

	spec        *api.JobSpec
	consoleHost string
}

func (r *reporter) Report(err error) {
	state := StateSucceeded
	if err != nil {
		state = StateFailed
	}
	reasons := Reasons(err)
	if len(reasons) == 0 {
		reasons = []string{string(ReasonUnknown)}
	}
	for _, reason := range reasons {
		r.report(Request{
			JobName: r.spec.Job,
			Type:    string(r.spec.Type),
			Cluster: r.consoleHost,
			State:   state,
			Reason:  reason,
		})
	}
}

func (r *reporter) report(request Request) {
	data, err := json.Marshal(request)
	if err != nil {
		logrus.Tracef("could not marshal request: %v", err)
		return
	}

	reportMsg := fmt.Sprintf("Reporting job state '%s'", request.State)
	if request.State != StateSucceeded {
		reportMsg = fmt.Sprintf("Reporting job state '%s' with reason '%s'", request.State, request.Reason)
	}

	logrus.Infof(reportMsg)
	req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/result", r.address), bytes.NewReader(data))
	if err != nil {
		logrus.Tracef("could not create report request: %v", err)
		return
	}
	sendRequest(req, r.client, r.username, r.password)
}

type PodScalerReporter interface {
	ReportMemoryConfigurationWarning(workloadName, workloadType, configuredMemory, determinedMemory string)
}

type podScalerReporter struct {
	client             *http.Client
	username, password string
	address            string
}

func (o *Options) PodScalerReporter() (PodScalerReporter, error) {
	username, password, err := getUsernameAndPassword(o.credentials)
	if err != nil {
		return nil, fmt.Errorf("failed to get username and password: %w", err)
	}

	return &podScalerReporter{
		client:   &http.Client{},
		username: username,
		password: password,
		address:  o.address,
	}, nil
}

// ReportMemoryConfigurationWarning is used to send the information about memory configuration
// from pod-scaler-admission to result-aggregator.
func (r *podScalerReporter) ReportMemoryConfigurationWarning(workloadName, workloadType, configuredMemory, determinedMemory string) {
	request := PodScalerRequest{
		WorkloadName:     workloadName,
		WorkloadType:     workloadType,
		ConfiguredMemory: configuredMemory,
		DeterminedMemory: determinedMemory,
	}

	data, err := json.Marshal(request)
	if err != nil {
		logrus.Tracef("could not marshal pod-scaler request: %v", err)
		return
	}

	httpRequest, err := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/pod-scaler", r.address), bytes.NewReader(data))
	if err != nil {
		logrus.Tracef("could not create pod-scaler request: %v", err)
		return
	}

	sendRequest(httpRequest, r.client, r.username, r.password)
}

func sendRequest(req *http.Request, client *http.Client, username, password string) {
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth(username, password)
	resp, err := client.Do(req)
	if err != nil {
		logrus.Tracef("could not send report request: %v", err)
		return
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			logrus.Tracef("could not close report response: %v", err)
		}
	}()
	if resp != nil && resp.StatusCode != http.StatusOK {
		body, _ := ioutil.ReadAll(resp.Body)
		logrus.Tracef("response for report was not 200: %v", string(body))
	}
}
