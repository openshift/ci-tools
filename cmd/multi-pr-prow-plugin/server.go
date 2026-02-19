package main

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/sirupsen/logrus"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	prowv1 "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	prowconfig "sigs.k8s.io/prow/pkg/config"
	"sigs.k8s.io/prow/pkg/github"
	"sigs.k8s.io/prow/pkg/kube"
	"sigs.k8s.io/prow/pkg/pjutil"
	"sigs.k8s.io/prow/pkg/pluginhelp"
	"sigs.k8s.io/prow/pkg/plugins/trigger"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/dispatcher"
	"github.com/openshift/ci-tools/pkg/jobconfig"
	"github.com/openshift/ci-tools/pkg/prowgen"
	registryserver "github.com/openshift/ci-tools/pkg/registry/server"
)

const (
	testwithPrefix            = "/testwith"
	maxPRs                    = 20
	defaultMultiRefJobTimeout = 8 * time.Hour
	testwithLabel             = "ci.openshift.io/testwith"
	githubURL                 = "https://github.com/"
)

var (
	testwithCommand = regexp.MustCompile(fmt.Sprintf(`(?mi)^%s\s+(?P<job>[-\w./]+)\s+(?P<prs>(?:[-\w./#:]+\s*)+)\s*$`, testwithPrefix))
	abortCommand    = fmt.Sprintf("%s abort", testwithPrefix)
)

type githubClient interface {
	CreateComment(owner, repo string, number int, comment string) error
	GetPullRequest(org, repo string, number int) (*github.PullRequest, error)
	GetRef(org, repo, ref string) (string, error)
}

func helpProvider(_ []prowconfig.OrgRepo) (*pluginhelp.PluginHelp, error) {
	pluginHelp := &pluginhelp.PluginHelp{
		Description: "The multi-pr-prow-plugin triggers the requested test against source(s) built from the origin PR and the requested additional PRs",
	}
	pluginHelp.AddCommand(pluginhelp.Command{
		Usage:       "/testwith",
		Description: "The multi-pr-prow-plugin /testwith command triggers the requested test against source(s) built from the origin PR and the requested additional PRs",
		WhoCanUse:   "Members of the trusted organization for the repo.",
		Examples:    []string{"/testwith openshift/kubernetes/master/e2e openshift/kubernetes#1234 openshift/installer#999"},
	})
	pluginHelp.AddCommand(pluginhelp.Command{
		Usage:       "/testwith abort",
		Description: "Abort all active multi-pr presubmit jobs where the operand PR is the orign PR on the job",
		WhoCanUse:   "Members of the trusted organization for the repo.",
		Examples:    []string{"/testwith abort"},
	})
	return pluginHelp, nil
}

type trustedChecker interface {
	trustedUser(author, org, repo string, num int) (bool, error)
}

type githubTrustedChecker struct {
	githubClient github.Client
}

func (c *githubTrustedChecker) trustedUser(author, org, repo string, _ int) (bool, error) {
	triggerTrustedResponse, err := trigger.TrustedUser(c.githubClient, false, []string{}, "", author, org, repo)
	if err != nil {
		return false, fmt.Errorf("error checking %s for trust: %w", author, err)
	}
	return triggerTrustedResponse.IsTrusted, nil
}

type ciOpConfigResolver interface {
	Config(*api.Metadata) (*api.ReleaseBuildConfiguration, error)
}

type prowConfigGetter interface {
	Defaulter() periodicDefaulter
	Config() *prowconfig.Config
}

type wrappedProwConfigAgent struct {
	pc *prowconfig.Agent
}

func (w *wrappedProwConfigAgent) Defaulter() periodicDefaulter {
	return w.pc.Config()
}

func (w *wrappedProwConfigAgent) Config() *prowconfig.Config {
	return w.pc.Config()
}

type periodicDefaulter interface {
	DefaultPeriodic(periodic *prowconfig.Periodic) error
}

