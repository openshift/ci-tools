package main

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	githubql "github.com/shurcooL/githubv4"
	"github.com/sirupsen/logrus"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	prowapi "k8s.io/test-infra/prow/apis/prowjobs/v1"
	"k8s.io/test-infra/prow/config"
	"k8s.io/test-infra/prow/git/v2"
	"k8s.io/test-infra/prow/github"
	"k8s.io/test-infra/prow/tide"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
)

type retestController struct {
	ctx           context.Context
	ghClient      githubClient
	gitClient     git.ClientFactory
	configGetter  config.Getter
	prowJobClient ctrlruntimeclient.Client

	// changedFiles caches the names of files changed by PRs.
	// Cache entries expire if they are not used during a sync loop.
	changedFiles *changedFilesAgent

	mergeChecker *mergeChecker

	logger *logrus.Entry

	usesGitHubApp bool
}

func newController(ghClient githubClient, cfg config.Getter, gitClient git.ClientFactory, prowJobClient ctrlruntimeclient.Client, usesApp bool) *retestController {
	return &retestController{
		ctx:           context.Background(),
		ghClient:      ghClient,
		gitClient:     gitClient,
		configGetter:  cfg,
		prowJobClient: prowJobClient,
		changedFiles: &changedFilesAgent{
			ghc:             ghClient,
			nextChangeCache: make(map[changeCacheKey][]string),
		},
		mergeChecker:  newMergeChecker(cfg, ghClient),
		logger:        logrus.NewEntry(logrus.StandardLogger()),
		usesGitHubApp: usesApp,
	}
}

func (c *retestController) sync() error {
	// Input: Tide Config
	// Output: A list of PRs that are filter out by the queries in Tide's config
	candidates, err := findCandidates(c.configGetter, c.ghClient, c.usesGitHubApp, c.logger)
	if err != nil {
		return err
	}

	rawPools, err := c.dividePool(candidates)
	if err != nil {
		return err
	}
	//each filteredPool holds a subset of input PRs whose jobs are actually required for merge
	filteredPools := c.filterSubpools(c.mergeChecker.isAllowed, rawPools)

	for key, filteredPool := range filteredPools {
		filteredPRs := filteredPool.prs
		logrus.Infof("Found %d candidates in pool %s for retest", len(filteredPRs), key)
		for _, pr := range filteredPRs {
			logrus.Infof("Candidate PR: (https://github.com/%s/%s/pull/%d", pr.Repository.Owner.Login, pr.Repository.Name, pr.Number)
		}

		// Input: A list of PRs that would merge but have failing required jobs
		// Output: A subset of input PRs that are *not* in a back-off (whatever the back-off is)
		filteredPRs = notInBackOff(filteredPRs)

		// Actually comment...
		retest(filteredPRs)
	}

	logrus.Info("Sync finished")
	return nil
}

func findCandidates(config config.Getter, gc githubClient, usesGitHubAppsAuth bool, logger *logrus.Entry) (map[string]tide.PullRequest, error) {
	prs, err := query(config, gc, usesGitHubAppsAuth, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to query GitHub for prs: %w", err)
	}

	return prs, nil
}

// refactor out the query function from the tide's controller
// https://github.com/kubernetes/test-infra/blob/0d18a317a517e1bdb9a2c728a46fbeb3642445dd/prow/tide/tide.go#L450
func query(config config.Getter, gc githubClient, usesGitHubAppsAuth bool, logger *logrus.Entry) (map[string]tide.PullRequest, error) {
	prs := make(map[string]tide.PullRequest)
	var errs []error
	for i, query := range config().Tide.Queries {

		// Use org-sharded queries only when GitHub apps auth is in use
		var queries map[string]string
		if usesGitHubAppsAuth {
			queries = query.OrgQueries()
		} else {
			queries = map[string]string{"": query.Query()}
		}

		for org, q := range queries {
			org, i := org, i
			q := "status:failure " + q

			results, err := search(gc.QueryWithGitHubAppsSupport, logger, q, time.Time{}, time.Now(), org)

			if err != nil && len(results) == 0 {
				logger.WithField("query", q).WithError(err).Warn("Failed to execute query.")
				errs = append(errs, fmt.Errorf("query %d, err: %w", i, err))
				continue
			}
			if err != nil {
				logger.WithError(err).WithField("query", q).Warning("found partial results")
			}

			for _, pr := range results {
				prs[prKey(&pr)] = pr
			}
		}
		logrus.Infof("Finished query: %d", i)
	}

	return prs, utilerrors.NewAggregate(errs)
}

