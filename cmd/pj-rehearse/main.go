package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	// TODO: Solve this properly
	"github.com/getlantern/deepcopy"
	"github.com/ghodss/yaml"
	"github.com/sirupsen/logrus"

	pjapi "k8s.io/test-infra/prow/apis/prowjobs/v1"
	pjclientset "k8s.io/test-infra/prow/client/clientset/versioned"
	pj "k8s.io/test-infra/prow/client/clientset/versioned/typed/prowjobs/v1"
	prowconfig "k8s.io/test-infra/prow/config"
	"k8s.io/test-infra/prow/pjutil"

	corev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/openshift/ci-operator-prowgen/pkg/diffs"
	"github.com/openshift/ci-operator-prowgen/pkg/rehearse"
)

func getPrNumber(jobSpec *pjapi.ProwJobSpec) int {
	return jobSpec.Refs.Pulls[0].Number
}

func getJobSpec() (*pjapi.ProwJobSpec, error) {
	specEnv := []byte(os.Getenv("JOB_SPEC"))
	if len(specEnv) == 0 {
		return nil, fmt.Errorf("JOB_SPEC not set or set to an empty string")
	}
	spec := pjapi.ProwJobSpec{}
	if err := json.Unmarshal(specEnv, &spec); err != nil {
		return nil, err
	}

	if len(spec.Refs.Pulls) > 1 {
		return nil, fmt.Errorf("Cannot rehearse in the context of a batch job")
	}

	return &spec, nil
}

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

func makeRehearsalPresubmit(source *prowconfig.Presubmit, repo string, prNumber int) (*prowconfig.Presubmit, error) {
	var rehearsal prowconfig.Presubmit
	deepcopy.Copy(&rehearsal, source)

	rehearsal.Name = fmt.Sprintf("rehearse-%d-%s", prNumber, source.Name)
	rehearsal.Context = fmt.Sprintf("ci/rehearse/%s/%s", repo, strings.TrimPrefix(source.Context, "ci/prow/"))

	if len(source.Spec.Containers) != 1 {
		return nil, fmt.Errorf("Cannot rehearse jobs with more than 1 container in Spec")
	}
	container := source.Spec.Containers[0]

	if len(container.Command) != 1 || container.Command[0] != "ci-operator" {
		return nil, fmt.Errorf("Cannot rehearse jobs that have Command different from simple 'ci-operator'")
	}

	for _, arg := range container.Args {
		if strings.HasPrefix(arg, "--git-ref") || strings.HasPrefix(arg, "-git-ref") {
			return nil, fmt.Errorf("Cannot rehearse jobs that call ci-operator with '--git-ref' arg")
		}
	}

	if len(source.Branches) != 1 {
		return nil, fmt.Errorf("Cannot rehearse jobs that run over multiple branches")
	}
	branch := strings.TrimPrefix(strings.TrimSuffix(source.Branches[0], "$"), "^")

	gitrefArg := fmt.Sprintf("--git-ref=%s@%s", repo, branch)
	rehearsal.Spec.Containers[0].Args = append(source.Spec.Containers[0].Args, gitrefArg)

	return &rehearsal, nil
}

func submitRehearsal(job *prowconfig.Presubmit, jobSpec *pjapi.ProwJobSpec, logger logrus.FieldLogger, pjclient pj.ProwJobInterface, dry bool) (*pjapi.ProwJob, error) {
	labels := make(map[string]string)
	for k, v := range job.Labels {
		labels[k] = v
	}

	pj := pjutil.NewProwJob(pjutil.PresubmitSpec(*job, *(jobSpec.Refs)), labels)
	logger.WithFields(pjutil.ProwJobFields(&pj)).Info("Submitting a new prowjob.")

	if dry {
		jobAsYAML, err := yaml.Marshal(pj)
		if err != nil {
			return nil, fmt.Errorf("Failed to marshal job to YAML: %v", err)
		}
		fmt.Printf("%s\n", jobAsYAML)
		return &pj, nil
	}

	return pjclient.Create(&pj)
}

