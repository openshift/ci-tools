package main

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/test-infra/prow/apis/prowjobs/v1"
	"k8s.io/test-infra/prow/github"
	"k8s.io/test-infra/prow/kube"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	retention = 24 * time.Hour
)

type minimalGhClient interface {
	GetPullRequest(org, repo string, number int) (*github.PullRequest, error)
	CreateComment(org, repo string, number int, comment string) error
}

type pullRequest struct {
	closed    bool
	checkTime time.Time
}

type closedPRsCache struct {
	prs       map[string]pullRequest
	m         sync.Mutex
	ghc       minimalGhClient
	clearTime time.Time
}

func composePRIdentifier(refs *v1.Refs) string {
	return fmt.Sprintf("%s/%s/%d", refs.Org, refs.Repo, refs.Pulls[0].Number)
}

// isPRClosed quieries either github or short-term cache to determine if PR is closed. Draft PRs are
// also quialified as closed due to potential, unexpected side effects
func (c *closedPRsCache) isPRClosed(refs *v1.Refs) (bool, error) {
	id := composePRIdentifier(refs)
	c.m.Lock()
	defer c.m.Unlock()
	c.clearCache()
	pr, ok := c.prs[id]
	if ok && (time.Since(pr.checkTime) < 5*time.Minute || pr.closed) {
		return pr.closed, nil
	}
	ghPr, err := c.ghc.GetPullRequest(refs.Org, refs.Repo, refs.Pulls[0].Number)
	if err != nil {
		return false, fmt.Errorf("error getting pull request: %w", err)
	}
	c.prs[id] = pullRequest{closed: ghPr.State != github.PullRequestStateOpen || ghPr.Draft, checkTime: time.Now()}
	return ghPr.State != github.PullRequestStateOpen || ghPr.Draft, nil
}

func (c *closedPRsCache) clearCache() {
	if time.Since(c.clearTime) < retention {
		return
	}
	for k, v := range c.prs {
		if time.Since(v.checkTime) >= retention {
			delete(c.prs, k)
		}
	}
	c.clearTime = time.Now()
}

func composeKey(refs *v1.Refs) string {
	return fmt.Sprintf("%s/%s/%d/%s/%s", refs.Org, refs.Repo, refs.Pulls[0].Number, refs.BaseRef, refs.Pulls[0].SHA)
}

type reconciler struct {
	pjclientset        ctrlruntimeclient.Client
	lister             ctrlruntimeclient.Reader
	configDataProvider *ConfigDataProvider
	ghc                minimalGhClient
	closedPRsCache     closedPRsCache
	ids                sync.Map
	logger             *logrus.Entry
}

func NewReconciler(
	mgr manager.Manager,
	configDataProvider *ConfigDataProvider,
	ghc github.Client,
	logger *logrus.Entry,
) (*reconciler, error) {
	reconciler := &reconciler{
		pjclientset:        mgr.GetClient(),
		lister:             mgr.GetCache(),
		configDataProvider: configDataProvider,
		ghc:                ghc,
		ids:                sync.Map{},
		logger:             logger,
		closedPRsCache: closedPRsCache{
			prs:       map[string]pullRequest{},
			m:         sync.Mutex{},
			ghc:       ghc,
			clearTime: time.Now(),
		},
	}
	if err := builder.
		ControllerManagedBy(mgr).
		Named("pipeline-controller").
		For(&v1.ProwJob{}).
		WithOptions(controller.Options{MaxConcurrentReconciles: 5}).
		Complete(reconciler); err != nil {
		return nil, fmt.Errorf("failed to construct controller: %w", err)
	}
	return reconciler, nil
}

func (r *reconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	log := r.logger.WithField("key", req.String()).WithField("prowjob", req.Name)
	err := r.reconcile(ctx, req)
	if err != nil {
		log.WithError(err).Error("reconciliation failed")
	}
	return reconcile.Result{}, err
}