func prKey(pr *tide.PullRequest) string {
	return fmt.Sprintf("%s#%d", string(pr.Repository.NameWithOwner), int(pr.Number))
}

type querier func(ctx context.Context, q interface{}, vars map[string]interface{}, org string) error

func floor(t time.Time) time.Time {
	if t.Before(github.FoundingYear) {
		return github.FoundingYear
	}
	return t
}
func datedQuery(q string, start, end time.Time) string {
	return fmt.Sprintf("%s %s", q, dateToken(start, end))
}

// dateToken generates a GitHub search query token for the specified date range.
// See: https://help.github.com/articles/understanding-the-search-syntax/#query-for-dates
func dateToken(start, end time.Time) string {
	// GitHub's GraphQL API silently fails if you provide it with an invalid time
	// string.
	// Dates before 1970 (unix epoch) are considered invalid.
	startString, endString := "*", "*"
	if start.Year() >= 1970 {
		startString = start.Format(github.SearchTimeFormat)
	}
	if end.Year() >= 1970 {
		endString = end.Format(github.SearchTimeFormat)
	}
	return fmt.Sprintf("updated:%s..%s", startString, endString)
}

type searchQuery struct {
	RateLimit struct {
		Cost      githubql.Int
		Remaining githubql.Int
	}
	Search struct {
		PageInfo struct {
			HasNextPage githubql.Boolean
			EndCursor   githubql.String
		}
		Nodes []PRNode
	} `graphql:"search(type: ISSUE, first: 37, after: $searchCursor, query: $query)"`
}
type PRNode struct {
	PullRequest tide.PullRequest `graphql:"... on PullRequest"`
}

func search(query querier, log *logrus.Entry, q string, start, end time.Time, org string) ([]tide.PullRequest, error) {
	start = floor(start)
	end = floor(end)
	log = log.WithFields(logrus.Fields{
		"query": q,
		"start": start.String(),
		"end":   end.String(),
	})
	requestStart := time.Now()
	var cursor *githubql.String
	vars := map[string]interface{}{
		"query":        githubql.String(datedQuery(q, start, end)),
		"searchCursor": cursor,
	}

	var totalCost, remaining int
	var ret []tide.PullRequest
	var sq searchQuery
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	for {
		log.Debug("Sending query")
		if err := query(ctx, &sq, vars, org); err != nil {
			if cursor != nil {
				err = fmt.Errorf("cursor: %q, err: %w", *cursor, err)
			}
			return ret, err
		}
		totalCost += int(sq.RateLimit.Cost)
		remaining = int(sq.RateLimit.Remaining)
		for _, n := range sq.Search.Nodes {
			ret = append(ret, n.PullRequest)
		}
		if !sq.Search.PageInfo.HasNextPage {
			break
		}
		cursor = &sq.Search.PageInfo.EndCursor
		vars["searchCursor"] = cursor
		log = log.WithField("searchCursor", *cursor)
	}
	log.WithFields(logrus.Fields{
		"duration":       time.Since(requestStart).String(),
		"pr_found_count": len(ret),
		"cost":           totalCost,
		"remaining":      remaining,
	}).Debug("Finished query")
	return ret, nil
}

func notInBackOff(input []tide.PullRequest) []tide.PullRequest {
	return input
}

func retest(prs []tide.PullRequest) {

}

type contextChecker interface {
	// IsOptional tells whether a context is optional.
	IsOptional(string) bool
	// MissingRequiredContexts tells if required contexts are missing from the list of contexts provided.
	MissingRequiredContexts([]string) []string
}

