package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/sirupsen/logrus"

	"k8s.io/apimachinery/pkg/util/sets"
	prowconfig "sigs.k8s.io/prow/pkg/config"
	"sigs.k8s.io/prow/pkg/flagutil"
	configflagutil "sigs.k8s.io/prow/pkg/flagutil/config"
	prowpluginconfig "sigs.k8s.io/prow/pkg/flagutil/plugins"
	"sigs.k8s.io/prow/pkg/github"
	"sigs.k8s.io/prow/pkg/logrusutil"
	"sigs.k8s.io/prow/pkg/plugins"
	"sigs.k8s.io/prow/pkg/pod-utils/downwardapi"

	"github.com/openshift/ci-tools/pkg/config"
)

const (
	cherrypickPlugin      = "cherrypick"
	cherrypickRobot       = "openshift-cherrypick-robot"
	branchProtectionRobot = "openshift-merge-robot"
	standard              = appCheckMode("standard")
	tide                  = appCheckMode("tide")
)

type appCheckMode string

type options struct {
	config                configflagutil.ConfigOptions
	bots                  flagutil.Strings
	appName               string
	appCheckMode          string
	checkBranchProtection bool
	ignore                flagutil.Strings
	repos                 flagutil.Strings
	releaseRepoPath       string
	flagutil.GitHubOptions
	pluginConfig prowpluginconfig.PluginOptions
}

func gatherOptions() options {
	o := options{}
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)

	fs.Var(&o.bots, "bot", "Check if this bot is a collaborator. Can be passed multiple times.")
	fs.StringVar(&o.appName, "app", "openshift-ci", "The name of the app that is checking bot configuration, and for which installation will be checked")
	fs.StringVar(&o.appCheckMode, "app-check-mode", "standard", "Which mode to check for app installation: 'standard' checks always, 'tide' only checks when tide is configured for the repo")
	fs.BoolVar(&o.checkBranchProtection, "check-branch-protection", true, fmt.Sprintf("Check branch protection configs in order to verify %s has admin access if necessary. Enabled by default.", branchProtectionRobot))
	fs.Var(&o.ignore, "ignore", "Ignore a repo or entire org. Formatted org or org/repo. Can be passed multiple times.")
	fs.Var(&o.repos, "repo", "Specifically check only an org/repo. Can be passed multiple times.")
	fs.StringVar(&o.releaseRepoPath, "candidate-path", "", "Path to a openshift/release working copy with a revision to be tested")
	o.pluginConfig.AddFlags(fs)

	o.GitHubOptions.AddFlags(fs)
	o.config.AddFlags(fs)

	if err := fs.Parse(os.Args[1:]); err != nil {
		logrus.WithError(err).Fatal("could not parse input")
	}
	return o
}

func (o *options) validate() error {
	if err := o.config.Validate(true); err != nil {
		return fmt.Errorf("error when validating prow config: %w", err)
	}

	if appCheckMode(o.appCheckMode) != standard && appCheckMode(o.appCheckMode) != tide {
		return fmt.Errorf("app-check-mode of %s not recognized, must be: %s or %s", o.appCheckMode, standard, tide)
	}

	return o.GitHubOptions.Validate(true)
}

type automationClient interface {
	IsMember(org, user string) (bool, error)
	IsCollaborator(org, repo, user string) (bool, error)
	IsAppInstalled(org, repo string) (bool, error)
	HasPermission(org, repo, user string, permissions ...string) (bool, error)
	GetRepo(owner, name string) (github.FullRepo, error)
	GetOrg(name string) (*github.Organization, error)
}