type jobClusterCache struct {
	clusterForJob map[string]string
	lastCleared   time.Time
}

type server struct {
	ghc                githubClient
	ctx                context.Context
	kubeClient         ctrlruntimeclient.Client
	namespace          string
	trustedChecker     trustedChecker
	ciOpConfigResolver ciOpConfigResolver
	prowConfigGetter   prowConfigGetter
	dispatcherClient   dispatcher.Client
	jobClusterCache
	reporter Reporter
}

func newServer(ctx context.Context,
	githubClient github.Client,
	kubeClient ctrlruntimeclient.Client,
	namespace string,
	prowConfigAgent *prowconfig.Agent,
	dispatcherAddress string,
	reporter Reporter) *server {
	return &server{
		ghc:        githubClient,
		kubeClient: kubeClient,
		ctx:        ctx,
		namespace:  namespace,
		trustedChecker: &githubTrustedChecker{
			githubClient: githubClient,
		},
		ciOpConfigResolver: registryserver.NewResolverClient(api.URLForService(api.ServiceConfig)),
		prowConfigGetter:   &wrappedProwConfigAgent{pc: prowConfigAgent},
		dispatcherClient:   dispatcher.NewClient(dispatcherAddress),
		jobClusterCache: jobClusterCache{
			clusterForJob: make(map[string]string),
			lastCleared:   time.Now(),
		},
		reporter: reporter,
	}
}

func (s *server) handleIssueComment(l *logrus.Entry, ic github.IssueCommentEvent) {
	if strings.HasPrefix(ic.Comment.Body, testwithPrefix) {
		l.Infof("handling comment: %s", ic.Comment.Body)
		_, err := s.handle(l, ic)
		if err != nil {
			org := ic.Repo.Owner.Login
			repo := ic.Repo.Name
			number := ic.Issue.Number
			user := ic.Comment.User.Login
			s.reportFailure("Error processing request", err, org, repo, user, number, l)
		}
	}
}

func (s *server) handle(l *logrus.Entry, ic github.IssueCommentEvent) ([]*prowv1.ProwJob, error) {
	var prowJobs []*prowv1.ProwJob
	if strings.HasPrefix(ic.Comment.Body, testwithPrefix) {
		l.Infof("handling comment: %s", ic.Comment.Body)
		org := ic.Repo.Owner.Login
		repo := ic.Repo.Name
		number := ic.Issue.Number
		user := ic.Comment.User.Login
		pr, err := s.ghc.GetPullRequest(org, repo, number)
		if err != nil {
			l.Errorf("error getting origin PR %s/%s#%d: %v", org, repo, number, err)
			return nil, fmt.Errorf("error getting origin PR %s/%s#%d: %w", org, repo, number, err)
		}

		trusted, err := s.trustedChecker.trustedUser(ic.Comment.User.Login, org, repo, number)
		if err != nil {
			l.WithError(err).WithField("user", ic.Comment.User.Login).Error("could not check if the user is trusted")
			return nil, fmt.Errorf("could not check if the user is trusted: %w", err)
		}
		if !trusted {
			l.WithField("user", ic.Comment.User.Login).Warn("the user is not trusted")
			return nil, fmt.Errorf("the user: %s is not trusted to trigger tests", user)
		}

		if ic.Comment.Body == abortCommand {
			abortedJobs, err := s.abortMultiPRJobs(*pr, l)
			if err != nil {
				l.WithError(err).Errorf("error aborting multi-PR jobs")
				return nil, fmt.Errorf("error aborting multi-PR jobs: %w", err)
			}
			return abortedJobs, nil
		}

		jobRuns, err := s.determineJobRuns(ic.Comment.Body, *pr)
		if err != nil {
			l.WithError(err).Warn("could not determine job runs")
			return nil, fmt.Errorf("could not determine job runs: %w", err)
		}

		for _, jr := range jobRuns {
			prowJob, err := s.generateProwJob(jr)
			if err != nil {
				l.WithError(err).Warn("could not generate prow job")
				s.reportFailure("could not generate prow job", err, org, repo, user, number, l)
				continue
			}
			logrus.Infof("submitting prowjob: %s", prowJob.ObjectMeta.Name)
			if err = s.kubeClient.Create(context.Background(), prowJob); err != nil {
				l.WithError(err).Error("could not create prow job")
				s.reportFailure("could not submit prow job", nil, org, repo, user, number, l)
				continue
			}
			prowJobs = append(prowJobs, prowJob)

			go func() {
				err := s.reporter.reportNewProwJob(prowJob, jr, l)
				if err != nil {
					l.WithError(err).Error("could not report new prow job")
				}
			}()

		}
	}

	return prowJobs, nil
}

