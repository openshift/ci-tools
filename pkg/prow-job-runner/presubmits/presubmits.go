// Package presubmits resolves ci-operator test coordinates into presubmits configured for a PR.
package presubmits

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/sirupsen/logrus"

	"k8s.io/apimachinery/pkg/util/sets"
	prowapi "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	prowconfig "sigs.k8s.io/prow/pkg/config"
	"sigs.k8s.io/prow/pkg/pjutil"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/config"
	"github.com/openshift/ci-tools/pkg/jobconfig"
	"github.com/openshift/ci-tools/pkg/prowgen"
)

// resolve binds the matching presubmit from the requested branch shard to the PR refs.
func resolve(jobs []prowconfig.Presubmit, source api.MetadataWithTest, refs *prowapi.Refs, schedulerEnabled bool) (*prowapi.ProwJob, error) {
	name := source.JobName(jobconfig.PresubmitPrefix)
	shard := filepath.Base(source.JobFilePath("presubmits"))
	var match *prowconfig.Presubmit
	for i := range jobs {
		job := &jobs[i]
		if job.Name != name || filepath.Base(job.SourcePath) != shard {
			continue
		}
		if job.Labels[jobconfig.ProwJobLabelVariant] != source.Variant {
			continue
		}
		if match != nil {
			return nil, fmt.Errorf("ambiguous test: multiple presubmits named %q in %s", name, shard)
		}
		match = job
	}
	if match == nil {
		return nil, errors.New("no matching presubmit")
	}
	return configurePresubmit(match, source, refs, schedulerEnabled)
}

// ForTests loads the release Prow configuration and returns jobs for the validated tests in plan order.
func ForTests(releaseRepo string, tests []api.MetadataWithTest, refs *prowapi.Refs) ([]*prowapi.ProwJob, error) {
	if len(tests) == 0 {
		return nil, nil
	}
	if refs == nil {
		return nil, errors.New("pull request refs are required")
	}
	shards := sets.New[string]()
	for _, test := range tests {
		shards.Insert(filepath.Join(releaseRepo, config.JobConfigInRepoPath, test.JobFilePath("presubmits")))
	}
	configPath := filepath.Join(releaseRepo, config.ConfigInRepoPath)
	jobFiles := sets.List(shards)
	logrus.WithFields(logrus.Fields{"config": configPath, "job_files": jobFiles, "test_count": len(tests)}).Info("Loading presubmits from release checkout")
	var agent prowconfig.Agent
	if err := agent.Start(configPath, jobFiles, []string{filepath.Dir(configPath)}, config.SupplementalProwConfigFileName); err != nil {
		return nil, fmt.Errorf("loading Prow config from %s: %w", releaseRepo, err)
	}
	cfg := agent.Config()
	result := make([]*prowapi.ProwJob, 0, len(tests))
	for _, source := range tests {
		job, err := resolve(cfg.GetPresubmitsStatic(source.Org+"/"+source.Repo), source, refs, cfg.Scheduler.Enabled)
		if err != nil {
			return nil, fmt.Errorf("resolving test %q from %s: %w", source.Test, source.AsString(), err)
		}
		result = append(result, job)
	}
	return result, nil
}

// configurePresubmit binds a presubmit to the PR refs, importing the test only for non-native jobs.
func configurePresubmit(job *prowconfig.Presubmit, source api.MetadataWithTest, refs *prowapi.Refs, schedulerEnabled bool) (*prowapi.ProwJob, error) {
	native := source.Org == refs.Org && source.Repo == refs.Repo && (source.Branch == refs.BaseRef ||
		(strings.HasPrefix(refs.BaseRef, source.Branch+"-") && job.Brancher.ShouldRun(refs.BaseRef)))
	spec := pjutil.PresubmitSpec(*job, *refs)
	if !native {
		// Source-specific checkout settings, such as clone_uri, must not redirect the PR checkout.
		spec.Refs = refs
	}
	pj := pjutil.NewProwJob(spec, job.Labels, job.Annotations, pjutil.RequireScheduling(schedulerEnabled))
	logrus.WithFields(api.LogFieldsFor(source.Metadata)).WithFields(logrus.Fields{
		"test": source.Test, "job": job.Name, "native": native,
		"cluster": pj.Spec.Cluster, "state": pj.Status.State,
	}).Info("Configuring presubmit")
	pod := pj.Spec.PodSpec
	if native || pod == nil || len(pod.Containers) == 0 {
		return &pj, nil
	}
	if slices.Contains(pod.Containers[0].Command, "ci-operator") {
		if err := prowgen.InjectTestFrom(&source)(pod); err != nil {
			return nil, fmt.Errorf("injecting test into presubmit %s: %w", job.Name, err)
		}
	}
	return &pj, nil
}
