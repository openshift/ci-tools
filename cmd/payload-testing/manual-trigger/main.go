package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/ghodss/yaml"
	"github.com/sirupsen/logrus"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	v1 "k8s.io/test-infra/prow/apis/prowjobs/v1"
	pjclientset "k8s.io/test-infra/prow/client/clientset/versioned"
	prowconfig "k8s.io/test-infra/prow/config"
	"k8s.io/test-infra/prow/config/secret"
	prowflagutil "k8s.io/test-infra/prow/flagutil"
	"k8s.io/test-infra/prow/github"
	"k8s.io/test-infra/prow/pjutil"

	"github.com/openshift/ci-tools/pkg/api"
	ciopconfig "github.com/openshift/ci-tools/pkg/config"
	"github.com/openshift/ci-tools/pkg/prowgen"
	"github.com/openshift/ci-tools/pkg/registry/server"
	"github.com/openshift/ci-tools/pkg/util"
)

var (
	configResolverAddress = api.URLForService(api.ServiceConfig)
)

func makeProwjob(configuration *api.ReleaseBuildConfiguration, pr *github.PullRequest, base *api.Metadata, inject *api.MetadataWithTest, prowConfig *prowconfig.Config) *prowconfig.Periodic {
	fakeProwgenInfo := &prowgen.ProwgenInfo{
		Metadata: *base,
		Config:   ciopconfig.Prowgen{},
	}

	var periodic *prowconfig.Periodic
	for i := range configuration.Tests {
		if configuration.Tests[i].As == inject.Test {
			jobBaseGen := prowgen.NewProwJobBaseBuilderForTest(configuration, fakeProwgenInfo, prowgen.NewCiOperatorPodSpecGenerator(), configuration.Tests[i])
			jobBaseGen.PodSpec.Add(prowgen.InjectTestFrom(inject))
			jobBaseGen.PodSpec.Add(prowgen.CustomHashInput(time.Now().String()))

			// TODO(muller): Solve cluster assignment
			jobBaseGen.Cluster("build02")

			periodic = prowgen.GeneratePeriodicForTest(jobBaseGen, fakeProwgenInfo, "@yearly", "", false, configuration.CanonicalGoRepository)
			break
		}
		// TODO(muller): Handle the not found case
	}

	// TODO(muller): Name the job something better
	periodic.Name = fmt.Sprintf("%s-%d-%s-%s", base.Repo, pr.Number, inject.Variant, inject.Test)
	// TODO(muller): Solve cluster assignment
	periodic.Cluster = "build02"

	// This is a copy of createRefs() from pjutil.go
	// TODO(muller): DRY
	periodic.ExtraRefs[0] = v1.Refs{
		Org:      pr.Base.Repo.Owner.Login,
		Repo:     pr.Base.Repo.Name,
		RepoLink: pr.Base.Repo.HTMLURL,
		BaseRef:  pr.Base.Ref,
		BaseSHA:  pr.Base.SHA,
		BaseLink: fmt.Sprintf("%s/commit/%s", pr.Base.Repo.HTMLURL, pr.Base.SHA),
		Pulls: []v1.Pull{
			{
				Number:     pr.Number,
				Author:     pr.User.Login,
				SHA:        pr.Head.SHA,
				Title:      pr.Title,
				Link:       pr.HTMLURL,
				AuthorLink: pr.User.HTMLURL,
				CommitLink: fmt.Sprintf("%s/pull/%d/commits/%s", pr.Base.Repo.HTMLURL, pr.Number, pr.Head.SHA),
			},
		},
	}
	err := prowConfig.DefaultPeriodic(periodic)
	if err != nil {
		panic("Failed to roundtrip Prow config: read")
	}

	return periodic
}

type options struct {
	pullRequest    string
	test           string
	prowConfigPath string
	aggregationID  string

	confirm bool

	github prowflagutil.GitHubOptions
}

func gatherOptions() options {
	o := options{}
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)

	fs.StringVar(&o.pullRequest, "pull-request", "", "Pull Request to test (org/repo#number)")
	fs.StringVar(&o.test, "test", "", "Coordinates of the test to execute (org/repo@branch__variant:test)")
	fs.BoolVar(&o.confirm, "confirm", false, "Set to true to actually submit the create ProwJob")
	fs.StringVar(&o.prowConfigPath, "prow-config-path", "", "Path to Prow configuration file")
	fs.StringVar(&o.aggregationID, "aggregation-id", "", "If set, release.openshift.io/aggregation-id label will be set to this value on the spawned job")

	o.github.AddFlags(fs)

	if err := fs.Parse(os.Args[1:]); err != nil {
		logrus.WithError(err).Fatalf("cannot parse args: '%s'", os.Args[1:])
	}
	return o
}

