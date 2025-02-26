package main

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/release"
	"github.com/openshift/ci-tools/pkg/release/config"
)

type releaseControllerJobResolver struct {
	httpClient release.HTTPClient
}

func newReleaseControllerJobResolver(httpClient release.HTTPClient) jobResolver {
	return &releaseControllerJobResolver{httpClient: httpClient}
}

// resolve will resolve the jobs of the given parameters from the release-controller.
// If there is an env var actively configured to skip the job, it will not be included in the returned list
func (r *releaseControllerJobResolver) resolve(ocp string, releaseType api.ReleaseStream, jobType config.JobType) ([]config.Job, error) {
	if releaseType != api.ReleaseStreamNightly && releaseType != api.ReleaseStreamCI {
		return nil, fmt.Errorf("release type is not supported: %s", releaseType)
	}

	if jobType != config.Informing && jobType != config.Blocking && jobType != config.Periodics && jobType != config.All {
		return nil, fmt.Errorf("job type is not supported: %s", jobType)
	}
	jobs, err := config.ResolveJobs(r.httpClient, api.Candidate{
		ReleaseDescriptor: api.ReleaseDescriptor{
			Product:      api.ReleaseProductOCP,
			Architecture: api.ReleaseArchitectureAMD64,
		},
		Stream:  releaseType,
		Version: ocp,
	}, jobType)
	if err != nil {
		return nil, err
	}
	jobSkips, err := determineJobSkips(time.Now())
	if err != nil {
		return nil, fmt.Errorf("could not determine job skips: %v", err)
	}
	if len(jobSkips) == 0 {
		return jobs, nil
	}

	var filteredJobs []config.Job
	for _, job := range jobs {
		if shouldFilterOutJob(job.Name, jobSkips) {
			continue
		}

		filteredJobs = append(filteredJobs, job)
	}

	return filteredJobs, nil
}

type jobSkip struct {
	regex      regexp.Regexp
	expiration time.Time
}

func (js jobSkip) String() string {
	return fmt.Sprintf("%s@%s", js.regex.String(), js.expiration.Format(time.RFC3339))
}

func shouldFilterOutJob(jobName string, jobSkips []jobSkip) bool {
	for _, js := range jobSkips {
		regex := js.regex
		if time.Now().After(js.expiration) {
			logrus.Warnf("A configured job skip for %s has expired, it should be removed or extended", regex.String())
			continue
		}
		if regex.MatchString(jobName) {
			logrus.Infof("Skipping job %s, due to job skip %s", jobName, regex.String())
			return true
		}
	}

	return false
}

const (
	regexPrefix      = "SKIP_JOB_REGEX_"
	expirationPrefix = "SKIP_JOB_EXPIRE_"
)

// determineJobSkips will find the configured job skips based on env vars.
// Each skip must include 2 env vars where "123" is a sequential number:
//  1. SKIP_JOB_REGEX_123 containing the regex to match job name(s) to skip
//  2. SKIP_JOB_EXPIRE_123 containing the expiration date after which this skip will no longer apply
func determineJobSkips(now time.Time) ([]jobSkip, error) {
	var jobSkips []jobSkip
	for _, env := range os.Environ() {
		envVarParts := strings.SplitN(env, "=", 2)
		key := envVarParts[0]
		rawRegex := envVarParts[1]

		if strings.HasPrefix(key, regexPrefix) {
			index := strings.TrimPrefix(key, regexPrefix)
			expireEnvVar := os.Getenv(fmt.Sprintf("%s%s", expirationPrefix, index))
			if expireEnvVar == "" {
				// If there is no matching expiration, it should always result in an active skip
				expireEnvVar = now.Add(time.Hour).Format(time.RFC3339)
			}
			expiration, err := time.Parse(time.RFC3339, expireEnvVar)
			if err != nil {
				return nil, fmt.Errorf("failed to parse expiration time %q: %v", expireEnvVar, err)
			}
			regex, err := regexp.Compile(rawRegex)
			if err != nil {
				return nil, fmt.Errorf("could not compile job regexp %s: %v", rawRegex, err)
			}
			jobSkips = append(jobSkips, jobSkip{
				regex:      *regex,
				expiration: expiration,
			})
		}
	}

	return jobSkips, nil
}