type subpool struct {
	log    *logrus.Entry
	org    string
	repo   string
	branch string
	// sha is the baseSHA for this subpool
	sha string

	// pjs contains all ProwJobs of type Presubmit or Batch
	// that have the same baseSHA as the subpool
	pjs []prowapi.ProwJob
	prs []tide.PullRequest

	cc map[int]contextChecker
	// presubmit contains all required presubmits for each PR
	// in this subpool
	presubmits map[int][]config.Presubmit
}

func poolKey(org, repo, branch string) string {
	return fmt.Sprintf("%s/%s:%s", org, repo, branch)
}

// cacheIndexName is the name of the index that indexes presubmit+batch ProwJobs by
// org+repo+branch+baseSHA. Use the cacheIndexKey func to get the correct key.
const cacheIndexName = "tide-global-index"

// cacheIndexKey returns the index key for the tideCacheIndex
func cacheIndexKey(org, repo, branch, baseSHA string) string {
	return fmt.Sprintf("%s/%s:%s@%s", org, repo, branch, baseSHA)
}

// dividePool splits up the list of pull requests and prow jobs into a group
// per repo and branch. It only keeps ProwJobs that match the latest branch.
func (c *retestController) dividePool(pool map[string]tide.PullRequest) (map[string]*subpool, error) {
	sps := make(map[string]*subpool)
	for _, pr := range pool {
		org := string(pr.Repository.Owner.Login)
		repo := string(pr.Repository.Name)
		branch := string(pr.BaseRef.Name)
		branchRef := string(pr.BaseRef.Prefix) + string(pr.BaseRef.Name)
		fn := poolKey(org, repo, branch)
		if sps[fn] == nil {
			sha, err := c.ghClient.GetRef(org, repo, strings.TrimPrefix(branchRef, "refs/"))
			if err != nil {
				return nil, err
			}
			sps[fn] = &subpool{
				log: c.logger.WithFields(logrus.Fields{
					"org":      org,
					"repo":     repo,
					"branch":   branch,
					"base-sha": sha,
				}),
				org:    org,
				repo:   repo,
				branch: branch,
				sha:    sha,
			}
		}
		sps[fn].prs = append(sps[fn].prs, pr)
	}

	for subpoolkey, sp := range sps {
		pjs := &prowapi.ProwJobList{}
		err := c.prowJobClient.List(
			c.ctx,
			pjs,
			ctrlruntimeclient.MatchingFields{cacheIndexName: cacheIndexKey(sp.org, sp.repo, sp.branch, sp.sha)},
			ctrlruntimeclient.InNamespace(c.configGetter().ProwJobNamespace))
		if err != nil {
			return nil, fmt.Errorf("failed to list jobs for subpool %s: %w", subpoolkey, err)
		}
		c.logger.WithField("subpool", subpoolkey).Debugf("Found %d prowjobs.", len(pjs.Items))
		sps[subpoolkey].pjs = pjs.Items
	}
	return sps, nil
}

func subpoolsInParallel(goroutines int, sps map[string]*subpool, process func(*subpool)) {
	// Load the subpools into a channel for use as a work queue.
	queue := make(chan *subpool, len(sps))
	for _, sp := range sps {
		queue <- sp
	}
	close(queue)

	if goroutines > len(queue) {
		goroutines = len(queue)
	}
	wg := &sync.WaitGroup{}
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			for sp := range queue {
				process(sp)
			}
		}()
	}
	wg.Wait()
}

// filterSubpools filters non-pool PRs out of the initially identified subpools,
// deleting any pools that become empty.
// See filterSubpool for filtering details.
func (c *retestController) filterSubpools(mergeAllowed func(*tide.PullRequest) (string, error), raw map[string]*subpool) map[string]*subpool {
	filtered := make(map[string]*subpool)
	var lock sync.Mutex

	subpoolsInParallel(
		c.configGetter().Tide.MaxGoroutines,
		raw,
		func(sp *subpool) {
			if err := c.initSubpoolData(sp); err != nil {
				sp.log.WithError(err).Error("Error initializing subpool.")
				return
			}
			key := poolKey(sp.org, sp.repo, sp.branch)
			if spFiltered := filterSubpool(c.ghClient, mergeAllowed, sp); spFiltered != nil {
				sp.log.WithField("key", key).WithField("pool", spFiltered).Debug("filtered sub-pool")

				lock.Lock()
				filtered[key] = spFiltered
				lock.Unlock()
			} else {
				sp.log.WithField("key", key).WithField("pool", spFiltered).Debug("filtering sub-pool removed all PRs")
			}
		},
	)
	return filtered
}