func main() {
	logrusutil.ComponentInit()
	logger := logrus.WithField("component", "check-gh-automation")

	o := gatherOptions()

	if err := o.validate(); err != nil {
		logger.Fatalf("validation error: %v", err)
	}

	prowAgent, err := o.config.ConfigAgent()
	if err != nil {
		logger.Fatalf("error loading prow config: %v", err)
	}
	tideQueries := prowAgent.Config().Tide.Queries.QueryMap()

	var pluginAgent *plugins.ConfigAgent
	if o.pluginConfig.PluginConfigPath != "" {
		logger.Infof("Loading plugin configuration from: %s", o.pluginConfig.PluginConfigPath)
		var err error
		pluginAgent, err = o.pluginConfig.PluginAgent()
		if err != nil {
			logger.Fatalf("Error creating plugin agent: %v", err)
		}
		logger.Info("Plugin configuration loaded successfully.")
	} else {
		logger.Info("No plugin configuration provided, continuing without a plugin agent.")
	}

	client, err := o.GitHubOptions.GitHubClient(false)
	if err != nil {
		logger.Fatalf("error creating client: %v", err)
	}

	repos := determineRepos(o, prowAgent, logger)
	failing, err := checkRepos(repos, o.bots.Strings(), o.appName, o.ignore.StringSet(), appCheckMode(o.appCheckMode), o.checkBranchProtection, client, logger, pluginAgent, tideQueries, prowAgent)
	if err != nil {
		logger.Fatalf("error checking repos: %v", err)
	}

	if len(failing) > 0 {
		logger.Fatalf("Repo(s) missing github automation: %s", strings.Join(failing, ", "))
	}

	logger.Infof("All repos have github automation configured.")
}

func determineRepos(o options, prowAgent *prowconfig.Agent, logger *logrus.Entry) []string {
	repos := o.repos.Strings()
	if len(repos) > 0 {
		return repos
	}

	if o.releaseRepoPath != "" {
		return gatherModifiedRepos(o.releaseRepoPath, logger)
	}

	return sets.List(prowAgent.Config().AllRepos)
}

