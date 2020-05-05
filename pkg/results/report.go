package results

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"net/http"

	"github.com/sirupsen/logrus"

	"github.com/openshift/ci-tools/pkg/api"
)

const (
	// reportAddress is the default result aggregator address in api.ci
	reportAddress = "http://result-aggregator-ci.svc.ci.openshift.org"
)

// Options holds the configuration options for connecting to the remote aggregation server
type Options struct {
	address  string
	certFile string
	keyFile  string
	caFile   string
}

// Bind adds flags for the options
func (o *Options) Bind(flag *flag.FlagSet) {
	flag.StringVar(&o.address, "report-address", reportAddress, "Address of the aggregate reporting server.")
	flag.StringVar(&o.certFile, "report-cert-file", "", "File holding the certificate for the aggregate reporting server.")
	flag.StringVar(&o.keyFile, "report-key-file", "", "File holding the key for the aggregate reporting server.")
	flag.StringVar(&o.caFile, "report-ca-file", "", "File holding the certificate authority for the aggregate reporting server.")
}

// Validate ensures that options are set correctly
func (o *Options) Validate() error {
	numSet := 0
	for _, field := range []string{o.certFile, o.keyFile, o.caFile} {
		if field != "" {
			numSet = numSet + 1
		}
	}

	if numSet != 0 && numSet != 3 {
		return errors.New("--report-{cert|key|cacert}-file must be set together or not at all")
	}
	return nil
}

// Client returns an HTTP or HTTPs client, based on the options
func (o *Options) Reporter(spec *api.JobSpec, consoleHost string) (Reporter, error) {
	if o.address == "" {
		return &noopReporter{}, nil
	}
	r := &reporter{
		spec:        spec,
		address:     o.address,
		consoleHost: consoleHost,
		client:      &http.Client{},
	}
	if o.certFile == "" {
		return r, nil
	}

	cert, err := tls.LoadX509KeyPair(o.certFile, o.keyFile)
	if err != nil {
		return nil, fmt.Errorf("could not load client cert: %v", err)
	}

	caCert, err := ioutil.ReadFile(o.caFile)
	if err != nil {
		return nil, fmt.Errorf("could not load CA cert: %v", err)
	}
	caCertPool := x509.NewCertPool()
	caCertPool.AppendCertsFromPEM(caCert)

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      caCertPool,
	}
	transport := &http.Transport{TLSClientConfig: tlsConfig}
	r.client = &http.Client{Transport: transport}
	return r, nil
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
	Report(err error)
}

type noopReporter struct{}

func (r *noopReporter) Report(err error) {}

type reporter struct {
	client      *http.Client
	spec        *api.JobSpec
	consoleHost string
	address     string
}

// Report sends a report for this error to an aggregation server.
// This action is best-effort and errors are logged but not exposed.
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
	}
	resp, err := r.client.Post(fmt.Sprintf("%s/result", r.address), "application/json", bytes.NewReader(data))
	if err != nil {
		logrus.Tracef("could not create report request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := ioutil.ReadAll(resp.Body)
		logrus.Tracef("response for report was not 200: %v", body)
	}
}
