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
	// reportAddress is the default result aggregator address in api.ci
	reportAddress = "https://result-aggregator-ci.apps.ci.l2s4.p1.openshiftapps.com"
)

// Options holds the configuration options for connecting to the remote aggregation server
type Options struct {
	address     string
	username    string
	password    string
	credentials string
}

// Bind adds flags for the options
func (o *Options) Bind(flag *flag.FlagSet) {
	flag.StringVar(&o.address, "report-address", reportAddress, "Address of the aggregate reporting server.")
	flag.StringVar(&o.username, "report-username", "", "Username for the aggregate reporting server.")
	flag.StringVar(&o.password, "report-password-file", "", "File holding the password for the aggregate reporting server.")
	flag.StringVar(&o.credentials, "report-credentials-file", "", "File holding the <username>:<password> for the aggregate reporting server.")
}

// Validate ensures that options are set correctly
func (o *Options) Validate() error {
	numSet := 0
	for _, field := range []string{o.username, o.password} {
		if field != "" {
			numSet = numSet + 1
		}
	}

	if numSet != 0 && numSet != 2 {
		return errors.New("--report-{username|password-file} must be set together or not at all")
	}
	return nil
}

func getUsernameAndPassword(username, password, credentials string) (string, string, error) {
	if credentials != "" {
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
	raw, err := ioutil.ReadFile(password)
	if err != nil {
		return "", "", fmt.Errorf("failed to read password file %q: %w", password, err)
	}
	return username, string(raw), nil
}

// Client returns an HTTP or HTTPs client, based on the options
func (o *Options) Reporter(spec *api.JobSpec, consoleHost string) (Reporter, error) {
	if o.address == "" || (o.password == "" && o.credentials == "") {
		return &noopReporter{}, nil
	}
	username, password, err := getUsernameAndPassword(o.username, o.password, o.credentials)
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
	request := Request{
		JobName: r.spec.Job,
		Type:    string(r.spec.Type),
		Cluster: r.consoleHost,
		State:   state,
		Reason:  FullReason(err),
	}
	data, err := json.Marshal(request)
	if err != nil {
		logrus.Tracef("could not marshal request: %v", err)
		return
	}

	reportMsg := fmt.Sprintf("Reporting job state '%s'", request.State)
	if state != StateSucceeded {
		reportMsg = fmt.Sprintf("Reporting job state '%s' with reason '%s'", request.State, request.Reason)
	}

	logrus.Infof(reportMsg)
	req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/result", r.address), bytes.NewReader(data))
	if err != nil {
		logrus.Tracef("could not create report request: %v", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth(r.username, r.password)
	resp, err := r.client.Do(req)
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
		logrus.Tracef("response for report was not 200: %v", body)
	}
}