type jobRun struct {
	JobMetadata   api.MetadataWithTest
	OriginPR      github.PullRequest
	AdditionalPRs []github.PullRequest
}

func (s *server) determineJobRuns(comment string, originPR github.PullRequest) ([]jobRun, error) {
	lines := strings.Split(comment, "\n")
	var jobRuns []jobRun
	for _, line := range lines {
		for _, match := range testwithCommand.FindAllStringSubmatch(line, -1) {
			jobIndex := testwithCommand.SubexpIndex("job")
			rawJob := match[jobIndex]
			jobMetadata, err := jobMetadataFromRawCommand(rawJob)
			if err != nil {
				return nil, err
			}

			prsIndex := testwithCommand.SubexpIndex("prs")
			var additionalPRs []github.PullRequest

			rawPRs := strings.Fields(match[prsIndex])
			if len(rawPRs) >= maxPRs {
				return nil, fmt.Errorf("%d PRs found which is more than the max of %d, will not process request", len(rawPRs), maxPRs)
			}
			for _, rawPR := range rawPRs {
				if strings.HasPrefix(rawPR, githubURL) {
					// When users copy/paste the command, GitHub likes to fully resolve the url into the value.
					// we can catch this and convert it to the proper format.
					rawPR = strings.Replace(strings.TrimPrefix(rawPR, githubURL), "/pull/", "#", 1)
				}

				orgSplit := strings.Split(rawPR, "/")
				if len(orgSplit) != 2 {
					return nil, fmt.Errorf("invalid format for additional PR: %s", rawPR)
				}
				org := orgSplit[0]
				repoNumberSplit := strings.Split(orgSplit[1], "#")
				if len(repoNumberSplit) != 2 {
					return nil, fmt.Errorf("invalid format for additional PR: %s", rawPR)
				}
				repo := repoNumberSplit[0]
				prNumber, err := strconv.Atoi(repoNumberSplit[1])
				if err != nil {
					return nil, fmt.Errorf("couldn't convert pr Number from: %s", rawPR)
				}
				pr, err := s.ghc.GetPullRequest(org, repo, prNumber)
				if err != nil {
					return nil, fmt.Errorf("couldn't get PR from GitHub: %s: %w", rawPR, err)
				}
				// For additional PRs from the same org/repo as the job, set the base branch
				// to the job's branch. This handles renamed default branches (e.g., "master" → "main").
				if pr.Base.Repo.Owner.Login == jobMetadata.Org && pr.Base.Repo.Name == jobMetadata.Repo {
					pr.Base.Ref = jobMetadata.Branch
				}
				additionalPRs = append(additionalPRs, *pr)
			}

			// Store the OriginPR with its base branch cleared. The branch is captured
			// in the job metadata, and clearing it avoids confusion when the PR was
			// opened against a renamed branch (e.g., "master" when the default is now "main").
			storedOriginPR := originPR
			storedOriginPR.Base.Ref = ""
			jobRuns = append(jobRuns, jobRun{
				JobMetadata:   *jobMetadata,
				OriginPR:      storedOriginPR,
				AdditionalPRs: additionalPRs,
			})
		}
	}

	return jobRuns, nil
}

