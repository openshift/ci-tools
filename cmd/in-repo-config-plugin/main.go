package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/sirupsen/logrus"

	pjapi "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	"sigs.k8s.io/prow/pkg/config/secret"
	prowflagutil "sigs.k8s.io/prow/pkg/flagutil"
	"sigs.k8s.io/prow/pkg/githubeventserver"
	"sigs.k8s.io/prow/pkg/interrupts"
	"sigs.k8s.io/prow/pkg/logrusutil"
	"sigs.k8s.io/prow/pkg/pjutil"
	"k8s.io/client-go/rest"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"k8s.io/apimachinery/pkg/runtime"
)

const pluginName = "in-repo-config"

type options struct {
	logLevel                 string
	githubEventServerOptions githubeventserver.Options
	github                   prowflagutil.GitHubOptions
	webhookSecretFile        string
	jobConfigDir             string
	releaseRepoDir           string
	prowgenImage             string
	checkconfigImage         string
	namespace                string
}

func gatherOptions() options {
	o := options{}
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)

	fs.StringVar(&o.logLevel, "log-level", "info", "Level at which to log output.")
	fs.StringVar(&o.webhookSecretFile, "hmac-secret-file", "/etc/webhook/hmac", "Path to the file containing the GitHub HMAC secret.")
	fs.StringVar(&o.jobConfigDir, "job-config-dir", "", "Path to the EFS-mounted job config directory.")
	fs.StringVar(&o.releaseRepoDir, "release-repo-dir", "", "Path to the git-sync'd openshift/release repository directory.")
	fs.StringVar(&o.prowgenImage, "prowgen-image", "", "Container image for ci-operator-prowgen used in the bootstrap postsubmit.")
	fs.StringVar(&o.checkconfigImage, "checkconfig-image", "", "Container image for ci-operator-checkconfig used in the bootstrap presubmit.")
	fs.StringVar(&o.namespace, "namespace", "ci", "Namespace where ProwJobs will be created.")

	o.github.AddFlags(fs)
	o.githubEventServerOptions.Bind(fs)

	if err := fs.Parse(os.Args[1:]); err != nil {
		logrus.WithError(err).Fatalf("cannot parse args: '%s'", os.Args[1:])
	}
	return o
}

func (o *options) Validate() error {
	if _, err := logrus.ParseLevel(o.logLevel); err != nil {
		return fmt.Errorf("invalid --log-level: %w", err)
	}
	if o.jobConfigDir == "" {
		return fmt.Errorf("--job-config-dir must be set")
	}
	if o.releaseRepoDir == "" {
		return fmt.Errorf("--release-repo-dir must be set")
	}
	if o.prowgenImage == "" {
		return fmt.Errorf("--prowgen-image must be set")
	}
	if o.checkconfigImage == "" {
		return fmt.Errorf("--checkconfig-image must be set")
	}
	return o.githubEventServerOptions.DefaultAndValidate()
}

func main() {
	logrusutil.ComponentInit()
	logger := logrus.WithField("plugin", pluginName)

	o := gatherOptions()
	if err := o.Validate(); err != nil {
		logger.Fatalf("Invalid options: %v", err)
	}

	level, _ := logrus.ParseLevel(o.logLevel)
	logrus.SetLevel(level)

	var tokens []string
	if o.github.TokenPath != "" {
		tokens = append(tokens, o.github.TokenPath)
	}
	if o.github.AppPrivateKeyPath != "" {
		tokens = append(tokens, o.github.AppPrivateKeyPath)
	}
	tokens = append(tokens, o.webhookSecretFile)

	if err := secret.Add(tokens...); err != nil {
		logger.WithError(err).Fatal("Error starting secrets agent.")
	}

	getWebhookHMAC := secret.GetTokenGenerator(o.webhookSecretFile)

	githubClient, err := o.github.GitHubClient(false)
	if err != nil {
		logger.WithError(err).Fatal("Error getting GitHub client.")
	}

	clusterConfig, err := rest.InClusterConfig()
	if err != nil {
		logger.WithError(err).Fatal("Error getting in-cluster config.")
	}
	scheme := runtime.NewScheme()
	if err := pjapi.AddToScheme(scheme); err != nil {
		logger.WithError(err).Fatal("Error adding ProwJob scheme.")
	}
	pjclient, err := ctrlruntimeclient.New(clusterConfig, ctrlruntimeclient.Options{Scheme: scheme})
	if err != nil {
		logger.WithError(err).Fatal("Error creating ProwJob client.")
	}

	serv := &server{
		ghc:              githubClient,
		trustedChecker:   &githubTrustedChecker{githubClient: githubClient},
		pjclient:         pjclient,
		namespace:        o.namespace,
		jobConfigDir:     o.jobConfigDir,
		releaseRepoDir:   o.releaseRepoDir,
		prowgenImage:     o.prowgenImage,
		checkconfigImage: o.checkconfigImage,
	}

	eventServer := githubeventserver.New(o.githubEventServerOptions, getWebhookHMAC, logger)
	eventServer.RegisterHandleIssueCommentEvent(serv.handleIssueComment)
	eventServer.RegisterHandlePullRequestEvent(serv.handlePullRequest)
	eventServer.RegisterHelpProvider(helpProvider, logger)

	interrupts.OnInterrupt(func() {
		eventServer.GracefulShutdown()
	})

	health := pjutil.NewHealth()
	health.ServeReady()

	interrupts.ListenAndServe(eventServer, time.Second*30)
	interrupts.WaitForGracefulShutdown()
}