func refGetterFactory(ref string) config.RefGetter {
	return func() (string, error) {
		return ref, nil
	}
}

type changeCacheKey struct {
	org, repo string
	number    int
	sha       string
}

// changedFilesAgent queries and caches the names of files changed by PRs.
// Cache entries expire if they are not used during a sync loop.
type changedFilesAgent struct {
	ghc         githubClient
	changeCache map[changeCacheKey][]string
	// nextChangeCache caches file change info that is relevant this sync for use next sync.
	// This becomes the new changeCache when prune() is called at the end of each sync.
	nextChangeCache map[changeCacheKey][]string
	sync.RWMutex
}

// prChanges gets the files changed by the PR, either from the cache or by
// querying GitHub.
func (c *changedFilesAgent) prChanges(pr *tide.PullRequest) config.ChangedFilesProvider {
	return func() ([]string, error) {
		cacheKey := changeCacheKey{
			org:    string(pr.Repository.Owner.Login),
			repo:   string(pr.Repository.Name),
			number: int(pr.Number),
			sha:    string(pr.HeadRefOID),
		}

		c.RLock()
		changedFiles, ok := c.changeCache[cacheKey]
		if ok {
			c.RUnlock()
			c.Lock()
			c.nextChangeCache[cacheKey] = changedFiles
			c.Unlock()
			return changedFiles, nil
		}
		if changedFiles, ok = c.nextChangeCache[cacheKey]; ok {
			c.RUnlock()
			return changedFiles, nil
		}
		c.RUnlock()

		// We need to query the changes from GitHub.
		changes, err := c.ghc.GetPullRequestChanges(
			string(pr.Repository.Owner.Login),
			string(pr.Repository.Name),
			int(pr.Number),
		)
		if err != nil {
			return nil, fmt.Errorf("error getting PR changes for #%d: %w", int(pr.Number), err)
		}
		changedFiles = make([]string, 0, len(changes))
		for _, change := range changes {
			changedFiles = append(changedFiles, change.Filename)
		}

		c.Lock()
		c.nextChangeCache[cacheKey] = changedFiles
		c.Unlock()
		return changedFiles, nil
	}
}

func logFields(pr tide.PullRequest) logrus.Fields {
	return logrus.Fields{
		"org":    string(pr.Repository.Owner.Login),
		"repo":   string(pr.Repository.Name),
		"pr":     int(pr.Number),
		"branch": string(pr.BaseRef.Name),
		"sha":    string(pr.HeadRefOID),
	}
}

func (c *retestController) presubmitsByPull(sp *subpool) (map[int][]config.Presubmit, error) {
	presubmits := make(map[int][]config.Presubmit, len(sp.prs))
	record := func(num int, job config.Presubmit) {
		if jobs, ok := presubmits[num]; ok {
			presubmits[num] = append(jobs, job)
		} else {
			presubmits[num] = []config.Presubmit{job}
		}
	}

	// filtered PRs contains all PRs for which we were able to get the presubmits
	var filteredPRs []tide.PullRequest

	for _, pr := range sp.prs {
		log := c.logger.WithField("base-sha", sp.sha).WithFields(logFields(pr))
		presubmitsForPull, err := c.configGetter().GetPresubmits(c.gitClient, sp.org+"/"+sp.repo, refGetterFactory(sp.sha), refGetterFactory(string(pr.HeadRefOID)))
		if err != nil {
			c.logger.WithError(err).Debug("Failed to get presubmits for PR, excluding from subpool")
			continue
		}
		filteredPRs = append(filteredPRs, pr)
		log.Debugf("Found %d possible presubmits", len(presubmitsForPull))

		for _, ps := range presubmitsForPull {
			if !ps.ContextRequired() {
				continue
			}

			shouldRun, err := ps.ShouldRun(sp.branch, c.changedFiles.prChanges(&pr), false, false)
			if err != nil {
				return nil, err
			}
			if !shouldRun {
				log.WithField("context", ps.Context).Debug("Presubmit excluded by ps.ShouldRun")
				continue
			}

			record(int(pr.Number), ps)
		}
	}

	sp.prs = filteredPRs
	return presubmits, nil
}