func checkRepos(repos []string, bots []string, appName string, ignore sets.Set[string], mode appCheckMode, checkBranchProtection bool, client automationClient, logger *logrus.Entry, pluginAgent *plugins.ConfigAgent, tideQueries *prowconfig.QueryMap, prowAgent *prowconfig.Agent) ([]string, error) {
	logger.Infof("checking %d repo(s): %s", len(repos), strings.Join(repos, ", "))
	failing := sets.New[string]()
	for _, orgRepo := range repos {
		split := strings.Split(orgRepo, "/")
		org, repo := split[0], split[1]
		repoLogger := logger.WithFields(logrus.Fields{
			"org":  org,
			"repo": repo,
		})

		fullRepo, err := client.GetRepo(org, repo)
		if err != nil {
			logger.Errorf("Error obtaining repository from github: %s/%s: %v", org, repo, err)
			return nil, fmt.Errorf("error obtaining repository from github: %s/%s: %w", org, repo, err)
		}

		if ignore.Has(org) || ignore.Has(orgRepo) {
			repoLogger.Infof("skipping ignored repo")
			continue
		}

		var missingBots []string
		if len(bots) > 0 {
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
				failing.Insert(orgRepo)
				repoLogger.Errorf("bots that are not collaborators: %s", strings.Join(missingBots, ", "))
			} else {
				repoLogger.Info("all bots are org members or repo collaborators")
			}
		} else {
			repoLogger.Info("no bots provided to check")
		}

		fullOrg, err := client.GetOrg(org)
		if err != nil {
			logger.Errorf("Error obtaining org from github: %s: %v", org, err)
			return nil, fmt.Errorf("error obtaining org from github: %s: %w", org, err)
		}
		if checkBranchProtection {
			orgConfig := prowAgent.Config().BranchProtection.GetOrg(org)
			branchProtectionEnabled := true // By default, it is turned on in absence of configuration stating otherwise
			if orgConfig != nil {
				unmanagedOrgLevel := orgConfig.Policy.Unmanaged
				branchProtectionEnabled = unmanagedOrgLevel == nil || !*unmanagedOrgLevel

				repoLevel, exists := orgConfig.Repos[repo]
				if exists {
					// if "unmanaged" is set to "true" it is disabled. If it is "nil" or "false" we consider it enabled
					branchProtectionEnabled = repoLevel.Unmanaged == nil || !*repoLevel.Unmanaged
				}
			}

			// If branch protection is configured, verify admin access for the hardcoded admin bot
			// also branch protection should be used only on public repos or paid plans
			if branchProtectionEnabled {
				logger.Infof("Branch protection is enabled for %s/%s", org, repo)

				if fullRepo.Private && fullOrg.Plan.Name == "free" {
					logger.Errorf("Branch protection is enabled, the org %s has a free plan, the repository %s must be public", org, repo)
					failing.Insert(orgRepo)
				}

				// Hardcoded merge robot
				hasAdminAccess, err := client.HasPermission(org, repo, branchProtectionRobot, "admin")
				if err != nil {
					logger.Errorf("Error checking admin access for bot %s in %s/%s: %v", branchProtectionRobot, org, repo, err)
					return nil, fmt.Errorf("error checking admin access for bot %s in %s/%s: %w", branchProtectionRobot, org, repo, err)
				}
				if !hasAdminAccess {
					logger.Errorf("Bot %s does not have admin access in %s/%s with branch protection enabled", branchProtectionRobot, org, repo)
					failing.Insert(orgRepo)
				} else {
					logger.Infof("Bot %s has admin access in %s/%s", branchProtectionRobot, org, repo)
				}
			} else {
				logger.Infof("Branch protection is not enabled for %s/%s", org, repo)
			}
		}

		if pluginAgent != nil {
			externalPlugins := pluginAgent.Config().ExternalPlugins[orgRepo]
			if externalPlugins == nil {
				externalPlugins = pluginAgent.Config().ExternalPlugins[org]
			}
			for _, plugin := range externalPlugins {
				if plugin.Name == cherrypickPlugin {
					isMember, err := client.IsMember(org, cherrypickRobot)
					if err != nil {
						return nil, fmt.Errorf("failed to determine membership status of 'openshift-cherrypick-robot' in '%s': %w", org, err)
					}
					hasAccess, err := client.HasPermission(org, repo, cherrypickRobot, "read", "write", "admin")
					if err != nil {
						return nil, fmt.Errorf("error checking access level (read/write/admin) for 'openshift-cherrypick-robot' in '%s/%s': %w", org, repo, err)
					}
					if !isMember && !hasAccess {
						repoLogger.Errorf("'openshift-cherrypick-robot' lacks required permissions (read/write/admin) or org membership in '%s/%s'", org, repo)
						failing.Insert(orgRepo)
					} else {
						repoLogger.Infof("'openshift-cherrypick-robot' has sufficient permissions (member or read/write/admin) in '%s/%s'", org, repo)
					}
				}
			}
		}

		checkAppInstall := true
		if mode == tide {
			queriesForRepo := tideQueries.ForRepo(prowconfig.OrgRepo{Org: org, Repo: repo})
			if len(queriesForRepo) > 0 {
				repoLogger.Infof("at least one tide query exists for repo, checking app install")
			} else {
				repoLogger.Infof("no tide query exists for repo, ignoring app install check")
				checkAppInstall = false
			}
		}
		if checkAppInstall {
			appInstalled, err := client.IsAppInstalled(org, repo)
			if err != nil {
				return nil, fmt.Errorf("unable to determine if %s app is installed on %s/%s: %w", appName, org, repo, err)
			}
			if !appInstalled {
				failing.Insert(orgRepo)
				repoLogger.Errorf("%s app is not installed for repo", appName)
			} else {
				repoLogger.Infof("%s app is installed for repo", appName)
			}
		}
	}

	return sets.List(failing), nil
}

const maxRepos = 10

func gatherModifiedRepos(releaseRepoPath string, logger *logrus.Entry) []string {
	jobSpec, err := downwardapi.ResolveSpecFromEnv()
	if err != nil {
		logger.Fatalf("error resolving JobSpec: %v", err)
	}
	configs, err := config.GetAddedConfigs(releaseRepoPath, jobSpec.Refs.BaseSHA)
	if err != nil {
		logger.Fatalf("error determining changed configs: %v", err)
	}

	orgRepos := sets.Set[string]{}
	for _, c := range configs {
		path := strings.TrimPrefix(c, config.CiopConfigInRepoPath+"/")
		split := strings.Split(path, "/")
		if split[1] == ".config.prowgen" {
			continue
		}

		orgRepos.Insert(fmt.Sprintf("%s/%s", split[0], split[1]))
	}

	if orgRepos.Len() > maxRepos {
		logger.Warnf("Found %d repos, which is more than we will check for a PR. It is likely that this PR is a config update on many repos, and doesn't need to be checked.", orgRepos.Len())
		return []string{}
	}

	return sets.List(orgRepos)
}
