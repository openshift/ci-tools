package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/sirupsen/logrus"

	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/test-infra/prow/flagutil"
	configflagutil "k8s.io/test-infra/prow/flagutil/config"
	"k8s.io/test-infra/prow/logrusutil"
	"k8s.io/test-infra/prow/pod-utils/downwardapi"

	"github.com/openshift/ci-tools/pkg/config"
)

type options struct {
	config          configflagutil.ConfigOptions
	bots            flagutil.Strings
	ignore          flagutil.Strings
	releaseRepoPath string
	flagutil.GitHubOptions
}

func gatherOptions() options {
	o := options{}
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)

	fs.Var(&o.bots, "bot", "Check if this bot is a collaborator. Can be passed multiple times.")
	fs.Var(&o.ignore, "ignore", "Ignore a repo or entire org. Formatted org or org/repo. Can be passed multiple times.")
	fs.StringVar(&o.releaseRepoPath, "candidate-path", "", "Path to a openshift/release working copy with a revision to be tested")

	o.GitHubOptions.AddFlags(fs)
	o.config.AddFlags(fs)

	if err := fs.Parse(os.Args[1:]); err != nil {
		logrus.WithError(err).Fatal("could not parse input")
	}
	return o
}

func (o *options) validate() error {
	if len(o.bots.Strings()) < 1 {
		return errors.New("at least one bot must be configured")
	}

	// We either need the release repo path, or a proper prow config
	if o.releaseRepoPath == "" {
		if err := o.config.Validate(true); err != nil {
			return fmt.Errorf("candidate-path not provided, and error when validating prow config: %w", err)
		}
	} else {
		if o.config.ConfigPath != "" {
			return errors.New("candidate-path and prow config provided, these are mutually exclusive")
		}
	}

	return o.GitHubOptions.Validate(true)
}

type collaboratorClient interface {
	IsMember(org, user string) (bool, error)
	IsCollaborator(org, repo, user string) (bool, error)
}

func main() {
	logrusutil.ComponentInit()
	logger := logrus.WithField("component", "check-gh-automation")

	o := gatherOptions()
	if err := o.validate(); err != nil {
		logger.Fatalf("validation error: %v", err)
	}

	client, err := o.GitHubOptions.GitHubClient(false)
	if err != nil {
		logger.Fatalf("error creating client: %v", err)
	}

	var repos []string
	if o.config.ConfigPath != "" {
		configAgent, err := o.config.ConfigAgent()
		if err != nil {
			logger.Fatalf("error loading prow config: %v", err)
		}
		repos = configAgent.Config().AllRepos.List()
	} else {
		repos = gatherModifiedRepos(o.releaseRepoPath, logger)
	}
	failing, err := checkRepos(repos, o.bots.Strings(), o.ignore.StringSet(), client, logger)
	if err != nil {
		logger.Fatalf("error checking repos: %v", err)
	}

	if len(failing) > 0 {
		logger.Fatalf("Repo(s) missing github automation: %s", strings.Join(failing, ", "))
	}

	logger.Infof("All repos have github automation configured.")
}

func checkRepos(repos []string, bots []string, ignore sets.String, client collaboratorClient, logger *logrus.Entry) ([]string, error) {
	logger.Infof("checking %d repo(s): %s", len(repos), strings.Join(repos, ", "))
	var failing []string
	for _, orgRepo := range repos {
		split := strings.Split(orgRepo, "/")
		org, repo := split[0], split[1]
		repoLogger := logger.WithFields(logrus.Fields{
			"org":  org,
			"repo": repo,
		})

		if ignore.Has(org) || ignore.Has(orgRepo) {
			repoLogger.Infof("skipping ignored repo")
			continue
		}

		var missingBots []string
		for _, bot := range bots {
			isMember, err := client.IsMember(org, bot)
			if err != nil {
				return nil, fmt.Errorf("unable to determine if: %s is a member of %s: %w", bot, org, err)
			}
			if isMember {
				repoLogger.WithField("bot", bot).Info("bot is an org member")
				continue
			}

			isCollaborator, err := client.IsCollaborator(org, repo, bot)
			if err != nil {
				return nil, fmt.Errorf("unable to determine if: %s is a collaborator on %s/%s: %w", bot, org, repo, err)
			}
			if !isCollaborator {
				missingBots = append(missingBots, bot)
			}
		}

		if len(missingBots) > 0 {
			failing = append(failing, orgRepo)
			repoLogger.Errorf("bots that are not collaborators: %s", strings.Join(missingBots, ", "))
		} else {
			repoLogger.Info("all bots are org members or repo collaborators")
		}
	}

	return failing, nil
}

func gatherModifiedRepos(releaseRepoPath string, logger *logrus.Entry) []string {
	jobSpec, err := downwardapi.ResolveSpecFromEnv()
	if err != nil {
		logger.Fatalf("error resolving JobSpec: %v", err)
	}
	configs, err := config.GetAddedConfigs(releaseRepoPath, jobSpec.Refs.BaseSHA)
	if err != nil {
		logger.Fatalf("error determining changed configs: %v", err)
	}

	orgRepos := sets.String{}
	for _, c := range configs {
		path := strings.TrimPrefix(c, config.CiopConfigInRepoPath+"/")
		split := strings.Split(path, "/")
		orgRepos.Insert(fmt.Sprintf("%s/%s", split[0], split[1]))
	}

	return orgRepos.List()
}