func (c *retestController) initSubpoolData(sp *subpool) error {
	var err error
	sp.presubmits, err = c.presubmitsByPull(sp)
	if err != nil {
		return fmt.Errorf("error determining required presubmit prowjobs: %w", err)
	}
	sp.cc = make(map[int]contextChecker, len(sp.prs))
	for _, pr := range sp.prs {
		sp.cc[int(pr.Number)], err = c.configGetter().GetTideContextPolicy(c.gitClient, sp.org, sp.repo, sp.branch, refGetterFactory(sp.sha), string(pr.HeadRefOID))
		if err != nil {
			return fmt.Errorf("error setting up context checker for pr %d: %w", int(pr.Number), err)
		}
	}
	return nil
}

// filterSubpool filters PRs from an initially identified subpool, returning the
// filtered subpool.
// If the subpool becomes empty 'nil' is returned to indicate that the subpool
// should be deleted.
func filterSubpool(ghc githubClient, mergeAllowed func(*tide.PullRequest) (string, error), sp *subpool) *subpool {
	var toKeep []tide.PullRequest
	for _, pr := range sp.prs {
		if !filterPR(ghc, mergeAllowed, sp, &pr) {
			toKeep = append(toKeep, pr)
		}
	}
	if len(toKeep) == 0 {
		return nil
	}
	sp.prs = toKeep
	return sp
}

// filterPR indicates if a PR should be filtered out of the subpool.
// Specifically we filter out PRs that:
// - Have known merge conflicts or invalid merge method.
// - Have failing or missing status contexts.
// - Have pending required status contexts that are not associated with a
//   ProwJob. (This ensures that the 'tide' context indicates that the pending
//   status is preventing merge. Required ProwJob statuses are allowed to be
//   'pending' because this prevents kicking PRs from the pool when Tide is
//   retesting them.)
func filterPR(ghc githubClient, mergeAllowed func(*tide.PullRequest) (string, error), sp *subpool, pr *tide.PullRequest) bool {
	log := sp.log.WithFields(logFields(*pr))
	// Skip PRs that are known to be unmergeable.
	if reason, err := mergeAllowed(pr); err != nil {
		log.WithError(err).Error("Error checking PR mergeability.")
		return true
	} else if reason != "" {
		log.WithField("reason", reason).Debug("filtering out PR as it is not mergeable")
		return true
	}

	// Filter out PRs with unsuccessful contexts unless the only unsuccessful
	// contexts are pending required prowjobs.
	contexts, err := headContexts(log, ghc, pr)
	if err != nil {
		log.WithError(err).Error("Getting head contexts.")
		return true
	}
	presubmitsHaveContext := func(context string) bool {
		for _, job := range sp.presubmits[int(pr.Number)] {
			if job.Context == context {
				return true
			}
		}
		return false
	}
	for _, ctx := range unsuccessfulContexts(contexts, sp.cc[int(pr.Number)], log) {
		if ctx.State != githubql.StatusStatePending {
			log.WithField("context", ctx.Context).Debug("filtering out PR as unsuccessful context is not pending")
			return true
		}
		if !presubmitsHaveContext(string(ctx.Context)) {
			log.WithField("context", ctx.Context).Debug("filtering out PR as unsuccessful context is not Prow-controlled")
			return true
		}
	}

	return false
}

