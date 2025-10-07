package main

import (
	"strings"

	v1 "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	"sigs.k8s.io/prow/pkg/config"
	"sigs.k8s.io/prow/pkg/github"
)

type minimalGhClient interface {
	GetPullRequest(org, repo string, number int) (*github.PullRequest, error)
	CreateComment(org, repo string, number int, comment string) error
	GetPullRequestChanges(org string, repo string, number int) ([]github.PullRequestChange, error)
	CreateStatus(org, repo, ref string, s github.Status) error
}

func sendComment(presubmits presubmitTests, pj *v1.ProwJob, ghc minimalGhClient, deleteIds func()) error {
	return sendCommentWithMode(presubmits, pj, ghc, deleteIds)
}

func sendCommentWithMode(presubmits presubmitTests, pj *v1.ProwJob, ghc minimalGhClient, deleteIds func()) error {
	testContexts, err := acquireConditionalContexts(pj, presubmits.pipelineConditionallyRequired, ghc, deleteIds)
	if err != nil {
		deleteIds()
		return err
	}

	var comment string

	var protectedCommands string
	for _, presubmit := range presubmits.protected {
		protectedCommands += "\n " + presubmit.RerunCommand
	}
	if protectedCommands != "" {
		comment += "Scheduling required tests:" + protectedCommands
	}
	if testContexts != "" {
		if protectedCommands != "" {
			comment += "\n"
		}
		comment += "\nScheduling tests matching the `pipeline_run_if_changed` or not excluded by `pipeline_skip_if_only_changed` parameters:"
		comment += testContexts
	}
	if err := ghc.CreateComment(pj.Spec.Refs.Org, pj.Spec.Refs.Repo, pj.Spec.Refs.Pulls[0].Number, comment); err != nil {
		deleteIds()
		return err
	}
	return nil
}

func acquireConditionalContexts(pj *v1.ProwJob, pipelineConditionallyRequired []config.Presubmit, ghc minimalGhClient, deleteIds func()) (string, error) {
	repoBaseRef := pj.Spec.Refs.Repo + "-" + pj.Spec.Refs.BaseRef
	var testCommands string
	if len(pipelineConditionallyRequired) != 0 {
		cfp := config.NewGitHubDeferredChangedFilesProvider(ghc, pj.Spec.Refs.Org, pj.Spec.Refs.Repo, pj.Spec.Refs.Pulls[0].Number)
		for _, presubmit := range pipelineConditionallyRequired {
			if !strings.Contains(presubmit.Name, repoBaseRef) {
				continue
			}
			// Check pipeline_run_if_changed first (takes precedence)
			if run, ok := presubmit.Annotations["pipeline_run_if_changed"]; ok && run != "" {
				psList := []config.Presubmit{presubmit}
				psList[0].RegexpChangeMatcher = config.RegexpChangeMatcher{RunIfChanged: run}
				if err := config.SetPresubmitRegexes(psList); err != nil {
					deleteIds()
					return "", err
				}
				_, shouldRun, err := psList[0].RegexpChangeMatcher.ShouldRun(cfp)
				if err != nil {
					deleteIds()
					return "", err
				}
				if shouldRun {
					testCommands += "\n" + presubmit.RerunCommand
				}
				continue
			}
			// Check pipeline_skip_if_only_changed if pipeline_run_if_changed is not present
			if skip, ok := presubmit.Annotations["pipeline_skip_if_only_changed"]; ok && skip != "" {
				psList := []config.Presubmit{presubmit}
				psList[0].RegexpChangeMatcher = config.RegexpChangeMatcher{SkipIfOnlyChanged: skip}
				if err := config.SetPresubmitRegexes(psList); err != nil {
					deleteIds()
					return "", err
				}
				_, shouldRun, err := psList[0].RegexpChangeMatcher.ShouldRun(cfp)
				if err != nil {
					deleteIds()
					return "", err
				}
				if shouldRun {
					testCommands += "\n" + presubmit.RerunCommand
				}
			}
		}
	}
	return testCommands, nil
}
