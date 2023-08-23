package main

import (
	"context"
	"fmt"
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

type prs struct {
	merged    bool
	checkTime time.Time
}

type mergedPRsCache struct {
	prs map[string]prs
	m   sync.Mutex
	ghc github.Client
}

func composePRIdentifier(refs *v1.Refs) string {
	return fmt.Sprintf("%s/%s/%d", refs.Org, refs.Repo, refs.Pulls[0].Number)
}

func (c *mergedPRsCache) isPRMerged(refs *v1.Refs) (bool, error) {
	c.m.Lock()
	pr, ok := c.prs[composePRIdentifier(refs)]
	if ok && (time.Since(pr.checkTime) < time.Second*10 || pr.merged) {
		return pr.merged, nil
	}
	c.prs[composePRIdentifier(refs)] = prs{merged: false, checkTime: time.Now()} //pre-assign to avoid multiple calls
	c.m.Unlock()
	ghPr, err := c.ghc.GetPullRequest(refs.Org, refs.Repo, refs.Pulls[0].Number)
	if err != nil {
		return false, fmt.Errorf("error getting pull request: %w", err)
	}
	logrus.Debugf("PR %s/%s/%d merged: %v", refs.Org, refs.Repo, refs.Pulls[0].Number, ghPr.Merged)
	c.m.Lock()
	defer c.m.Unlock()
	c.prs[composePRIdentifier(refs)] = prs{merged: ghPr.Merged, checkTime: time.Now()}
	return ghPr.Merged, nil
}

type reconciler struct {
	pjclientset        ctrlruntimeclient.Client
	lister             ctrlruntimeclient.Reader
	configDataProvider *ConfigDataProvider
	ghc                github.Client
	baseShas           map[string]string
	baseShasMutex      sync.Mutex
	mergedPRsCache     mergedPRsCache
}

func NewReconciler(
	mgr manager.Manager,
	configDataProvider *ConfigDataProvider,
	ghc github.Client,
) error {
	if err := builder.
		ControllerManagedBy(mgr).
		Named("simplified_pipeline_ctrl").
		For(&v1.ProwJob{}).
		WithOptions(controller.Options{MaxConcurrentReconciles: 1}).
		Complete(&reconciler{
			pjclientset:        mgr.GetClient(),
			lister:             mgr.GetCache(),
			configDataProvider: configDataProvider,
			ghc:                ghc,
			baseShas:           map[string]string{},
			baseShasMutex:      sync.Mutex{},
			mergedPRsCache:     mergedPRsCache{prs: map[string]prs{}, m: sync.Mutex{}, ghc: ghc},
		}); err != nil {
		return fmt.Errorf("failed to construct controller: %w", err)
	}

	return nil
}

func (r *reconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	log := logrus.WithField("reporter", "github").WithField("key", req.String()).WithField("prowjob", req.Name)
	err := r.reconcile(ctx, req)
	if err != nil {
		log.WithError(err).Error("Reconciliation failed")
	}
	return reconcile.Result{}, err
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

	merged, err := r.mergedPRsCache.isPRMerged(pj.Spec.Refs)
	if err != nil {
		return err
	}
	if merged {
		return nil
	}

	if !r.compareAndUpdateBaseSha(composeKey(pj.Spec.Refs), pj.Spec.Refs.BaseSHA) {
		return nil
	}

	status, err := r.reportSuccessOnPR(ctx, &pj, presubmits)
	if err != nil {
		return err
	}
	if status {
		if err := r.ghc.CreateComment(pj.Spec.Refs.Org, pj.Spec.Refs.Repo, pj.Spec.Refs.Pulls[0].Number, "/test remaining-required"); err != nil {
			r.deleteBaseSha(composeKey(pj.Spec.Refs)) //remove if failed to comment
			return err
		}
	}

	return nil
}

func (r *reconciler) compareAndUpdateBaseSha(key, value string) bool {
	r.baseShasMutex.Lock()
	defer r.baseShasMutex.Unlock()
	if v, ok := r.baseShas[key]; ok && v == value {
		return false
	}
	r.baseShas[key] = value
	return true
}

func (r *reconciler) deleteBaseSha(key string) {
	r.baseShasMutex.Lock()
	defer r.baseShasMutex.Unlock()
	delete(r.baseShas, key)
}

func composeKey(refs *v1.Refs) string {
	return fmt.Sprintf("%d:%s/%s@%s", refs.Pulls[0].Number, refs.Org, refs.Repo, refs.BaseRef)
}

func (r *reconciler) reportSuccessOnPR(ctx context.Context, pj *v1.ProwJob, presubmits presubmitTests) (bool, error) {
	if pj == nil || pj.Spec.Refs == nil || len(pj.Spec.Refs.Pulls) != 1 {
		return false, nil
	}
	selector := map[string]string{}
	for _, l := range []string{kube.OrgLabel, kube.RepoLabel, kube.PullLabel} {
		selector[l] = pj.ObjectMeta.Labels[l]
	}
	var pjs v1.ProwJobList
	if err := r.lister.List(ctx, &pjs, ctrlruntimeclient.MatchingLabels(selector)); err != nil {
		return false, fmt.Errorf("cannot list prowjob using selector %v", selector)
	}

	latestBatch := make(map[string]v1.ProwJob)
	for _, pjob := range pjs.Items {
		//if !pjob.Complete() {
		//	return false, nil
		//}
		if existing, ok := latestBatch[pjob.Name]; !ok {
			latestBatch[pjob.Name] = pjob
		} else if pjob.CreationTimestamp.After(existing.CreationTimestamp.Time) {
			latestBatch[pjob.Name] = pjob
		}
	}
	// if any of the presubmits is in the latest batch, pipeline was already triggered
	// unless someone manually triggered it
	for _, presubmit := range presubmits.protected {
		if _, ok := latestBatch[presubmit]; ok {
			return false, nil
		}
	}
	for _, presubmit := range presubmits.alwaysRequired {
		if pjob, ok := latestBatch[presubmit]; ok && pjob.Status.State != v1.SuccessState {
			return false, nil
		}
	}
	for _, presubmit := range presubmits.conditionallyRequired {
		if pjob, ok := latestBatch[presubmit]; ok && pjob.Status.State != v1.SuccessState {
			return false, nil
		}
	}
	// TODO: batch is 0, prowjobs removed from cluster, special case to implement

	return true, nil
}