// headContexts gets the status contexts for the commit with OID == pr.HeadRefOID
//
// First, we try to get this value from the commits we got with the PR query.
// Unfortunately the 'last' commit ordering is determined by author date
// not commit date so if commits are reordered non-chronologically on the PR
// branch the 'last' commit isn't necessarily the logically last commit.
// We list multiple commits with the query to increase our chance of success,
// but if we don't find the head commit we have to ask GitHub for it
// specifically (this costs an API token).
func headContexts(log *logrus.Entry, ghc githubClient, pr *tide.PullRequest) ([]tide.Context, error) {
	for _, node := range pr.Commits.Nodes {
		if node.Commit.OID == pr.HeadRefOID {
			return append(node.Commit.Status.Contexts, checkRunNodesToContexts(log, node.Commit.StatusCheckRollup.Contexts.Nodes)...), nil
		}
	}
	// We didn't get the head commit from the query (the commits must not be
	// logically ordered) so we need to specifically ask GitHub for the status
	// and coerce it to a graphql type.
	org := string(pr.Repository.Owner.Login)
	repo := string(pr.Repository.Name)
	// Log this event so we can tune the number of commits we list to minimize this.
	// TODO alvaroaleman: Add checkrun support here. Doesn't seem to happen often though,
	// openshift doesn't have a single occurrence of this in the past seven days.
	log.Warnf("'last' %d commits didn't contain logical last commit. Querying GitHub...", len(pr.Commits.Nodes))
	combined, err := ghc.GetCombinedStatus(org, repo, string(pr.HeadRefOID))
	if err != nil {
		return nil, fmt.Errorf("failed to get the combined status: %w", err)
	}
	checkRunList, err := ghc.ListCheckRuns(org, repo, string(pr.HeadRefOID))
	if err != nil {
		return nil, fmt.Errorf("Failed to list checkruns: %w", err)
	}
	checkRunNodes := make([]tide.CheckRunNode, 0, len(checkRunList.CheckRuns))
	for _, checkRun := range checkRunList.CheckRuns {
		checkRunNodes = append(checkRunNodes, tide.CheckRunNode{CheckRun: tide.CheckRun{
			Name: githubql.String(checkRun.Name),
			// They are uppercase in the V4 api and lowercase in the V3 api
			Conclusion: githubql.String(strings.ToUpper(checkRun.Conclusion)),
			Status:     githubql.String(strings.ToUpper(checkRun.Status)),
		}})
	}

	contexts := make([]tide.Context, 0, len(combined.Statuses)+len(checkRunNodes))
	for _, status := range combined.Statuses {
		contexts = append(contexts, tide.Context{
			Context:     githubql.String(status.Context),
			Description: githubql.String(status.Description),
			State:       githubql.StatusState(strings.ToUpper(status.State)),
		})
	}
	contexts = append(contexts, checkRunNodesToContexts(log, checkRunNodes)...)

	// Add a commit with these contexts to pr for future look ups.
	pr.Commits.Nodes = append(pr.Commits.Nodes,
		struct{ Commit tide.Commit }{
			Commit: tide.Commit{
				OID:    pr.HeadRefOID,
				Status: struct{ Contexts []tide.Context }{Contexts: contexts},
			},
		},
	)
	return contexts, nil
}

func checkRunNodesToContexts(log *logrus.Entry, nodes []tide.CheckRunNode) []tide.Context {
	var result []tide.Context
	for _, node := range nodes {
		// GitHub gives us an empty checkrun per status context. In theory they could
		// at some point decide to create a virtual check run per status context.
		// If that were to happen, we would retrieve redundant data as we get the
		// status context both directly as a status context and as a checkrun, however
		// the actual data in there should be identical, hence this isn't a problem.
		if string(node.CheckRun.Name) == "" {
			continue
		}
		result = append(result, checkRunToContext(node.CheckRun))
	}
	result = deduplicateContexts(result)
	if len(result) > 0 {
		log.WithField("checkruns", len(result)).Debug("Transformed checkruns to contexts")
	}
	return result
}

const (
	statusContext = "tide"

	checkRunStatusCompleted   = githubql.String("COMPLETED")
	checkRunConclusionNeutral = githubql.String("NEUTRAL")
)

