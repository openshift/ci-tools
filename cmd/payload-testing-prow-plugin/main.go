package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/sirupsen/logrus"
	"gopkg.in/fsnotify.v1"

	"k8s.io/client-go/kubernetes/scheme"
	controllerruntime "sigs.k8s.io/controller-runtime"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/prow/pkg/config/secret"
	prowflagutil "sigs.k8s.io/prow/pkg/flagutil"
	"sigs.k8s.io/prow/pkg/githubeventserver"
	"sigs.k8s.io/prow/pkg/interrupts"
	"sigs.k8s.io/prow/pkg/kube"
	"sigs.k8s.io/prow/pkg/logrusutil"
	"sigs.k8s.io/prow/pkg/pjutil"

	"github.com/openshift/ci-tools/pkg/api"
	prpqv1 "github.com/openshift/ci-tools/pkg/api/pullrequestpayloadqualification/v1"
	"github.com/openshift/ci-tools/pkg/load/agents"
	registryserver "github.com/openshift/ci-tools/pkg/registry/server"
)

type options struct {
	logLevel                 string
	githubEventServerOptions githubeventserver.Options
	github                   prowflagutil.GitHubOptions
	kubernetesOptions        prowflagutil.KubernetesOptions
	trustedApps              prowflagutil.Strings
	namespace                string
	ciOpConfigDir            string
	releaseRepoGitSyncPath   string
	webhookSecretFile        string
}

func gatherOptions() options {
	o := options{}
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)

	fs.StringVar(&o.logLevel, "log-level", "info", "Level at which to log output.")
	fs.StringVar(&o.webhookSecretFile, "hmac-secret-file", "/etc/webhook/hmac", "Path to the file containing the GitHub HMAC secret.")

	o.github.AddFlags(fs)
	o.githubEventServerOptions.Bind(fs)
	o.kubernetesOptions.AddFlags(fs)
	fs.StringVar(&o.namespace, "namespace", "ci", "Namespace to create PullRequestPayloadQualificationRuns.")
	fs.StringVar(&o.ciOpConfigDir, "ci-op-config-dir", "", "Path to CI Operator configuration directory.")
	fs.StringVar(&o.releaseRepoGitSyncPath, "release-repo-git-sync-path", "/var/repo/release", "Path to release repository dir")
	fs.Var(&o.trustedApps, "trusted-app", "Repeatable. GitHub App slug allowed to issue /payload . Example: --trusted-app=openshift-pr-manager")
	if err := fs.Parse(os.Args[1:]); err != nil {
		logrus.WithError(err).Fatalf("cannot parse args: '%s'", os.Args[1:])
	}
	return o
}

func (o *options) Validate() error {
	_, err := logrus.ParseLevel(o.logLevel)
	if err != nil {
		return fmt.Errorf("invalid --log-level: %w", err)
	}
	if o.ciOpConfigDir == "" {
		return fmt.Errorf("--ci-op-config-dir must be set")
	}
	if err := o.kubernetesOptions.Validate(false); err != nil {
		return err
	}
	return o.githubEventServerOptions.DefaultAndValidate()
}

const (
	appCIContextName = string(api.ClusterAPPCI)
)

func addSchemes() error {
	if err := prpqv1.AddToScheme(scheme.Scheme); err != nil {
		return fmt.Errorf("failed to add prpqv1 to scheme: %w", err)
	}
	return nil
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
	if err := addSchemes(); err != nil {
		logger.WithError(err).Fatal("failed to set up scheme")
	}

	getWebhookHMAC := secret.GetTokenGenerator(o.webhookSecretFile)

	githubClient, err := o.github.GitHubClient(false)
	if err != nil {
		logger.WithError(err).Fatal("Error getting GitHub client.")
	}

	kubeconfigChangedCallBack := func() {
		logger.Info("Kubeconfig changed, exiting to get restarted by Kubelet and pick up the changes")
		interrupts.Terminate()
	}
	kubeconfigs, err := o.kubernetesOptions.LoadClusterConfigs(kubeconfigChangedCallBack)
	if err != nil {
		logger.WithError(err).Fatal("failed to load kubeconfigs")
	}

	inClusterConfig, hasInClusterConfig := kubeconfigs[kube.InClusterContext]
	delete(kubeconfigs, kube.InClusterContext)
	delete(kubeconfigs, kube.DefaultClusterAlias)

	if _, hasAppCi := kubeconfigs[appCIContextName]; !hasAppCi {
		if !hasInClusterConfig {
			logger.WithError(err).Fatalf("had no context for '%s' and loading InClusterConfig failed", appCIContextName)
		}
		logger.Infof("use InClusterConfig for %s", appCIContextName)
		kubeconfigs[appCIContextName] = inClusterConfig
	}

	kubeConfig := kubeconfigs[appCIContextName]
	kubeClient, err := ctrlruntimeclient.New(&kubeConfig, ctrlruntimeclient.Options{})
	if err != nil {
		logger.WithError(err).WithField("context", appCIContextName).Fatal("could not get client for kube config")
	}

	eventCh := make(chan fsnotify.Event)
	errCh := make(chan error)
	universalSymlinkWatcher := &agents.UniversalSymlinkWatcher{
		EventCh:   eventCh,
		ErrCh:     errCh,
		WatchPath: o.releaseRepoGitSyncPath,
	}

	configAgentOption := func(opt *agents.ConfigAgentOptions) {
		opt.UniversalSymlinkWatcher = universalSymlinkWatcher
	}
	watcher, err := universalSymlinkWatcher.GetWatcher()
	if err != nil {
		logger.Fatalf("Failed to get the universal symlink watcher: %v", err)
	}
	interrupts.Run(watcher)

	configErrCh := make(chan error)
	configAgent, err := agents.NewConfigAgent(o.ciOpConfigDir, configErrCh, configAgentOption)
	if err != nil {
		logger.WithError(err).Fatal("Failed to construct config agent")
	}
	go func() { logger.Fatal(<-configErrCh) }()

	serv := &server{
		ghc:          githubClient,
		kubeClient:   kubeClient,
		ctx:          controllerruntime.SetupSignalHandler(),
		namespace:    o.namespace,
		jobResolver:  newReleaseControllerJobResolver(&http.Client{}),
		testResolver: &fileTestResolver{configAgent: configAgent},
		trustedChecker: &githubTrustedChecker{
			githubClient: githubClient,
			trustedApps:  o.trustedApps,
		},
		ciOpConfigResolver: registryserver.NewResolverClient(api.URLForService(api.ServiceConfig)),
	}

	eventServer := githubeventserver.New(o.githubEventServerOptions, getWebhookHMAC, logger)
	eventServer.RegisterHandleIssueCommentEvent(serv.handleIssueComment)
	eventServer.RegisterHelpProvider(helpProvider, logger)

	interrupts.OnInterrupt(func() {
		eventServer.GracefulShutdown()
	})

	health := pjutil.NewHealth()
	health.ServeReady()

	interrupts.ListenAndServe(eventServer, time.Second*30)
	interrupts.WaitForGracefulShutdown()
}
