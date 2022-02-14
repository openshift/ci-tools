package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"sync"
	"time"

	githubql "github.com/shurcooL/githubv4"
	"github.com/sirupsen/logrus"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/test-infra/pkg/flagutil"
	"k8s.io/test-infra/prow/config"
	prowflagutil "k8s.io/test-infra/prow/flagutil"
	configflagutil "k8s.io/test-infra/prow/flagutil/config"
	"k8s.io/test-infra/prow/github"
	"k8s.io/test-infra/prow/tide"
)

type githubClient interface {
	QueryWithGitHubAppsSupport(ctx context.Context, q interface{}, vars map[string]interface{}, org string) error
}

type options struct {
	config configflagutil.ConfigOptions
	github prowflagutil.GitHubOptions

	dryRun bool

	orgRaw prowflagutil.Strings
	orgs   sets.String
}

func (o *options) Validate() error {
	for _, group := range []flagutil.OptionGroup{&o.github, &o.config} {
		if err := group.Validate(o.dryRun); err != nil {
			return err
		}
	}
	return nil
}

func gatherOptions() options {
	o := options{}
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)

	fs.BoolVar(&o.dryRun, "dry-run", true, "Dry run for testing. Uses API tokens but does not mutate.")
	fs.Var(&o.orgRaw, "org", "A GitHub org to run the retester. Can be passed multiple times.")

	for _, group := range []flagutil.OptionGroup{&o.github, &o.config} {
		group.AddFlags(fs)
	}

	if err := fs.Parse(os.Args[1:]); err != nil {
		logrus.WithError(err).Fatal("could not parse input")
	}

	o.orgs = sets.NewString(o.orgRaw.Strings()...)
	return o
}

func main() {
	o := gatherOptions()
	if err := o.Validate(); err != nil {
		logrus.Fatalf("Invalid options: %v", err)
	}

	gc, err := o.github.GitHubClient(o.dryRun)
	if err != nil {
		logrus.WithError(err).Fatal("Error creating github client.")
	}

	configAgent, err := o.config.ConfigAgent()
	if err != nil {
		logrus.WithError(err).Fatal("Error starting config agent.")
	}
	cfg := configAgent.Config

	// TODO: Should this be a Tide-like loop or should we react on events (or both)?

	// Input: Tide Config
	// Output: A list of PRs that would merge but have failing jobs
	candidates, err := findCandidates(cfg, gc, o.github.AppPrivateKeyPath != "", o.orgs, logrus.NewEntry(logrus.StandardLogger()))
	if err != nil {
		logrus.WithError(err).Fatal("Error finding candidates.")
	}

	// Input: A list of PRs that would merge but have failing jobs
	// Output: A subset of input PRs whose jobs are actually required for merge
	candidates = atLeastOneFailingRequiredJob(candidates)

	// Input: A list of PRs that would merge but have failing required jobs
	// Output: A subset of input PRs that are *not* in a back-off (whatever the back-off is)
	notInBackOff(candidates)

	// TODO: One day I will be useful
	time.Sleep(time.Hour)
}

func findCandidates(config config.Getter, gc githubClient, usesGitHubAppsAuth bool, orgs sets.String, logger *logrus.Entry) (map[string]tide.PullRequest, error) {
	prs, err := query(config, gc, usesGitHubAppsAuth, orgs, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to query GitHub for prs: %w", err)
	}
	// TODO Remove the PR with failing jobs
	// 1. ask github what the failing jobs are on the PR
	// 2. ask ? if the failing job is required
	return prs, nil
}

// refactor out the query function from the tide's controller
// https://github.com/kubernetes/test-infra/blob/0d18a317a517e1bdb9a2c728a46fbeb3642445dd/prow/tide/tide.go#L450
func query(config config.Getter, gc githubClient, usesGitHubAppsAuth bool, orgs sets.String, logger *logrus.Entry) (map[string]tide.PullRequest, error) {
	lock := sync.Mutex{}
	wg := sync.WaitGroup{}
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
			org, q, i := org, q, i
			if !orgs.Has(org) {
				continue
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				results, err := search(gc.QueryWithGitHubAppsSupport, logger, q, time.Time{}, time.Now(), org)

				lock.Lock()
				defer lock.Unlock()
				if err != nil && len(results) == 0 {
					logger.WithField("query", q).WithError(err).Warn("Failed to execute query.")
					errs = append(errs, fmt.Errorf("query %d, err: %w", i, err))
					return
				}
				if err != nil {
					logger.WithError(err).WithField("query", q).Warning("found partial results")
				}

				for _, pr := range results {
					prs[prKey(&pr)] = pr
				}
			}()
		}
	}
	wg.Wait()

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

func atLeastOneFailingRequiredJob(input map[string]tide.PullRequest) map[string]tide.PullRequest {
	return input
}

func notInBackOff(input map[string]tide.PullRequest) map[string]tide.PullRequest {
	return input
}
