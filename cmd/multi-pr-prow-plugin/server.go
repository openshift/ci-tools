package main

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/sirupsen/logrus"

	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	prowv1 "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	prowconfig "sigs.k8s.io/prow/pkg/config"
	"sigs.k8s.io/prow/pkg/github"
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
)

var (
	testwithCommand = regexp.MustCompile(fmt.Sprintf(`(?mi)^%s\s+(?P<job>[-\w./]+)\s+(?P<prs>(?:[-\w./#]+\s*)+)\s*$`, testwithPrefix))
	// TODO(sgoeddel): we will likely want to add abort functionality, eventually
)

type githubClient interface {
	CreateComment(owner, repo string, number int, comment string) error
	GetPullRequest(org, repo string, number int) (*github.PullRequest, error)
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
				additionalPRs = append(additionalPRs, *pr)
			}

			jobRuns = append(jobRuns, jobRun{
				JobMetadata:   *jobMetadata,
				OriginPR:      originPR,
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

	refs := createRefsForPullRequests(append(jr.AdditionalPRs, jr.OriginPR), ciopConfig)
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
		return nil, fmt.Errorf("no ref for requested test included in command")
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

func createRefsForPullRequests(prs []github.PullRequest, ciopConfig *api.ReleaseBuildConfiguration) []prowv1.Refs {
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

	var refs []prowv1.Refs
	for prBase := range prsByBase {
		ref := prowv1.Refs{
			Org:       prBase.org,
			Repo:      prBase.repo,
			BaseRef:   prBase.ref,
			PathAlias: ciopConfig.DeterminePathAlias(prBase.org, prBase.repo),
			BaseSHA:   prsByBase[prBase][0].Base.SHA, //TODO(sgoeddel): It would be better if we used the oldest base SHA rather than just the first in the list, but this mimics prpqr_reconciller, and is unlikely to result in many issues
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

	return refs
}

func (s *server) reportFailure(message string, err error, org, repo, user string, number int, l *logrus.Entry) {
	comment := fmt.Sprintf("@%s, `testwith`: %s. ERROR: \n ```\n%v\n```\n", user, message, err)
	if err := s.ghc.CreateComment(org, repo, number, comment); err != nil {
		l.WithError(err).Error("failed to create comment")
	}
}
