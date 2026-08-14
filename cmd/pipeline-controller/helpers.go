package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/sirupsen/logrus"

	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	prowapi "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	prowconfig "sigs.k8s.io/prow/pkg/config"
	"sigs.k8s.io/prow/pkg/kube"
)

// commentCreator abstracts the GitHub comment API for testing.
type commentCreator interface {
	CreateComment(org, repo string, number int, comment string) error
}

// pjLister abstracts Kubernetes ProwJob listing for testing.
type pjLister interface {
	List(ctx context.Context, list ctrlruntimeclient.ObjectList, opts ...ctrlruntimeclient.ListOption) error
}

// sendCommentWithMode posts /test commands for presubmits that need
// to be triggered in the second pipeline stage. The presubmits are
// split into two categories:
//
//   - protected: non-optional, always-run jobs that must be present
//     in the second stage.
//   - conditionally-required: jobs whose pipeline_run_if_changed or
//     pipeline_skip_if_only_changed annotations matched the changed files.
//
// For conditionally-required presubmits, acquireConditionalContexts
// already de-duplicates by checking if a ProwJob exists at the same SHA.
// For protected presubmits, we apply the same de-duplication check here
// before generating /test commands.
//
// If isExplicitCommand is true (i.e. triggered via /pipeline required),
// all matching tests are triggered unconditionally regardless of whether
// ProwJobs already exist.
func sendCommentWithMode(
	ctx context.Context,
	logger *logrus.Entry,
	ghc commentCreator,
	lister pjLister,
	prowJob *prowapi.ProwJob,
	protectedPresubmits []prowconfig.Presubmit,
	conditionalPresubmits []prowconfig.Presubmit,
	isExplicitCommand bool,
	namespace string,
) (string, error) {
	if prowJob.Spec.Refs == nil || len(prowJob.Spec.Refs.Pulls) == 0 {
		return "", fmt.Errorf("prowjob %s has no pull request refs", prowJob.Name)
	}

	org := prowJob.Spec.Refs.Org
	repo := prowJob.Spec.Refs.Repo
	prNumber := prowJob.Spec.Refs.Pulls[0].Number
	headSHA := prowJob.Spec.Refs.Pulls[0].SHA

	// Gather /test commands for conditionally-required presubmits.
	conditionalCommands, conditionalMsg := acquireConditionalContexts(
		ctx, logger, lister, conditionalPresubmits, org, repo, prNumber, headSHA, namespace,
	)

	// Gather /test commands for protected presubmits.
	// When this is an explicit /pipeline required command, trigger all protected tests
	// unconditionally. Otherwise, deduplicate against existing ProwJobs at the same SHA.
	var protectedCommands []string
	var protectedAlreadyExist []string

	if isExplicitCommand {
		for _, ps := range protectedPresubmits {
			protectedCommands = append(protectedCommands, fmt.Sprintf("/test %s", ps.Name))
		}
	} else {
		for _, ps := range protectedPresubmits {
			exists, err := prowJobExistsForSHA(ctx, lister, ps.Name, org, repo, prNumber, headSHA, namespace)
			if err != nil {
				logger.WithError(err).WithField("job", ps.Name).Warn("failed to check for existing ProwJob, will trigger to be safe")
				protectedCommands = append(protectedCommands, fmt.Sprintf("/test %s", ps.Name))
				continue
			}
			if exists {
				logger.WithField("job", ps.Name).WithField("sha", headSHA).Info("protected ProwJob already exists at HEAD, skipping re-trigger")
				protectedAlreadyExist = append(protectedAlreadyExist, ps.Name)
			} else {
				protectedCommands = append(protectedCommands, fmt.Sprintf("/test %s", ps.Name))
			}
		}
	}

	allCommands := append(protectedCommands, conditionalCommands...)
	if len(allCommands) == 0 {
		// All tests already exist at this SHA; return an informational message
		// rather than posting an empty comment.
		var parts []string
		if len(protectedAlreadyExist) > 0 {
			parts = append(parts, fmt.Sprintf("protected tests already triggered at SHA %s: %s", headSHA, strings.Join(protectedAlreadyExist, ", ")))
		}
		if conditionalMsg != "" {
			parts = append(parts, conditionalMsg)
		}
		msg := fmt.Sprintf("All pipeline tests already exist at the current HEAD. %s", strings.Join(parts, "; "))
		logger.Info(msg)
		return msg, nil
	}

	comment := strings.Join(allCommands, "\n")
	if err := ghc.CreateComment(org, repo, prNumber, comment); err != nil {
		return "", fmt.Errorf("failed to create comment on %s/%s#%d: %w", org, repo, prNumber, err)
	}

	return fmt.Sprintf("triggered %d test(s) for %s/%s#%d at SHA %s", len(allCommands), org, repo, prNumber, headSHA), nil
}

// acquireConditionalContexts checks which conditionally-required presubmits
// already have ProwJobs at the given SHA and returns /test commands only for
// those that do not. It also returns an informational message about any tests
// that were skipped because they already exist.
func acquireConditionalContexts(
	ctx context.Context,
	logger *logrus.Entry,
	lister pjLister,
	presubmits []prowconfig.Presubmit,
	org, repo string,
	prNumber int,
	headSHA string,
	namespace string,
) ([]string, string) {
	var commands []string
	var alreadyExist []string

	for _, ps := range presubmits {
		exists, err := prowJobExistsForSHA(ctx, lister, ps.Name, org, repo, prNumber, headSHA, namespace)
		if err != nil {
			logger.WithError(err).WithField("job", ps.Name).Warn("failed to check for existing ProwJob, will trigger to be safe")
			commands = append(commands, fmt.Sprintf("/test %s", ps.Name))
			continue
		}
		if exists {
			logger.WithField("job", ps.Name).WithField("sha", headSHA).Info("conditional ProwJob already exists at HEAD, skipping re-trigger")
			alreadyExist = append(alreadyExist, ps.Name)
		} else {
			commands = append(commands, fmt.Sprintf("/test %s", ps.Name))
		}
	}

	var msg string
	if len(alreadyExist) > 0 {
		msg = fmt.Sprintf("conditional tests already triggered at SHA %s: %s", headSHA, strings.Join(alreadyExist, ", "))
	}
	return commands, msg
}

// prowJobExistsForSHA checks if a ProwJob with the given job name already exists
// for the specified PR at the given HEAD SHA. It uses label-based filtering
// following the standard Prow labeling convention.
func prowJobExistsForSHA(
	ctx context.Context,
	lister pjLister,
	jobName string,
	org, repo string,
	prNumber int,
	headSHA string,
	namespace string,
) (bool, error) {
	var pjList prowapi.ProwJobList
	matchLabels := ctrlruntimeclient.MatchingLabels{
		kube.OrgLabel:          org,
		kube.RepoLabel:         repo,
		kube.PullLabel:         fmt.Sprintf("%d", prNumber),
		kube.ProwJobTypeLabel:  string(prowapi.PresubmitJob),
		kube.ProwJobAnnotation: jobName,
	}
	opts := []ctrlruntimeclient.ListOption{
		matchLabels,
		ctrlruntimeclient.InNamespace(namespace),
	}

	if err := lister.List(ctx, &pjList, opts...); err != nil {
		return false, fmt.Errorf("listing ProwJobs for %s: %w", jobName, err)
	}

	for i := range pjList.Items {
		pj := &pjList.Items[i]
		if pj.Spec.Refs != nil && len(pj.Spec.Refs.Pulls) > 0 && pj.Spec.Refs.Pulls[0].SHA == headSHA {
			return true, nil
		}
	}
	return false, nil
}
