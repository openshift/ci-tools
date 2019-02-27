package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/sirupsen/logrus"

	pjapi "k8s.io/test-infra/prow/apis/prowjobs/v1"
	prowconfig "k8s.io/test-infra/prow/config"
	prowgithub "k8s.io/test-infra/prow/github"
	pjdwapi "k8s.io/test-infra/prow/pod-utils/downwardapi"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	templateapi "github.com/openshift/api/template/v1"
	templatescheme "github.com/openshift/client-go/template/clientset/versioned/scheme"

	"github.com/openshift/ci-operator-prowgen/pkg/diffs"
	"github.com/openshift/ci-operator-prowgen/pkg/rehearse"
)

func loadClusterConfig() (*rest.Config, error) {
	clusterConfig, err := rest.InClusterConfig()
	if err == nil {
		return clusterConfig, nil
	}

	credentials, err := clientcmd.NewDefaultClientConfigLoadingRules().Load()
	if err != nil {
		return nil, fmt.Errorf("could not load credentials from config: %v", err)
	}

	clusterConfig, err = clientcmd.NewDefaultClientConfig(*credentials, &clientcmd.ConfigOverrides{}).ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("could not load client configuration: %v", err)
	}
	return clusterConfig, nil
}

type options struct {
	dryRun       bool
	noFail       bool
	allowVolumes bool
	debugLogPath string

	candidatePath  string
	rehearsalLimit int
}

func gatherOptions() options {
	o := options{}
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)

	fs.BoolVar(&o.dryRun, "dry-run", true, "Whether to actually submit rehearsal jobs to Prow")
	fs.BoolVar(&o.noFail, "no-fail", true, "Whether to actually end unsuccessfuly when something breaks")
	fs.BoolVar(&o.allowVolumes, "allow-volumes", false, "Allows jobs with extra volumes to be rehearsed")

	fs.StringVar(&o.debugLogPath, "debug-log", "", "Alternate file for debug output, defaults to stderr")
	fs.StringVar(&o.candidatePath, "candidate-path", "", "Path to a openshift/release working copy with a revision to be tested")

	fs.IntVar(&o.rehearsalLimit, "rehearsal-limit", 15, "Upper limit of jobs attempted to rehearse (if more jobs would be rehearsed, none will)")

	fs.Parse(os.Args[1:])
	return o
}

func validateOptions(o options) error {
	if len(o.candidatePath) == 0 {
		return fmt.Errorf("--candidate-path was not provided")
	}
	return nil
}

const (
	misconfigurationOutput = `[ERROR] pj-rehearse: misconfiguration

pj-rehearse could not process its necessary inputs properly. No rehearsal
jobs were run. This is likely a pj-rehearse job configuration problem.`
	rehearseFailureOutput = `[ERROR] pj-rehearse: rehearsal tool failure

pj-rehearse attempted to submit jobs for rehearsal, but it failed to either
submit them or to fetch their results. This is either a pj-rehearse bug or
an infrastructure issue.`
	jobsFailureOutput = `[ERROR] pj-rehearse: rehearsed jobs failure

pj-rehearse rehearsed jobs and at least one of them failed. This means that
job would fail when executed against the current HEAD of the target branch.`
)

func gracefulExit(suppressFailures bool, message string) {
	if message != "" {
		fmt.Fprintln(os.Stderr, message)
	}

	if suppressFailures {
		os.Exit(0)
	}
	os.Exit(1)
}

func main() {
	o := gatherOptions()
	err := validateOptions(o)
	if err != nil {
		logrus.WithError(err).Fatal("invalid options")
		gracefulExit(o.noFail, misconfigurationOutput)
	}

	jobSpec, err := pjdwapi.ResolveSpecFromEnv()
	if err != nil {
		logrus.WithError(err).Error("could not read JOB_SPEC")
		gracefulExit(o.noFail, misconfigurationOutput)
	}

	prFields := logrus.Fields{prowgithub.OrgLogField: jobSpec.Refs.Org, prowgithub.RepoLogField: jobSpec.Refs.Repo}
	logger := logrus.WithFields(prFields)

	if jobSpec.Type != pjapi.PresubmitJob {
		logger.Info("Not able to rehearse jobs when not run in the context of a presubmit job")
		// Exiting successfuly will make pj-rehearsal job not fail when run as a
		// in a batch job. Such failures would be confusing and unactionable
		gracefulExit(true, misconfigurationOutput)
	}

	prNumber := jobSpec.Refs.Pulls[0].Number
	logger = logrus.WithField(prowgithub.PrLogField, prNumber)
	logger.Info("Rehearsing Prow jobs for a configuration PR")

	var clusterConfig *rest.Config
	if !o.dryRun {
		clusterConfig, err = loadClusterConfig()
		if err != nil {
			logger.WithError(err).Error("could not load cluster clusterConfig")
			gracefulExit(o.noFail, misconfigurationOutput)
		}
	}

	prowConfig, prowPRConfig, err := getProwConfigs(o.candidatePath, jobSpec.Refs.BaseSHA)
	if err != nil {
		logger.WithError(err).Error("could not load prow configs")
		gracefulExit(o.noFail, misconfigurationOutput)
	}

	pjclient, err := rehearse.NewProwJobClient(clusterConfig, prowConfig.ProwJobNamespace, o.dryRun)
	if err != nil {
		logger.WithError(err).Error("could not create a ProwJob client")
		gracefulExit(o.noFail, misconfigurationOutput)
	}

	debugLogger := logrus.New()
	debugLogger.Level = logrus.DebugLevel
	if o.debugLogPath != "" {
		if f, err := os.OpenFile(o.debugLogPath, os.O_APPEND|os.O_WRONLY|os.O_CREATE, os.ModePerm); err == nil {
			defer f.Close()
			debugLogger.Out = f
		} else {
			logger.WithError(err).Error("could not open debug log file")
			gracefulExit(o.noFail, "")
		}
	}
	loggers := rehearse.Loggers{Job: logger, Debug: debugLogger.WithField(prowgithub.PrLogField, prNumber)}

	changedPresubmits := diffs.GetChangedPresubmits(prowConfig, prowPRConfig, logger)
	rehearsals := rehearse.ConfigureRehearsalJobs(changedPresubmits, o.candidatePath, prNumber, loggers, o.allowVolumes)
	if len(rehearsals) == 0 {
		logger.Info("no jobs to rehearse have been found")
		os.Exit(0)
	} else if len(rehearsals) > o.rehearsalLimit {
		jobCountFields := logrus.Fields{
			"rehearsal-threshold": o.rehearsalLimit,
			"rehearsal-jobs":      len(rehearsals),
		}
		logger.WithFields(jobCountFields).Info("Would rehearse too many jobs, will not proceed")
		os.Exit(0)
	}

	masterTemplates, prTemplates, err := getTemplates(o.candidatePath, jobSpec.Refs.BaseSHA)
	if err != nil {
		logger.WithError(err).Error("could not load templates")
	}

	changedTemplates := diffs.GetChangedTemplates(masterTemplates, prTemplates, logger)
	for name := range changedTemplates {
		logger.WithField("template-name", name).Info("Changed template")
	}

	executor := rehearse.NewExecutor(rehearsals, prNumber, o.candidatePath, jobSpec.Refs, o.dryRun, loggers, pjclient)
	success, err := executor.ExecuteJobs()
	if err != nil {
		logger.WithError(err).Error("Failed to rehearse jobs")
		gracefulExit(o.noFail, rehearseFailureOutput)
	}
	if !success {
		logger.Error("Some jobs failed their rehearsal runs")
		gracefulExit(o.noFail, jobsFailureOutput)
	}
	logger.Info("All jobs were rehearsed successfuly")
}