// checkRunToContext translates a checkRun to a classic context
// ref: https://developer.github.com/v3/checks/runs/#parameters
func checkRunToContext(checkRun tide.CheckRun) tide.Context {
	context := tide.Context{
		Context: checkRun.Name,
	}
	if checkRun.Status != checkRunStatusCompleted {
		context.State = githubql.StatusStatePending
		return context
	}

	if checkRun.Conclusion == checkRunConclusionNeutral || checkRun.Conclusion == githubql.String(githubql.StatusStateSuccess) {
		context.State = githubql.StatusStateSuccess
		return context
	}

	context.State = githubql.StatusStateFailure
	return context
}

type descriptionAndState struct {
	description githubql.String
	state       githubql.StatusState
}

// deduplicateContexts deduplicates contexts, returning the best result for
// contexts that have multiple entries
func deduplicateContexts(contexts []tide.Context) []tide.Context {
	result := map[githubql.String]descriptionAndState{}
	for _, context := range contexts {
		previousResult, found := result[context.Context]
		if !found {
			result[context.Context] = descriptionAndState{description: context.Description, state: context.State}
			continue
		}
		if isStateBetter(previousResult.state, context.State) {
			result[context.Context] = descriptionAndState{description: context.Description, state: context.State}
		}
	}

	var resultSlice []tide.Context
	for name, descriptionAndState := range result {
		resultSlice = append(resultSlice, tide.Context{Context: name, Description: descriptionAndState.description, State: descriptionAndState.state})
	}

	return resultSlice
}

func isStateBetter(previous, current githubql.StatusState) bool {
	if current == githubql.StatusStateSuccess {
		return true
	}
	if current == githubql.StatusStatePending && (previous == githubql.StatusStateError || previous == githubql.StatusStateFailure || previous == githubql.StatusStateExpected) {
		return true
	}
	if previous == githubql.StatusStateExpected && (current == githubql.StatusStateError || current == githubql.StatusStateFailure) {
		return true
	}

	return false
}

// unsuccessfulContexts determines which contexts from the list that we care about are
// failed. For instance, we do not care about our own context.
// If the branchProtection is set to only check for required checks, we will skip
// all non-required tests. If required tests are missing from the list, they will be
// added to the list of failed contexts.
func unsuccessfulContexts(contexts []tide.Context, cc contextChecker, log *logrus.Entry) []tide.Context {
	var failed []tide.Context
	for _, ctx := range contexts {
		if string(ctx.Context) == statusContext {
			continue
		}
		if cc.IsOptional(string(ctx.Context)) {
			continue
		}
		if ctx.State != githubql.StatusStateSuccess {
			failed = append(failed, ctx)
		}
	}
	for _, c := range cc.MissingRequiredContexts(contextsToStrings(contexts)) {
		failed = append(failed, newExpectedContext(c))
	}

	log.WithFields(logrus.Fields{
		"total_context_count":  len(contexts),
		"context_names":        contextsToStrings(contexts),
		"failed_context_count": len(failed),
		"failed_context_names": contextsToStrings(contexts),
	}).Debug("Filtered out failed contexts")
	return failed
}

// newExpectedContext creates a Context with Expected state.
func newExpectedContext(c string) tide.Context {
	return tide.Context{
		Context:     githubql.String(c),
		State:       githubql.StatusStateExpected,
		Description: githubql.String(""),
	}
}

// contextsToStrings converts a list Context to a list of string
func contextsToStrings(contexts []tide.Context) []string {
	var names []string
	for _, c := range contexts {
		names = append(names, string(c.Context))
	}
	return names
}

// mergeChecker provides a function to check if a PR can be merged with
// the requested method and does not have a merge conflict.
// It caches results and should be cleared periodically with clearCache()
type mergeChecker struct {
	config config.Getter
	ghc    githubClient

	sync.Mutex
	cache map[config.OrgRepo]map[github.PullRequestMergeType]bool
}