func jobMetadataFromRawCommand(rawJob string) (*api.MetadataWithTest, error) {
	jobParts := strings.Split(rawJob, "/")
	if len(jobParts) != 4 && len(jobParts) != 5 {
		return nil, fmt.Errorf("requested job is invalid. needs to be formatted like: <org>/<repo>/<branch>/<variant?>/<job>. instead it was: %s", rawJob)
	}
	org := jobParts[0]
	repo := jobParts[1]
	branch := jobParts[2]
	var variant, test string
	if len(jobParts) == 4 {
		test = jobParts[3]
	} else {
		variant = jobParts[3]
		test = jobParts[4]
	}
	return &api.MetadataWithTest{
		Metadata: api.Metadata{
			Org:     org,
			Repo:    repo,
			Branch:  branch,
			Variant: variant,
		},
		Test: test,
	}, nil
}

func (s *server) generateProwJob(jr jobRun) (*prowv1.ProwJob, error) {
	testJobMetadata := jr.JobMetadata.Metadata
	ciopConfig, err := s.ciOpConfigResolver.Config(&testJobMetadata)
	if err != nil {
		return nil, fmt.Errorf("could not determine ci op config from metadata: %w", err)
	}
	jobName := determineProwJobName(jr)
	var periodic *prowconfig.Periodic
	for i := range ciopConfig.Tests {
		test := ciopConfig.Tests[i]
		if test.As != jr.JobMetadata.Test {
			continue
		}
		if test.Timeout == nil {
			test.Timeout = &prowv1.Duration{Duration: defaultMultiRefJobTimeout}
		} else if test.Timeout.Duration < defaultMultiRefJobTimeout {
			test.Timeout.Duration = defaultMultiRefJobTimeout
		}

		fakeProwgenInfo := &prowgen.ProwgenInfo{Metadata: testJobMetadata}
		jobBaseGen := prowgen.NewProwJobBaseBuilderForTest(ciopConfig, fakeProwgenInfo, prowgen.NewCiOperatorPodSpecGenerator(), test)
		jobBaseGen.PodSpec.Add(prowgen.InjectTestFrom(&jr.JobMetadata))
		jobBaseGen.PodSpec.Add(prowgen.CustomHashInput(jobName))

		var requestedTestName string
		if test.IsPeriodic() {
			requestedTestName = testJobMetadata.JobName(jobconfig.PeriodicPrefix, test.As)
		} else {
			requestedTestName = testJobMetadata.JobName(jobconfig.PresubmitPrefix, test.As)
		}
		cluster, err := s.clusterForJob(requestedTestName)
		if err != nil {
			return nil, err
		}
		jobBaseGen.Cluster(api.Cluster(cluster))

		periodic = prowgen.GeneratePeriodicForTest(jobBaseGen, fakeProwgenInfo)
		break
	}
	if periodic == nil {
		return nil, fmt.Errorf("BUG: test '%s' not found in injected config", jr.JobMetadata.Test)
	}
	periodic.Name = jobName

	// Normalize PR base branches to handle renamed default branches (e.g., "master" → "main").
	// PRs from the same org/repo use the branch from the job metadata. PRs from other
	// org/repos use the branch from the first matching additional PR.
	branchByRepo := map[string]string{
		jr.JobMetadata.Org + "/" + jr.JobMetadata.Repo: jr.JobMetadata.Branch,
	}
	for _, pr := range jr.AdditionalPRs {
		key := pr.Base.Repo.Owner.Login + "/" + pr.Base.Repo.Name
		if _, exists := branchByRepo[key]; !exists && pr.Base.Ref != "" {
			branchByRepo[key] = pr.Base.Ref
		}
	}
	normalizePRBranch := func(pr github.PullRequest) github.PullRequest {
		key := pr.Base.Repo.Owner.Login + "/" + pr.Base.Repo.Name
		if branch, ok := branchByRepo[key]; ok {
			pr.Base.Ref = branch
		}
		return pr
	}
	normalizedPRs := make([]github.PullRequest, 0, len(jr.AdditionalPRs)+1)
	for _, pr := range jr.AdditionalPRs {
		normalizedPRs = append(normalizedPRs, normalizePRBranch(pr))
	}
	normalizedPRs = append(normalizedPRs, normalizePRBranch(jr.OriginPR))
	refs, err := createRefsForPullRequests(normalizedPRs, s.ciOpConfigResolver, s.ghc)
	if err != nil {
		return nil, fmt.Errorf("create refs for PR: %w", err)
	}

	periodic.ExtraRefs = nil // remove any extra_refs that have been initialized
	var primaryRef *prowv1.Refs
	for _, ref := range refs {
		if testJobMetadata.Org == ref.Org && testJobMetadata.Repo == ref.Repo && testJobMetadata.Branch == ref.BaseRef {
			primaryRef = &ref
		} else {
			periodic.ExtraRefs = append(periodic.ExtraRefs, ref)
		}
	}
	if primaryRef == nil {
		return nil, errors.New("no ref for requested test included in command. The org, repo, and branch containing the requested test need to be targeted by at least one of the included PRs")
	}

	if err := s.prowConfigGetter.Defaulter().DefaultPeriodic(periodic); err != nil {
		return nil, fmt.Errorf("failed to default the ProwJob: %w", err)
	}

	originPR := jr.OriginPR
	labels := map[string]string{
		testwithLabel: fmt.Sprintf("%s.%s.%d", originPR.Base.Repo.Owner.Login, originPR.Base.Repo.Name, originPR.Number),
	}
	pj := pjutil.NewProwJob(pjutil.PeriodicSpec(*periodic), labels, nil, pjutil.RequireScheduling(s.prowConfigGetter.Config().Scheduler.Enabled))
	pj.Spec.Refs = pjutil.CompletePrimaryRefs(*primaryRef, periodic.JobBase)
	pj.Namespace = s.namespace

	return &pj, nil
}

