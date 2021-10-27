package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"sort"

	"github.com/sirupsen/logrus"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/release"
	"github.com/openshift/ci-tools/pkg/release/candidate"
)

func endpoint(c api.Candidate) string {
	return fmt.Sprintf("%s/%s.0-0.%s%s/config", candidate.ServiceHost(c.Product, c.Architecture), c.Version, c.Stream, candidate.Architecture(c.Architecture))
}

type Job struct {
	Name                 string `json:"name"`
	api.MetadataWithTest `json:",inline"`
}

type Verify map[string]VerifyItem

type VerifyItem struct {
	ProwJob Job `json:"prowJob"`
}

type JobType string

const (
	Informing JobType = "informing"
	Blocking  JobType = "blocking"
	Periodics JobType = "periodics"
	All       JobType = "all"
)

func ResolveJobs(client release.HTTPClient, c api.Candidate, jobType JobType) ([]Job, error) {
	return resolveJobs(client, endpoint(candidate.DefaultFields(c)), jobType)
}

func resolveJobs(client release.HTTPClient, endpoint string, jobType JobType) ([]Job, error) {
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	q := req.URL.Query()
	q.Add("jobType", string(jobType))
	req.URL.RawQuery = q.Encode()

	logrus.Debugf("Requesting a release controller's jobs in config from %s", req.URL.String())
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to request release controller's jobs in config: %w", err)
	}
	if resp == nil {
		return nil, errors.New("failed to request latest release: got a nil response")
	}
	defer resp.Body.Close()
	data, readErr := ioutil.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to request latest release: server responded with %d: %s", resp.StatusCode, data)
	}
	if readErr != nil {
		return nil, fmt.Errorf("failed to read response body: %w", readErr)
	}
	verify := Verify{}
	err = json.Unmarshal(data, &verify)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal release controll's jobs in config: %w (%s)", err, data)
	}
	keys := make([]string, 0, len(verify))
	for k := range verify {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var jobs []Job
	for _, k := range keys {
		jobs = append(jobs, verify[k].ProwJob)
	}
	return jobs, nil
}
