package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	githubql "github.com/shurcooL/githubv4"
	"github.com/sirupsen/logrus"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/test-infra/prow/config"
	"k8s.io/test-infra/prow/git/v2"
	"k8s.io/test-infra/prow/github"
	"k8s.io/test-infra/prow/tide"
)

type retestController struct {
	ghClient     githubClient
	gitClient    git.ClientFactory
	configGetter config.Getter

	logger *logrus.Entry

	usesGitHubApp bool
}

func newController(ghClient githubClient, cfg config.Getter, gitClient git.ClientFactory, usesApp bool) *retestController {
	return &retestController{
		ghClient:      ghClient,
		gitClient:     gitClient,
		configGetter:  cfg,
		logger:        logrus.NewEntry(logrus.StandardLogger()),
		usesGitHubApp: usesApp,
	}
}

func (c *retestController) sync() error {
	// Input: Tide Config
	// Output: A list of PRs that are filter out by the queries in Tide's config
	candidates, err := findCandidates(c.configGetter, c.ghClient, c.usesGitHubApp, c.logger)
	if err != nil {
		return fmt.Errorf("Failed to find retestable candidates: %w", err)
	}

	logrus.Infof("Found %d candidates for retest (pass label criteria, fail some tests)", len(candidates))
	for _, pr := range candidates {
		logrus.Infof("Candidate PR: https://github.com/%s/%s/pull/%d", pr.Repository.Owner.Login, pr.Repository.Name, pr.Number)
	}

	candidates, err = c.atLeastOneRequiredJob(candidates)
	if err != nil {
		return fmt.Errorf("Failed to filter candidate PRs that have at least one required job: %w", err)
	}

	logrus.Infof("Remaining %d candidates for retest (fail at least one required prowjob)", len(candidates))
	for _, pr := range candidates {
		logrus.Infof("Candidate PR: https://github.com/%s/%s/pull/%d", pr.Repository.Owner.Login, pr.Repository.Name, pr.Number)
	}

	// Input: A list of PRs that would merge but have failing required jobs
	// Output: A subset of input PRs that are *not* in a back-off (whatever the back-off is)
	candidates = notInBackOff(candidates)

	// Actually comment...
	retest(candidates)

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

func (c *retestController) atLeastOneRequiredJob(candidates map[string]tide.PullRequest) (map[string]tide.PullRequest, error) {
	output := map[string]tide.PullRequest{}
	for key, pr := range candidates {
		// Get all non-optional Prowjobs configured for this org/repo/branch that could run on this PR
		presubmits := c.presubmitsForPRByContext(pr)

		c.logger.Infof("Pull request %s has %d relevant presubmits configured", key, len(presubmits))

		// If this PR cannot ever trigger any required Prowjob, `/retest-required` will never do anything useful for it
		if len(presubmits) == 0 {
			continue
		}

		// Get all contexts on the HEAD of the PR
		contexts, err := headContexts(c.ghClient, pr)
		if err != nil {
			c.logger.WithError(err).Errorf("Failed to get contexts for %s", key)
			return nil, err
		}
		c.logger.Infof("HEAD commit of PR %s has %d contexts", key, len(contexts))

		for _, ctx := range contexts {
			if ctx.State != githubql.StatusStateFailure {
				continue
			}
			// It is enough to find a single failed context that corresponds to a required Prowjob
			if ps, has := presubmits[string(ctx.Context)]; has {
				c.logger.Infof("PR %s fails required job %s (context=%s)", key, ps.Name, ctx.Context)
				output[key] = pr
				break
			}
		}
		if _, ok := output[key]; !ok {
			c.logger.Infof("PR %s has no failing context of a required Prowjob", key)
		}
	}
	return output, nil
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

func notInBackOff(input map[string]tide.PullRequest) map[string]tide.PullRequest {
	return input
}

func retest(prs map[string]tide.PullRequest) {

}

func (c *retestController) presubmitsForPRByContext(pr tide.PullRequest) map[string]config.Presubmit {
	presubmits := map[string]config.Presubmit{}

	presubmitsForRepo := c.configGetter().GetPresubmitsStatic(string(pr.Repository.Owner.Login) + "/" + string(pr.Repository.Name))

	for _, ps := range presubmitsForRepo {
		if ps.ContextRequired() && ps.CouldRun(string(pr.BaseRef.Name)) {
			presubmits[ps.Context] = ps
		}
	}

	return presubmits
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
func headContexts(ghc githubClient, pr tide.PullRequest) ([]tide.Context, error) {
	// We didn't get the head commit from the query (the commits must not be
	// logically ordered) so we need to specifically ask GitHub for the status
	// and coerce it to a graphql type.
	org := string(pr.Repository.Owner.Login)
	repo := string(pr.Repository.Name)
	combined, err := ghc.GetCombinedStatus(org, repo, string(pr.HeadRefOID))
	if err != nil {
		return nil, fmt.Errorf("failed to get the combined status: %w", err)
	}

	contexts := make([]tide.Context, 0, len(combined.Statuses))
	for _, status := range combined.Statuses {
		contexts = append(contexts, tide.Context{
			Context:     githubql.String(status.Context),
			Description: githubql.String(status.Description),
			State:       githubql.StatusState(strings.ToUpper(status.State)),
		})
	}

	return contexts, nil
}