func (s *server) clusterForJob(jobName string) (string, error) {
	if time.Now().Add(time.Minute * -15).After(s.jobClusterCache.lastCleared) {
		s.jobClusterCache.lastCleared = time.Now()
		s.jobClusterCache.clusterForJob = make(map[string]string)
	}
	if cluster, ok := s.jobClusterCache.clusterForJob[jobName]; ok {
		return cluster, nil
	}

	cluster, err := s.dispatcherClient.ClusterForJob(jobName)
	if err != nil {
		return "", fmt.Errorf("could not determine cluster for job %s: %w", jobName, err)
	}
	s.jobClusterCache.clusterForJob[jobName] = cluster

	return cluster, nil
}

func determineProwJobName(jr jobRun) string {
	formatPR := func(pr github.PullRequest) string {
		repo := pr.Base.Repo
		return fmt.Sprintf("%s-%s-%d", repo.Owner.Login, repo.Name, pr.Number)
	}
	var additionalPRs string
	for _, pr := range jr.AdditionalPRs {
		additionalPRs += formatPR(pr) + "-"
	}
	return fmt.Sprintf("multi-pr-%s-%s%s", formatPR(jr.OriginPR), additionalPRs, jr.JobMetadata.Test)
}

func createRefsForPullRequests(prs []github.PullRequest, configResolver ciOpConfigResolver, ghc githubClient) ([]prowv1.Refs, error) {
	type base struct {
		org  string
		repo string
		ref  string
	}
	prsByBase := make(map[base][]github.PullRequest)
	for _, pr := range prs {
		repo := pr.Base.Repo
		b := base{
			org:  repo.Owner.Login,
			repo: repo.Name,
			ref:  pr.Base.Ref,
		}
		prsByBase[b] = append(prsByBase[b], pr)
	}

	pathAliasFor := func(org, repo, branch string) (string, error) {
		config, err := configResolver.Config(&api.Metadata{Org: org, Repo: repo, Branch: branch})
		if err != nil {
			return "", fmt.Errorf("resolve config %s/%s@%s: %w", org, repo, branch, err)
		}
		return config.DeterminePathAlias(org, repo), nil
	}

	var refs []prowv1.Refs
	for prBase := range prsByBase {
		pathAlias, err := pathAliasFor(prBase.org, prBase.repo, prBase.ref)
		if err != nil {
			return nil, fmt.Errorf("path alias: %w", err)
		}

		// https://github.com/kubernetes-sigs/prow/blob/db89760fea406dd2813e331c3d52b53b5bcbd140/pkg/plugins/trigger/pull-request.go#L50
		baseSHA, err := ghc.GetRef(prBase.org, prBase.repo, "heads/"+prBase.ref)
		if err != nil {
			return nil, fmt.Errorf("failed to get baseSHA: %w", err)
		}

		ref := prowv1.Refs{
			Org:       prBase.org,
			Repo:      prBase.repo,
			BaseRef:   prBase.ref,
			PathAlias: pathAlias,
			BaseSHA:   baseSHA,
		}
		for _, pr := range prsByBase[prBase] {
			ref.Pulls = append(ref.Pulls, prowv1.Pull{
				Number: pr.Number,
				Author: pr.User.Login,
				SHA:    pr.Head.SHA,
				Title:  pr.Title,
			})
		}
		refs = append(refs, ref)
	}

	return refs, nil
}

