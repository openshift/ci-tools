package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/sirupsen/logrus"

	pjapi "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	prowconfig "sigs.k8s.io/prow/pkg/config"
	"sigs.k8s.io/prow/pkg/github"
	"sigs.k8s.io/prow/pkg/kube"
	"sigs.k8s.io/prow/pkg/pjutil"

	"github.com/openshift/ci-tools/pkg/dispatcher"
)

func (s *server) triggerJobs(logger *logrus.Entry, pre github.PullRequestEvent, orgrepo string, presubmits []prowconfig.Presubmit, periodics []prowconfig.Periodic) []string {
	prowCfg, err := prowconfig.Load(s.prowConfigPath, "", nil, "")
	if err != nil {
		logger.WithError(err).Error("could not load prow config for ProwJob defaults")
		return nil
	}

	refs := refsFromPREvent(pre)
	var triggered []string

	for _, job := range presubmits {
		job.JobBase = jobWithDefaults(job.JobBase, orgrepo, prowCfg, s.dispatcher)
		if !strings.Contains(job.Name, "ci-operator-config-check") {
			job.Optional = true
		}
		if name, err := s.createProwJob(pjutil.PresubmitSpec(job, refs), job.JobBase); err != nil {
			logger.WithError(err).WithField("job", job.Name).Error("could not create ProwJob")
		} else {
			triggered = append(triggered, name)
		}
	}

	for _, job := range periodics {
		job.ExtraRefs = filterSelfRef(job.ExtraRefs, pre.Repo.Owner.Login, pre.Repo.Name)
		testName := periodicTestName(job.Name, pre.Repo.Owner.Login, pre.Repo.Name, pre.PullRequest.Base.Ref)
		presubmit := prowconfig.Presubmit{
			JobBase:  job.JobBase,
			Reporter: prowconfig.Reporter{Context: fmt.Sprintf("ci/prow/%s", testName)},
			Optional: true,
		}
		presubmit.JobBase = jobWithDefaults(presubmit.JobBase, orgrepo, prowCfg, s.dispatcher)
		if name, err := s.createProwJob(pjutil.PresubmitSpec(presubmit, refs), presubmit.JobBase); err != nil {
			logger.WithError(err).WithField("job", job.Name).Error("could not create ProwJob for periodic")
		} else {
			triggered = append(triggered, name)
		}
	}
	return triggered
}

func (s *server) createProwJob(spec pjapi.ProwJobSpec, job prowconfig.JobBase) (string, error) {
	pj := pjutil.NewProwJob(spec, job.Labels, job.Annotations)
	pj.Namespace = s.namespace
	return job.Name, s.pjclient.Create(context.Background(), &pj)
}

func refsFromPREvent(pre github.PullRequestEvent) pjapi.Refs {
	return pjapi.Refs{
		Org:     pre.Repo.Owner.Login,
		Repo:    pre.Repo.Name,
		BaseRef: pre.PullRequest.Base.Ref,
		BaseSHA: pre.PullRequest.Base.SHA,
		Pulls:   []pjapi.Pull{{Number: pre.Number, Author: pre.PullRequest.User.Login, SHA: pre.PullRequest.Head.SHA}},
	}
}

func jobWithDefaults(job prowconfig.JobBase, orgrepo string, prowCfg *prowconfig.Config, dc dispatcher.Client) prowconfig.JobBase {
	if dc != nil {
		if cluster, err := dc.ClusterForJob(job.Name); err == nil {
			job.Cluster = cluster
		}
	}
	if job.Cluster == "" {
		job.Cluster = kube.DefaultClusterAlias
	}
	if job.Namespace == nil || *job.Namespace == "" {
		ns := prowCfg.PodNamespace
		job.Namespace = &ns
	}
	if dc := prowCfg.Plank.GuessDefaultDecorationConfig(orgrepo, job.Cluster); dc != nil {
		if job.DecorationConfig != nil {
			job.DecorationConfig = dc.ApplyDefault(job.DecorationConfig)
		} else {
			job.DecorationConfig = dc
		}
	}
	return job
}

func (s *server) commentNewJobs(logger *logrus.Entry, org, repo string, number int, sha string, presubmits []prowconfig.Presubmit, periodics []prowconfig.Periodic, triggered []string) {
	var sb strings.Builder
	fmt.Fprintf(&sb, "New tests detected in `.ci-operator/` configs (commit %s):\n\n", shortSHA(sha))
	for _, j := range presubmits {
		fmt.Fprintf(&sb, "- `%s`\n", j.Name)
	}
	for _, j := range periodics {
		fmt.Fprintf(&sb, "- `%s` (periodic, triggered as presubmit for validation)\n", j.Name)
	}
	if len(triggered) > 0 {
		sb.WriteString("\nTriggered automatically. Ephemeral definitions written to EFS — use `/test <name>` to re-run.")
	}
	sb.WriteString(" Jobs will be cleaned up when this PR is closed.")
	s.comment(logger, org, repo, number, sb.String())
}
