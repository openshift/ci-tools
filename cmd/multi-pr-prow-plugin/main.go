package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/sirupsen/logrus"

	controllerruntime "sigs.k8s.io/controller-runtime"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/prow/pkg/config/secret"
	prowflagutil "sigs.k8s.io/prow/pkg/flagutil"
	prowconfigflagutil "sigs.k8s.io/prow/pkg/flagutil/config"
	"sigs.k8s.io/prow/pkg/githubeventserver"
	"sigs.k8s.io/prow/pkg/interrupts"
	"sigs.k8s.io/prow/pkg/kube"
	"sigs.k8s.io/prow/pkg/logrusutil"
	"sigs.k8s.io/prow/pkg/pjutil"

	"github.com/openshift/ci-tools/pkg/api"
)

const (
	pluginName       = "multi-pr-testing"
	appCIContextName = string(api.ClusterAPPCI)
)

type options struct {
	prowconfigflagutil.ConfigOptions
	githubEventServerOptions githubeventserver.Options
	github                   prowflagutil.GitHubOptions
	kubernetesOptions        prowflagutil.KubernetesOptions

	logLevel          string
	namespace         string
	ciOpConfigDir     string
	webhookSecretFile string
	dispatcherAddress string

	jobConfigFile        string
	jobReportSyncSeconds int
}

func gatherOptions() options {
	o := options{}
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)

	fs.StringVar(&o.logLevel, "log-level", "info", "Level at which to log output.")
	fs.StringVar(&o.webhookSecretFile, "hmac-secret-file", "/etc/webhook/hmac", "Path to the file containing the GitHub HMAC secret.")

	o.ConfigOptions.AddFlags(fs)
	o.github.AddFlags(fs)
	o.githubEventServerOptions.Bind(fs)
	o.kubernetesOptions.AddFlags(fs)
	fs.StringVar(&o.namespace, "namespace", "ci", "Namespace to create PullRequestPayloadQualificationRuns.")
	fs.StringVar(&o.ciOpConfigDir, "ci-op-config-dir", "", "Path to CI Operator configuration directory.")
	fs.StringVar(&o.dispatcherAddress, "dispatcher-address", "http://prowjob-dispatcher.ci.svc.cluster.local:8080", "Address of prowjob-dispatcher server.")
	fs.StringVar(&o.jobConfigFile, "job-config", "", "path of job-config file.")
	fs.IntVar(&o.jobReportSyncSeconds, "job-sync-seconds", 60, "Interval seconds between job report sync cycles.")
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
	if err := o.ConfigOptions.Validate(false); err != nil {
		return err
	}
	if err := o.kubernetesOptions.Validate(false); err != nil {
		return err
	}

	if o.jobConfigFile == "" {
		return fmt.Errorf("--job-config must be set")
	}

	if o.jobReportSyncSeconds < 0 {
		return fmt.Errorf("--job-sync-seconds must be >= 0")
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

	kubeClient, err := o.getKubeClient(logger)
	if err != nil {
		logger.WithError(err).Fatal("Error getting kube client.")
	}

	agent, err := o.ConfigOptions.ConfigAgent()
	if err != nil {
		logrus.WithError(err).Fatal("could not load Prow configuration")
	}

	rep := newReporter(githubClient, kubeClient, o.namespace, o.jobConfigFile)
	ticker := time.NewTicker(time.Duration(o.jobReportSyncSeconds) * time.Second)
	quit := make(chan struct{})
	go func() {
		for {
			select {
			case <-ticker.C:
				if syncErr := rep.sync(logger); syncErr != nil {
					logger.WithError(syncErr).Error("sync loop failed to sync some jobs")
				}
			case <-quit:
				ticker.Stop()
				return
			}
		}
	}()

	serv := newServer(controllerruntime.SetupSignalHandler(), githubClient, kubeClient, o.namespace, agent, o.dispatcherAddress, &rep)

	eventServer := githubeventserver.New(o.githubEventServerOptions, getWebhookHMAC, logger)
	eventServer.RegisterHandleIssueCommentEvent(serv.handleIssueComment)
	eventServer.RegisterHelpProvider(helpProvider, logger)

	interrupts.OnInterrupt(func() {
		eventServer.GracefulShutdown()
	})

	health := pjutil.NewHealth()
	health.ServeReady()
	logrus.Infof("ready to serve")

	interrupts.ListenAndServe(eventServer, time.Second*30)
	interrupts.WaitForGracefulShutdown()
}

func (o *options) getKubeClient(logger *logrus.Entry) (ctrlruntimeclient.Client, error) {
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
		return nil, err
	}

	return kubeClient, nil
}