func (m *mergeChecker) clearCache() {
	// Only do this once per token reset since it could be a bit expensive for
	// Tide instances that handle hundreds of repos.
	ticker := time.NewTicker(time.Hour)
	for {
		<-ticker.C
		m.Lock()
		m.cache = make(map[config.OrgRepo]map[github.PullRequestMergeType]bool)
		m.Unlock()
	}
}

func (m *mergeChecker) repoMethods(orgRepo config.OrgRepo) (map[github.PullRequestMergeType]bool, error) {
	m.Lock()
	defer m.Unlock()

	repoMethods, ok := m.cache[orgRepo]
	if !ok {
		fullRepo, err := m.ghc.GetRepo(orgRepo.Org, orgRepo.Repo)
		if err != nil {
			return nil, err
		}
		logrus.WithFields(logrus.Fields{
			"org":              orgRepo.Org,
			"repo":             orgRepo.Repo,
			"AllowMergeCommit": fullRepo.AllowMergeCommit,
			"AllowSquashMerge": fullRepo.AllowSquashMerge,
			"AllowRebaseMerge": fullRepo.AllowRebaseMerge,
		}).Debug("GetRepo returns these values for repo methods")
		repoMethods = map[github.PullRequestMergeType]bool{
			github.MergeMerge:  fullRepo.AllowMergeCommit,
			github.MergeSquash: fullRepo.AllowSquashMerge,
			github.MergeRebase: fullRepo.AllowRebaseMerge,
		}
		m.cache[orgRepo] = repoMethods
	}
	return repoMethods, nil
}

// isAllowed checks if a PR does not have merge conflicts and requests an
// allowed merge method. If there is no error it returns a string explanation if
// not allowed or "" if allowed.
func (m *mergeChecker) isAllowed(pr *tide.PullRequest) (string, error) {
	if pr.Mergeable == githubql.MergeableStateConflicting {
		return "PR has a merge conflict.", nil
	}
	mergeMethod, err := prMergeMethod(m.config().Tide, pr)
	if err != nil {
		// This should be impossible.
		return "", fmt.Errorf("Programmer error! Failed to determine a merge method: %w", err)
	}
	orgRepo := config.OrgRepo{Org: string(pr.Repository.Owner.Login), Repo: string(pr.Repository.Name)}
	repoMethods, err := m.repoMethods(orgRepo)
	if err != nil {
		return "", fmt.Errorf("error getting repo data: %w", err)
	}
	if allowed, exists := repoMethods[mergeMethod]; !exists {
		// Should be impossible as well.
		return "", fmt.Errorf("Programmer error! PR requested the unrecognized merge type %q", mergeMethod)
	} else if !allowed {
		return fmt.Sprintf("Merge type %q disallowed by repo settings", mergeMethod), nil
	}
	return "", nil
}

func prMergeMethod(c config.Tide, pr *tide.PullRequest) (github.PullRequestMergeType, error) {
	repo := config.OrgRepo{Org: string(pr.Repository.Owner.Login), Repo: string(pr.Repository.Name)}
	method := c.MergeMethod(repo)
	squashLabel := c.SquashLabel
	rebaseLabel := c.RebaseLabel
	mergeLabel := c.MergeLabel
	if squashLabel != "" || rebaseLabel != "" || mergeLabel != "" {
		labelCount := 0
		for _, prlabel := range pr.Labels.Nodes {
			switch string(prlabel.Name) {
			case "":
				continue
			case squashLabel:
				method = github.MergeSquash
				labelCount++
			case rebaseLabel:
				method = github.MergeRebase
				labelCount++
			case mergeLabel:
				method = github.MergeMerge
				labelCount++
			}
			if labelCount > 1 {
				return "", fmt.Errorf("conflicting merge method override labels")
			}
		}
	}
	return method, nil
}

func newMergeChecker(cfg config.Getter, ghc githubClient) *mergeChecker {
	m := &mergeChecker{
		config: cfg,
		ghc:    ghc,
		cache:  map[config.OrgRepo]map[github.PullRequestMergeType]bool{},
	}

	go m.clearCache()
	return m
}
