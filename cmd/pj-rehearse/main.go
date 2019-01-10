package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/sirupsen/logrus"

	pjapi "k8s.io/test-infra/prow/apis/prowjobs/v1"
	pjclientset "k8s.io/test-infra/prow/client/clientset/versioned"
	prowconfig "k8s.io/test-infra/prow/config"

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

	changedPresubmits, err := diffs.GetChangedPresubmits(prowConfig, o.candidatePath)
	if err != nil {
		logger.WithError(err).Fatal("Failed to determine which jobs should be rehearsed")
	}

	if err := rehearse.ExecuteJobs(changedPresubmits, jobSpec, logger, rehearsalConfigs, pjclient, o.dryRun); err != nil {
		logger.WithError(err).Fatal("Failed to execute rehearsal jobs")
	}
}
