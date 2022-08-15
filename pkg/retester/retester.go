package retester

import (
	"context"
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"time"

	githubql "github.com/shurcooL/githubv4"
	"github.com/sirupsen/logrus"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/test-infra/prow/config"
	"k8s.io/test-infra/prow/git/v2"
	"k8s.io/test-infra/prow/github"
	"k8s.io/test-infra/prow/tide"
	"sigs.k8s.io/yaml"
)

type githubClient interface {
	GetCombinedStatus(org, repo, ref string) (*github.CombinedStatus, error)
	GetRef(string, string, string) (string, error)
	QueryWithGitHubAppsSupport(ctx context.Context, q interface{}, vars map[string]interface{}, org string) error
	CreateComment(owner, repo string, number int, comment string) error
}

// pullRequest represents GitHub PR and number of retests.
type pullRequest struct {
	PRSha              string      `json:"pr_sha,omitempty"`
	BaseSha            string      `json:"base_sha,omitempty"`
	RetestsForPrSha    int         `json:"retests_for_pr_sha,omitempty"`
	RetestsForBaseSha  int         `json:"retests_for_base_sha,omitempty"`
	LastConsideredTime metav1.Time `json:"last_considered_time,omitempty"`
}

type backoffCache struct {
	cache          map[string]*pullRequest
	file           string
	cacheRecordAge time.Duration
	logger         *logrus.Entry
}

func (b *backoffCache) loadFromDisk() error {
	if b.file == "" {
		return nil
	}
	if _, err := os.Stat(b.file); errors.Is(err, os.ErrNotExist) {
		b.logger.WithField("file", b.file).Info("cache file does not exit")
		return nil
	}
	bytes, err := ioutil.ReadFile(b.file)
	if err != nil {
		return fmt.Errorf("failed to read file %s: %w", b.file, err)
	}
	cache := map[string]*pullRequest{}
	if err := yaml.Unmarshal(bytes, &cache); err != nil {
		return fmt.Errorf("failed to unmarshal: %w", err)
	}
	for key, pr := range cache {
		if age := time.Since(pr.LastConsideredTime.Time); age > b.cacheRecordAge {
			b.logger.WithField("key", key).WithField("LastConsideredTime", pr.LastConsideredTime.Time).
				WithField("age", age).Info("deleting old record from cache")
			delete(cache, key)
		}
	}
	b.cache = cache
	return nil
}

func (b *backoffCache) saveToDisk() (ret error) {
	if b.file == "" {
		return nil
	}
	bytes, err := yaml.Marshal(b.cache)
	if err != nil {
		return fmt.Errorf("failed to marshal: %w", err)
	}
	// write to a temp file and rename it to the cache file to ensure "atomic write":
	// either it is complete or nothing
	tmpFile, err := ioutil.TempFile(filepath.Dir(b.file), "tmp-backoff-cache")
	if err != nil {
		return fmt.Errorf("failed to create a temp file: %w", err)
	}
	tmp := tmpFile.Name()
	defer func() {
		// do nothing when the file does not exist, e.g., write failed, or it has been renamed.
		if _, err := os.Stat(tmp); errors.Is(err, os.ErrNotExist) {
			return
		}
		if err := os.Remove(tmp); err != nil {
			ret = fmt.Errorf("failed to delete file %s: %w", tmp, err)
		}
	}()

	if err := ioutil.WriteFile(tmp, bytes, 0644); err != nil {
		return fmt.Errorf("failed to write file %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, b.file); err != nil {
		return fmt.Errorf("failed to rename file from %s to %s: %w", tmp, b.file, err)
	}
	return ret
}

// Config is retester configuration for all configured repos and orgs.
// It has three levels: global, org, and repo. A specific level overrides general ones.
type Config struct {
	Retester Retester `json:"retester"`
}

// Retester is global level configuration for retester configuration.
// Policy is overridden by specific levels when they are enabled.
type Retester struct {
	RetesterPolicy `json:",inline"`
	Oranizations   map[string]Oranization `json:"orgs,omitempty"`
}

// Oranization is org level configuration for retester configuration.
// Policy is overridden by repo level when it is enabled.
type Oranization struct {
	RetesterPolicy `json:",inline"`
	Repos          map[string]Repo `json:"repos"`
}

// Repo is repo level configuration for retester configuration.
// Policy overridden all general levels.
type Repo struct {
	RetesterPolicy `json:",inline"`
}

// RetesterPolicy for the retester/org/repo.
// When merging policies, a 0 value results in inheriting the parent policy.
// False in level repo means disabled repo. Nothing can change that.
// True/False in level org means enabled/disabled org. But repo can be disabled/enabled.
type RetesterPolicy struct {
	MaxRetestsForShaAndBase int   `json:"max_retests_for_sha_and_base,omitempty"`
	MaxRetestsForSha        int   `json:"max_retests_for_sha,omitempty"`
	Enabled                 *bool `json:"enabled,omitempty"`
}

// LoadConfig loads retester configuration via file.
func LoadConfig(configFilePath string) (*Config, error) {
	data, err := ioutil.ReadFile(configFilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config %w", err)
	}

	var config Config
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config %w", err)
	}

	return &config, nil
}