func getCurrentSHA(repoPath string) (string, error) {
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = repoPath
	sha, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("'%s' failed with error=%v", cmd.Args, err)
	}

	return strings.TrimSpace(string(sha)), nil
}

func gitCheckout(candidatePath, baseSHA string) error {
	cmd := exec.Command("git", "checkout", baseSHA)
	cmd.Dir = candidatePath
	stdoutStderr, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("'%s' failed with out: %s and error %v", cmd.Args, stdoutStderr, err)
	}
	return nil
}

func getProwConfigs(candidatePath, baseSHA string) (*prowconfig.Config, *prowconfig.Config, error) {
	currentSHA, err := getCurrentSHA(candidatePath)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get SHA of current HEAD: %v", err)
	}

	candidateConfigPath := filepath.Join(candidatePath, diffs.ConfigInRepoPath)
	candidateJobConfigPath := filepath.Join(candidatePath, diffs.JobConfigInRepoPath)

	prowPRConfig, err := prowconfig.Load(candidateConfigPath, candidateJobConfigPath)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to load PR's Prow config: %v", err)
	}

	if err := gitCheckout(candidatePath, baseSHA); err != nil {
		return nil, nil, fmt.Errorf("could not checkout worktree: %v", err)
	}

	masterProwConfig, err := prowconfig.Load(candidateConfigPath, candidateJobConfigPath)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to load master's Prow config: %v", err)
	}

	if err := gitCheckout(candidatePath, currentSHA); err != nil {
		return nil, nil, fmt.Errorf("failed to check out tested revision back: %v", err)
	}

	return masterProwConfig, prowPRConfig, nil
}

func getTemplates(candidatePath string, baseSHA string) (map[string]*templateapi.Template, map[string]*templateapi.Template, error) {
	templatesPath := filepath.Join(candidatePath, diffs.TemplatesPath)
	currentSHA, err := getCurrentSHA(candidatePath)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get SHA of current HEAD: %v", err)
	}

	prTemplates, err := parseTemplates(templatesPath)
	if err != nil {
		return nil, nil, err
	}

	if err := gitCheckout(candidatePath, baseSHA); err != nil {
		return nil, nil, fmt.Errorf("could not checkout worktree: %v", err)
	}
	masterTemplates, err := parseTemplates(templatesPath)
	if err != nil {
		return nil, nil, err
	}

	if err := gitCheckout(candidatePath, currentSHA); err != nil {
		return nil, nil, fmt.Errorf("failed to check out tested revision back: %v", err)
	}

	return masterTemplates, prTemplates, nil
}

func parseTemplates(templatePath string) (map[string]*templateapi.Template, error) {
	templates := make(map[string]*templateapi.Template)
	err := filepath.Walk(templatePath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return fmt.Errorf("prevent panic by handling failure accessing a path %q: %v", path, err)
		}

		if isYAML(path, info) {
			contents, err := ioutil.ReadFile(path)
			if err != nil {
				return fmt.Errorf("could not read file %s for template: %v", path, err)
			}

			if obj, _, err := templatescheme.Codecs.UniversalDeserializer().Decode(contents, nil, nil); err == nil {
				if template, ok := obj.(*templateapi.Template); ok {
					if len(template.Name) == 0 {
						template.Name = filepath.Base(path)
						template.Name = strings.TrimSuffix(template.Name, filepath.Ext(template.Name))
					}
					templates[template.Name] = template
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("error walking the path %q: %v", templatePath, err)
	}
	return templates, nil
}

func isYAML(file string, info os.FileInfo) bool {
	return !info.IsDir() && (filepath.Ext(file) == ".yaml" || filepath.Ext(file) == ".yml")
}