func (r *reconciler) cleanOldIds(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for range ticker.C {
		r.ids.Range(func(key, value interface{}) bool {
			if time.Since(value.(time.Time)) >= interval {
				r.ids.Delete(key)
			}
			return true
		})
	}
}

func (r *reconciler) reconcile(ctx context.Context, req reconcile.Request) error {
	var pj v1.ProwJob
	if err := r.pjclientset.Get(ctx, req.NamespacedName, &pj); err != nil {
		if errors.IsNotFound(err) {
			return nil
		}

		return fmt.Errorf("failed to get prowjob %s: %w", req.String(), err)
	}

	if pj.Spec.Refs == nil || pj.Spec.Type != v1.PresubmitJob {
		return nil
	}

	presubmits := r.configDataProvider.GetPresubmits(pj.Spec.Refs.Org + "/" + pj.Spec.Refs.Repo)
	if len(presubmits.protected) == 0 && len(presubmits.alwaysRequired) == 0 && len(presubmits.conditionallyRequired) == 0 {
		return nil
	}

	if status, err := r.reportSuccessOnPR(ctx, &pj, presubmits); err != nil || !status {
		return err
	}
	if err := r.ghc.CreateComment(pj.Spec.Refs.Org, pj.Spec.Refs.Repo, pj.Spec.Refs.Pulls[0].Number, "/test remaining-required"); err != nil {
		r.ids.Delete(composeKey(pj.Spec.Refs))
		return err
	}
	return nil
}

func (r *reconciler) reportSuccessOnPR(ctx context.Context, pj *v1.ProwJob, presubmits presubmitTests) (bool, error) {
	if pj == nil || pj.Spec.Refs == nil || len(pj.Spec.Refs.Pulls) != 1 {
		return false, nil
	}
	selector := map[string]string{}
	for _, l := range []string{kube.OrgLabel, kube.RepoLabel, kube.PullLabel, kube.BaseRefLabel} {
		selector[l] = pj.ObjectMeta.Labels[l]
	}
	var pjs v1.ProwJobList
	if err := r.lister.List(ctx, &pjs, ctrlruntimeclient.MatchingLabels(selector)); err != nil {
		return false, fmt.Errorf("cannot list prowjob using selector %v", selector)
	}

	latestBatch := make(map[string]v1.ProwJob)
	for _, pjob := range pjs.Items {
		if pjob.Spec.Refs.Pulls[0].SHA == pj.Spec.Refs.Pulls[0].SHA {
			if existing, ok := latestBatch[pjob.Spec.Job]; !ok {
				latestBatch[pjob.Spec.Job] = pjob
			} else if pjob.CreationTimestamp.After(existing.CreationTimestamp.Time) {
				latestBatch[pjob.Spec.Job] = pjob
			}
		}
	}

	repoBaseRef := pj.Spec.Refs.Repo + "-" + pj.Spec.Refs.BaseRef
	for _, presubmit := range presubmits.protected {
		if !strings.Contains(presubmit, repoBaseRef) {
			continue
		}
		if _, ok := latestBatch[presubmit]; ok {
			return false, nil
		}
	}
	for _, presubmit := range presubmits.alwaysRequired {
		if !strings.Contains(presubmit, repoBaseRef) {
			continue
		}
		if pjob, ok := latestBatch[presubmit]; !ok || (ok && pjob.Status.State != v1.SuccessState) {
			return false, nil
		}
	}
	for _, presubmit := range presubmits.conditionallyRequired {
		if !strings.Contains(presubmit, repoBaseRef) {
			continue
		}
		if pjob, ok := latestBatch[presubmit]; ok && pjob.Status.State != v1.SuccessState {
			return false, nil
		}
	}
	if closed, err := r.closedPRsCache.isPRClosed(pj.Spec.Refs); err != nil || closed {
		return false, err
	}

	if _, loaded := r.ids.LoadOrStore(composeKey(pj.Spec.Refs), time.Now()); loaded {
		return false, nil
	}
	return true, nil
}