func (s *server) abortMultiPRJobs(pr github.PullRequest, l *logrus.Entry) ([]*prowv1.ProwJob, error) {
	org := pr.Base.Repo.Owner.Login
	repo := pr.Base.Repo.Name
	number := pr.Number
	selector := ctrlruntimeclient.MatchingLabels{
		kube.OrgLabel:         org,
		kube.RepoLabel:        repo,
		kube.PullLabel:        strconv.Itoa(number),
		kube.ProwJobTypeLabel: string(prowv1.PresubmitJob),
		testwithLabel:         fmt.Sprintf("%s.%s.%d", org, repo, number),
	}
	jobs := &prowv1.ProwJobList{}
	err := s.kubeClient.List(context.TODO(), jobs, selector, ctrlruntimeclient.InNamespace(s.namespace))
	if err != nil {
		l.WithError(err).Error("failed to list prowjobs for pr")
	}
	l.Debugf("found %d prowjob(s) to abort", len(jobs.Items))

	var errors []error
	var abortedJobs []*prowv1.ProwJob
	for _, job := range jobs.Items {
		// Do not abort jobs that already completed
		if job.Complete() {
			continue
		}
		l.Debugf("aborting prowjob: %s", job.Name)
		job.Status.State = prowv1.AbortedState
		// We use Update and not Patch here, because we are not the authority of the .Status.State field
		// and must not overwrite changes made to it in the interim by the responsible agent.
		// The accepted trade-off for now is that this leads to failure if unrelated fields where changed
		// by another different actor.
		if err = s.kubeClient.Update(context.TODO(), &job); err != nil && !apierrors.IsConflict(err) {
			l.WithError(err).Errorf("failed to abort prowjob: %s", job.Name)
			errors = append(errors, fmt.Errorf("failed to abort prowjob %s: %w", job.Name, err))
		} else {
			l.Debugf("aborted prowjob: %s", job.Name)
		}
		abortedJobs = append(abortedJobs, &job)
	}

	return abortedJobs, utilerrors.NewAggregate(errors)
}

func (s *server) reportFailure(message string, err error, org, repo, user string, number int, l *logrus.Entry) {
	comment := fmt.Sprintf("@%s, `testwith`: %s. ERROR: \n ```\n%v\n```\n", user, message, err)
	if err := s.ghc.CreateComment(org, repo, number, comment); err != nil {
		l.WithError(err).Error("failed to create comment")
	}
}