// RetestController represents a retest controller which controls what the retester does.
type RetestController struct {
	ghClient     githubClient
	gitClient    git.ClientFactory
	configGetter config.Getter

	logger *logrus.Entry

	usesGitHubApp bool
	backoff       *backoffCache

	config *Config
}

func (c *Config) GetRetesterPolicy(org, repo string) (RetesterPolicy, error) {
	policy := RetesterPolicy{}
	if c.Retester.RetesterPolicy == policy && len(c.Retester.Oranizations) == 0 {
		return policy, nil
	}
	if orgStruct, ok := c.Retester.Oranizations[org]; ok && orgStruct.Enabled != nil {
		policy.Enabled = orgStruct.Enabled
		var repoStruct Repo
		if repoStruct, ok = orgStruct.Repos[repo]; ok && repoStruct.Enabled != nil {
			policy.Enabled = repoStruct.Enabled
			if *repoStruct.Enabled {
				// set max retests repo level value
				if repoStruct.MaxRetestsForSha != 0 {
					policy.MaxRetestsForSha = repoStruct.MaxRetestsForSha
				}
				if repoStruct.MaxRetestsForShaAndBase != 0 {
					policy.MaxRetestsForShaAndBase = repoStruct.MaxRetestsForShaAndBase
				}
			} else {
				return RetesterPolicy{}, nil
			}
		}
		if *orgStruct.Enabled {
			// set max retests org level value
			if orgStruct.MaxRetestsForSha != 0 && policy.MaxRetestsForSha == 0 {
				policy.MaxRetestsForSha = orgStruct.MaxRetestsForSha
			}
			if orgStruct.MaxRetestsForShaAndBase != 0 && policy.MaxRetestsForShaAndBase == 0 {
				policy.MaxRetestsForShaAndBase = orgStruct.MaxRetestsForShaAndBase
			}
		}
		if !*policy.Enabled && (c.Retester.Enabled == nil || !*c.Retester.Enabled) {
			return RetesterPolicy{}, nil
		}
	} else if c.Retester.Enabled == nil || !*c.Retester.Enabled {
		return RetesterPolicy{}, nil
	}
	// set max retests default value
	if policy.MaxRetestsForSha == 0 {
		policy.MaxRetestsForSha = c.Retester.MaxRetestsForSha
	}
	if policy.MaxRetestsForShaAndBase == 0 {
		policy.MaxRetestsForShaAndBase = c.Retester.MaxRetestsForShaAndBase
	}
	return policy, nil
}

func validatePolicies(policy RetesterPolicy) []error {
	var errs []error
	if policy.Enabled != nil {
		if *policy.Enabled {
			if policy.MaxRetestsForSha < 0 {
				errs = append(errs, fmt.Errorf("max_retest_for_sha has invalid value: %d", policy.MaxRetestsForSha))
			}
			if policy.MaxRetestsForShaAndBase < 0 {
				errs = append(errs, fmt.Errorf("max_retests_for_sha_and_base has invalid value: %d", policy.MaxRetestsForShaAndBase))
			}
			if policy.MaxRetestsForSha < policy.MaxRetestsForShaAndBase {
				errs = append(errs, fmt.Errorf("max_retest_for_sha value can't be lower than max_retests_for_sha_and_base value: %d < %d", policy.MaxRetestsForSha, policy.MaxRetestsForShaAndBase))
			}
		} else {
			return nil
		}
	}
	return errs
}

