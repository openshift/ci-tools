package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/sirupsen/logrus"

	"k8s.io/test-infra/prow/cmd/generic-autobumper/bumper"
	"k8s.io/test-infra/prow/config/secret"
	"k8s.io/test-infra/prow/flagutil"
)

const (
	githubOrg   = "openshift"
	githubRepo  = "release"
	githubLogin = "openshift-bot"
	githubTeam  = "openshift/openshift-team-developer-productivity-test-platform"
)

var extraFiles = []string{
	"hack/images.sh",
}

type options struct {
	dryRun      bool
	githubLogin string
	gitName     string
	gitEmail    string
	targetDir   string
	assign      string
	flagutil.GitHubOptions
}

func parseOptions() options {
	var o options
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	fs.BoolVar(&o.dryRun, "dry-run", true, "Whether to actually create the pull request with github client")
	fs.StringVar(&o.githubLogin, "github-login", githubLogin, "The GitHub username to use.")
	fs.StringVar(&o.gitName, "git-name", "", "The name to use on the git commit. Requires --git-email. If not specified, uses the system default.")
	fs.StringVar(&o.gitEmail, "git-email", "", "The email to use on the git commit. Requires --git-name. If not specified, uses the system default.")
	fs.StringVar(&o.targetDir, "target-dir", "", "The directory containing the target repo.")
	fs.StringVar(&o.assign, "assign", githubTeam, "The github username or group name to assign the created pull request to.")
	o.AddFlags(fs)
	o.AllowAnonymous = true
	if err := fs.Parse(os.Args[1:]); err != nil {
		logrus.WithError(err).Errorf("cannot parse args: '%s'", os.Args[1:])
	}
	return o
}

func validateOptions(o options) error {
	if o.githubLogin == "" {
		return fmt.Errorf("--github-login cannot be empty string")
	}
	if (o.gitEmail == "") != (o.gitName == "") {
		return fmt.Errorf("--git-name and --git-email must be specified together")
	}
	if o.targetDir == "" {
		return fmt.Errorf("--target-dir is mandatory")
	}
	if o.assign == "" {
		return fmt.Errorf("--assign is mandatory")
	}
	return o.GitHubOptions.Validate(o.dryRun)
}

func main() {
	o := parseOptions()
	if err := validateOptions(o); err != nil {
		logrus.WithError(err).Fatal("Invalid arguments.")
	}

	sa := &secret.Agent{}
	if err := sa.Start([]string{o.GitHubOptions.TokenPath}); err != nil {
		logrus.WithError(err).Fatal("Failed to start secrets agent")
	}

	gc, err := o.GitHubOptions.GitHubClient(sa, o.dryRun)
	if err != nil {
		logrus.WithError(err).Fatal("error getting GitHub client")
	}

	logrus.Infof("Changing working directory to '%s' ...", o.targetDir)
	if err := os.Chdir(o.targetDir); err != nil {
		logrus.WithError(err).Fatal("Failed to change to root dir")
	}

	opts := &bumper.Options{
		GitHubOrg:           "openshift",
		GitHubRepo:          "release",
		GitHubLogin:         o.githubLogin,
		GitHubToken:         string(sa.GetTokenGenerator(o.GitHubOptions.TokenPath)()),
		GitName:             o.gitName,
		GitEmail:            o.gitEmail,
		IncludedConfigPaths: []string{"clusters/", "cluster/ci/config/prow/", "core-services/prow", "ci-operator/", "hack/"},
		ExtraFiles:          extraFiles,
		TargetVersion:       "latest",
		RemoteName:          fmt.Sprintf("https://github.com/%s/%s.git", o.githubLogin, githubRepo),
		Prefixes: []bumper.Prefix{
			{
				Name:             "Prow",
				Prefix:           "gcr.io/k8s-prow/",
				Repo:             "https://github.com/kubernetes/test-infra",
				Summarise:        true,
				ConsistentImages: true,
			},
			{
				Name:             "Boskos",
				Prefix:           "gcr.io/k8s-staging-boskos/",
				Repo:             "https://github.com/kubernetes-sigs/boskos",
				Summarise:        true,
				ConsistentImages: true,
			},
		},
	}
	images, err := bumper.UpdateReferences(opts)
	if err != nil {
		logrus.WithError(err).Fatal("Failed to update references.")
	}

	versions, err := getVersionsAndCheckConsistency(opts.Prefixes, images)
	if err != nil {
		logrus.WithError(err).Fatal("unable get get versions")
	}

	changed, err := bumper.HasChanges()
	if err != nil {
		logrus.WithError(err).Fatal("error occurred when checking changes")
	}

	if !changed {
		logrus.Info("no images updated, exiting ...")
		return
	}

	stdout := bumper.HideSecretsWriter{Delegate: os.Stdout, Censor: sa}
	stderr := bumper.HideSecretsWriter{Delegate: os.Stderr, Censor: sa}

	remoteBranch := "autobump"
	if err := bumper.MakeGitCommit(fmt.Sprintf("https://%s:%s@github.com/%s/%s.git", o.githubLogin, opts.GitHubToken, o.githubLogin, githubRepo), remoteBranch, o.gitName, o.gitEmail, opts.Prefixes, stdout, stderr, versions); err != nil {
		logrus.WithError(err).Fatal("Failed to push changes.")
	}

	if err := bumper.UpdatePR(gc, githubOrg, githubRepo, images, "/cc @"+o.assign, o.githubLogin, "master", remoteBranch, true, opts.Prefixes, versions); err != nil {
		logrus.WithError(err).Fatal("PR creation failed.")
	}
}

func getVersionsAndCheckConsistency(prefixes []bumper.Prefix, images map[string]string) (map[string][]string, error) {
	// Key is tag, value is full image.
	versions := map[string][]string{}
	for _, prefix := range prefixes {
		newVersions := 0
		for k, v := range images {
			if strings.HasPrefix(k, prefix.Prefix) {
				if _, ok := versions[v]; !ok {
					newVersions++
				}
				versions[v] = append(versions[v], k)
				if prefix.ConsistentImages && newVersions > 1 {
					return nil, fmt.Errorf("%q was supposed to be bumped consistently but was not", prefix.Name)
				}
			}
		}
	}
	return versions, nil
}