func (o *options) Validate() error {
	if o.pullRequest == "" {
		return errors.New("--pull-request must not be empty")
	}
	if o.test == "" {
		return errors.New("--test must not be empty")
	}
	if o.prowConfigPath == "" {
		return errors.New("--prow-config-path must not be empty")
	}
	return o.github.Validate(false)
}

type githubClient interface {
	GetPullRequest(org, repo string, number int) (*github.PullRequest, error)
}

func getPullRequest(ghc githubClient, pr string) (*github.PullRequest, error) {
	parts := strings.Split(pr, "#")
	if len(parts) != 2 {
		return nil, fmt.Errorf("pull request not in org/repo#number format: %s", pr)
	}
	prNumber, err := strconv.Atoi(parts[1])
	if err != nil {
		return nil, fmt.Errorf("pull request not in org/repo#number format: %s", pr)
	}
	orgRepo := strings.Split(parts[0], "/")
	if len(parts) != 2 {
		return nil, fmt.Errorf("pull request not in org/repo#number format: %s", pr)
	}
	org, repo := orgRepo[0], orgRepo[1]
	return ghc.GetPullRequest(org, repo, prNumber)
}

func main() {
	o := gatherOptions()
	if err := o.Validate(); err != nil {
		logrus.WithError(err).Fatal("Invalid options")
	}

	var token string
	if o.github.TokenPath != "" {
		token = o.github.TokenPath
	}
	if o.github.AppPrivateKeyPath != "" {
		token = o.github.AppPrivateKeyPath
	}
	if token != "" {
		if err := secret.Add(token); err != nil {
			logrus.WithError(err).Fatal("Error starting secrets agent.")
		}
	}

	githubClient, err := o.github.GitHubClient(!o.confirm)
	if err != nil {
		logrus.WithError(err).Fatal("Error getting GitHub client.")
	}

	pr, err := getPullRequest(githubClient, o.pullRequest)
	if err != nil {
		logrus.WithError(err).Fatal("Failed to get information about tested PR")
	}
	base := &api.Metadata{
		Org:    pr.Base.Repo.Owner.Login,
		Repo:   pr.Base.Repo.Name,
		Branch: pr.Base.Ref,
	}

	inject, err := api.MetadataTestFromString(o.test)
	if err != nil {
		logrus.WithError(err).Fatal("Failed to parse --test")
	}

	rc := server.NewResolverClient(configResolverAddress)
	config, err := rc.ConfigWithTest(base, inject)
	if err != nil {
		logrus.WithError(err).Fatal("Failed to load ci-operator configuration")
	}

	prowConfig, err := prowconfig.Load(o.prowConfigPath, "", nil, "")
	prowjobConfig := makeProwjob(config, pr, base, inject, prowConfig)
	if err != nil {
		logrus.Fatal("Failed to make a Prowjob")
	}
	// TODO(muller): Need to propagate this to trigger
	extraLabels := map[string]string{}
	if o.aggregationID != "" {
		extraLabels["release.openshift.io/aggregation-id"] = o.aggregationID
	}
	prowjob := pjutil.NewProwJob(pjutil.PeriodicSpec(*prowjobConfig), extraLabels, nil)

	if !o.confirm {
		jobAsYAML, err := yaml.Marshal(prowjob)
		if err != nil {
			logrus.WithError(err).Fatal("failed to marshal the prowjob to YAML")
		}
		fmt.Println(string(jobAsYAML))
		os.Exit(0)
	}

	logrus.Info("getting cluster config")
	clusterConfig, err := util.LoadClusterConfig()
	if err != nil {
		logrus.WithError(err).Fatal("failed to load cluster configuration")
	}

	pjcset, err := pjclientset.NewForConfig(clusterConfig)
	if err != nil {
		logrus.WithError(err).Fatal("failed to create prowjob clientset")
	}

	pjclient := pjcset.ProwV1().ProwJobs("ci")

	logrus.WithFields(pjutil.ProwJobFields(&prowjob)).Info("submitting a new prowjob")
	_, err = pjclient.Create(context.TODO(), &prowjob, metav1.CreateOptions{})
	if err != nil {
		logrus.WithError(err).Fatal("failed to submit the prowjob")
	}
}