func execute(toBeRehearsed map[string][]prowconfig.Presubmit, jobSpec *pjapi.ProwJobSpec, logger logrus.FieldLogger, rehearsalConfigs rehearse.CIOperatorConfigs, pjclient pj.ProwJobInterface, dry bool) error {
	rehearsals := []*prowconfig.Presubmit{}

	for repo, jobs := range toBeRehearsed {
		for _, job := range jobs {
			jobLogger := logger.WithFields(logrus.Fields{"target-repo": repo, "target-job": job.Name})
			rehearsal, err := makeRehearsalPresubmit(&job, repo, getPrNumber(jobSpec))
			if err != nil {
				jobLogger.WithError(err).Warn("Failed to make a rehearsal presubmit")
			} else {
				jobLogger.WithField("rehearsal-job", rehearsal.Name).Info("Created a rehearsal job to be submitted")
				rehearsalConfigs.FixupJob(rehearsal, repo)
				rehearsals = append(rehearsals, rehearsal)
			}
		}
	}

	if len(rehearsals) > 0 {
		if err := rehearsalConfigs.Create(); err != nil {
			return fmt.Errorf("failed to prepare rehearsal ci-operator config ConfigMap: %v", err)
		}
		for _, job := range rehearsals {
			created, err := submitRehearsal(job, jobSpec, logger, pjclient, dry)
			if err != nil {
				logger.WithError(err).Warn("Failed to execute a rehearsal presubmit presubmit")
			} else {
				logger.WithFields(pjutil.ProwJobFields(created)).Info("Submitted rehearsal prowjob")
			}
		}
	} else {
		logger.Warn("No job rehearsals")
	}

	return nil
}

type options struct {
	dryRun bool

	configPath    string
	jobConfigPath string

	candidatePath string
}

func gatherOptions() options {
	o := options{}
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)

	fs.BoolVar(&o.dryRun, "dry-run", true, "Whether to actually submit rehearsal jobs to Prow")

	fs.StringVar(&o.configPath, "config-path", "/etc/config/config.yaml", "Path to *master* Prow config.yaml")
	fs.StringVar(&o.jobConfigPath, "job-config-path", "", "Path to *master* Prow Prow job configs.")

	fs.StringVar(&o.candidatePath, "candidate-path", "./", "Path to a openshift/release working copy with a revision to be tested")

	fs.Parse(os.Args[1:])
	return o
}

func validateOptions(o options) error {
	if len(o.jobConfigPath) == 0 {
		return fmt.Errorf("empty --job-config-path")
	}
	return nil
}

func main() {
	o := gatherOptions()
	err := validateOptions(o)
	if err != nil {
		logrus.WithError(err).Fatal("invalid options")
	}

	jobSpec, err := getJobSpec()
	if err != nil {
		logrus.WithError(err).Fatal("could not read JOB_SPEC")
	}

	prFields := logrus.Fields{"org": jobSpec.Refs.Org, "repo": jobSpec.Refs.Repo, "PR": getPrNumber(jobSpec)}
	logger := logrus.WithFields(prFields)
	logger.Info("Rehearsing Prow jobs for a configuration PR")

	prowConfig, err := prowconfig.Load(o.configPath, o.jobConfigPath)
	if err != nil {
		logger.WithError(err).Fatal("Failed to load Prow config")
	}
	prowjobNamespace := prowConfig.ProwJobNamespace

	clusterConfig, err := loadClusterConfig()
	if err != nil {
		logger.WithError(err).Fatal("could not load cluster clusterConfig")
	}

	pjcset, err := pjclientset.NewForConfig(clusterConfig)
	if err != nil {
		logger.WithError(err).Fatal("could not create a ProwJob clientset")
	}
	pjclient := pjcset.ProwV1().ProwJobs(prowjobNamespace)

	cmcset, err := corev1.NewForConfig(clusterConfig)
	if err != nil {
		logger.WithError(err).Fatal("could not create a Core clientset")
	}
	cmclient := cmcset.ConfigMaps(prowjobNamespace)

	rehearsalConfigs := rehearse.NewCIOperatorConfigs(cmclient, getPrNumber(jobSpec), o.candidatePath, logger, o.dryRun)

	changedPresubmits, err := diffs.GetChangedPresubmits(o.configPath, o.jobConfigPath, o.candidatePath)
	if err != nil {
		logger.WithError(err).Fatal("Failed to determine which jobs should be rehearsed")
	}

	if err := execute(changedPresubmits, jobSpec, logger, rehearsalConfigs, pjclient, o.dryRun); err != nil {
		logger.WithError(err).Fatal("Failed to execute rehearsal jobs")
	}
}

// TODO: Migrate GetChangedPresubmits to accept full config
// TODO: Use shared version of loadClusterConfig
// TODO: Extract job handling to a package