// NewController generates a retest controller.
func NewController(ghClient githubClient, cfg config.Getter, gitClient git.ClientFactory, usesApp bool, cacheFile string, cacheRecordAge time.Duration, config *Config) *RetestController {
	logger := logrus.NewEntry(logrus.StandardLogger())

	ret := &RetestController{
		ghClient:      ghClient,
		gitClient:     gitClient,
		configGetter:  cfg,
		logger:        logger,
		usesGitHubApp: usesApp,
		backoff:       &backoffCache{cache: map[string]*pullRequest{}, file: cacheFile, cacheRecordAge: cacheRecordAge, logger: logger},
		config:        config,
	}
	if err := ret.backoff.loadFromDisk(); err != nil {
		logger.WithError(err).Warn("Failed to load backoff cache from disk")
	}
	return ret
}

func prUrl(pr tide.PullRequest) string {
	return fmt.Sprintf("https://github.com/%s/%s/pull/%d", pr.Repository.Owner.Login, pr.Repository.Name, pr.Number)
}

// Run implements the business of the controller: filters out the pull requests satisfying the Tide's merge criteria except some required job failed and issue "/retest required" command on them.
func (c *RetestController) Run() error {
	// Input: Tide Config
	// Output: A list of PRs that are filter out by the queries in Tide's config
	candidates, err := findCandidates(c.configGetter, c.ghClient, c.usesGitHubApp, c.logger)
	if err != nil {
		return fmt.Errorf("failed to find retestable candidates: %w", err)
	}

	logrus.Infof("Found %d candidates for retest (pass label criteria, fail some tests)", len(candidates))
	for _, pr := range candidates {
		logrus.Infof("Candidate PR: %s", prUrl(pr))
	}

	candidates = c.enabledPRs(candidates)
	logrus.Infof("Remaining %d candidates for retest (from an enabled org or repo)", len(candidates))

	candidates, err = c.atLeastOneRequiredJob(candidates)
	if err != nil {
		return fmt.Errorf("failed to filter candidate PRs that have at least one required job: %w", err)
	}

	logrus.Infof("Remaining %d candidates for retest (fail at least one required prowjob)", len(candidates))
	for _, pr := range candidates {
		logrus.Infof("Candidate PR: %s", prUrl(pr))
	}

	var errs []error
	for _, pr := range candidates {
		errs = append(errs, c.retestOrBackoff(pr))
	}

	if err := c.backoff.saveToDisk(); err != nil {
		errs = append(errs, fmt.Errorf("failed to save cache to disk: %w", err))
	}
	logrus.Info("Sync finished")
	return utilerrors.NewAggregate(errs)
}

type retestBackoffAction int

const (
	retestBackoffHold = iota
	retestBackoffPause
	retestBackoffRetest
)

func (b *backoffCache) check(pr tide.PullRequest, baseSha string, config *Config, policy RetesterPolicy) (retestBackoffAction, string) {
	key := prKey(&pr)
	if _, has := b.cache[key]; !has {
		b.cache[key] = &pullRequest{}
	}
	record := b.cache[key]
	record.LastConsideredTime = metav1.Now()
	if currentPRSha := string(pr.HeadRefOID); record.PRSha != currentPRSha {
		record.PRSha = currentPRSha
		record.RetestsForPrSha = 0
		record.RetestsForBaseSha = 0
	}
	if record.BaseSha != baseSha {
		record.BaseSha = baseSha
		record.RetestsForBaseSha = 0
	}

	if record.RetestsForPrSha == policy.MaxRetestsForSha {
		record.RetestsForPrSha = 0
		record.RetestsForBaseSha = 0
		return retestBackoffHold, fmt.Sprintf("Revision %s was retested %d times: holding", record.PRSha, policy.MaxRetestsForSha)
	}

	if record.RetestsForBaseSha == policy.MaxRetestsForShaAndBase {
		return retestBackoffPause, fmt.Sprintf("Revision %s was retested %d times against base HEAD %s: pausing", record.PRSha, policy.MaxRetestsForShaAndBase, record.BaseSha)
	}

	record.RetestsForBaseSha++
	record.RetestsForPrSha++

	return retestBackoffRetest, fmt.Sprintf("Remaining retests: %d against base HEAD %s and %d for PR HEAD %s in total", policy.MaxRetestsForShaAndBase-record.RetestsForBaseSha, record.BaseSha, policy.MaxRetestsForSha-record.RetestsForPrSha, record.PRSha)
}

