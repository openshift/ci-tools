package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"

	"github.com/sirupsen/logrus"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/release"
	"github.com/openshift/ci-tools/pkg/release/candidate"
)

func endpoint(c api.Candidate) string {
	return candidate.Endpoint(c.ReleaseDescriptor, c.Version+".0-0.", string(c.Stream), "/config")
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

	logrus.Infof("Requesting a release controller's jobs in config from %s", req.URL.String())
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to request release controller's jobs in config: %w", err)
	}
	if resp == nil {
		return nil, errors.New("failed to request latest release: got a nil response")
	}
	defer resp.Body.Close()
	data, readErr := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to request latest release: server responded with %d: %s", resp.StatusCode, data)
	}
	if readErr != nil {
		return nil, fmt.Errorf("failed to read response body: %w", readErr)
	}
	verify := map[string]VerifyItem{}
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
		j := verify[k].ProwJob
		if verify[k].AggregatedProwJob != nil {
			j.AggregatedCount = verify[k].AggregatedProwJob.AnalysisJobCount
		}
		jobs = append(jobs, j)
	}
	return jobs, nil
}