func (c *RetestController) createComment(pr tide.PullRequest, cmd, message string) {
	comment := fmt.Sprintf("%s\n\n%s\n", cmd, message)
	if err := c.ghClient.CreateComment(string(pr.Repository.Owner.Login), string(pr.Repository.Name), int(pr.Number), comment); err != nil {
		c.logger.WithField("comment", comment).WithError(err).Error("failed to create a comment")
	}
}

func (c *RetestController) retestOrBackoff(pr tide.PullRequest) error {
	branchRef := string(pr.BaseRef.Prefix) + string(pr.BaseRef.Name)
	baseSha, err := c.ghClient.GetRef(string(pr.Repository.Owner.Login), string(pr.Repository.Name), strings.TrimPrefix(branchRef, "refs/"))
	if err != nil {
		return err
	}

	var policy RetesterPolicy
	org := string(pr.Repository.Owner.Login)
	repo := string(pr.Repository.Name)
	if policy, err = c.config.GetRetesterPolicy(org, repo); err != nil {
		return fmt.Errorf("failed to get the max retests: %w", err)
	}
	if validationErrors := validatePolicies(policy); len(validationErrors) != 0 {
		return fmt.Errorf("failed to validate retester policy: %v", validationErrors)
	}

	action, message := c.backoff.check(pr, baseSha, c.config, policy)
	switch action {
	case retestBackoffHold:
		c.createComment(pr, "/hold", message)
	case retestBackoffPause:
		c.logger.Infof("%s: %s (%s)", prUrl(pr), "no comment", message)
	case retestBackoffRetest:
		c.createComment(pr, "/retest-required", message)
	}
	return nil
}

func findCandidates(config config.Getter, gc githubClient, usesGitHubAppsAuth bool, logger *logrus.Entry) (map[string]tide.PullRequest, error) {
	prs, err := query(config, gc, usesGitHubAppsAuth, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to query GitHub for prs: %w", err)
	}

	return prs, nil
}

func (c *RetestController) atLeastOneRequiredJob(candidates map[string]tide.PullRequest) (map[string]tide.PullRequest, error) {
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

func (c *RetestController) presubmitsForPRByContext(pr tide.PullRequest) map[string]config.Presubmit {
	presubmits := map[string]config.Presubmit{}

	presubmitsForRepo := c.configGetter().GetPresubmitsStatic(string(pr.Repository.Owner.Login) + "/" + string(pr.Repository.Name))

	for _, ps := range presubmitsForRepo {
		if ps.ContextRequired() && ps.CouldRun(string(pr.BaseRef.Name)) {
			presubmits[ps.Context] = ps
		}
	}

	return presubmits
}

func (c *RetestController) enabledPRs(candidates map[string]tide.PullRequest) map[string]tide.PullRequest {
	output := map[string]tide.PullRequest{}
	for key, pr := range candidates {
		org := string(pr.Repository.Owner.Login)
		repo := string(pr.Repository.Name)
		policy, err := c.config.GetRetesterPolicy(org, repo)
		if err != nil {
			c.logger.WithError(err).Warn("Failed to get retester policy")
		}
		if validationErrors := validatePolicies(policy); len(validationErrors) != 0 {
			c.logger.Warnf("Failed to validate retester policy: %v", validationErrors)
		}
		if policy.Enabled != nil {
			if *policy.Enabled {
				output[key] = pr
			} else {
				c.logger.Infof("PR %s is not from an enabled org or repo", key)
			}
		}
	}
	return output
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
