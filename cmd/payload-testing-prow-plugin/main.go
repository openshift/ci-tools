package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/sirupsen/logrus"

	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/test-infra/prow/config/secret"
	prowflagutil "k8s.io/test-infra/prow/flagutil"
	"k8s.io/test-infra/prow/githubeventserver"
	"k8s.io/test-infra/prow/interrupts"
	"k8s.io/test-infra/prow/kube"
	"k8s.io/test-infra/prow/logrusutil"
	"k8s.io/test-infra/prow/pjutil"
	controllerruntime "sigs.k8s.io/controller-runtime"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openshift/ci-tools/pkg/api"
	prpqv1 "github.com/openshift/ci-tools/pkg/api/pullrequestpayloadqualification/v1"
)

type options struct {
	githubEventServerOptions githubeventserver.Options
	github                   prowflagutil.GitHubOptions
	kubernetesOptions        prowflagutil.KubernetesOptions
	namespace                string
	ciOpConfigDir            string
	webhookSecretFile        string
}

func gatherOptions() options {
	o := options{}
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)

	fs.StringVar(&o.webhookSecretFile, "hmac-secret-file", "/etc/webhook/hmac", "Path to the file containing the GitHub HMAC secret.")

	o.github.AddFlags(fs)
	o.githubEventServerOptions.Bind(fs)
	o.kubernetesOptions.AddFlags(fs)
	fs.StringVar(&o.namespace, "namespace", "ci", "Namespace to create PullRequestPayloadQualificationRuns.")
	fs.StringVar(&o.ciOpConfigDir, "ci-op-config-dir", "", "Path to CI Operator configuration directory.")
	if err := fs.Parse(os.Args[1:]); err != nil {
		logrus.WithError(err).Fatalf("cannot parse args: '%s'", os.Args[1:])
	}
	return o
}

func (o *options) Validate() error {
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
	logger := logrus.WithField("plugin", "payload-testing")

	o := gatherOptions()
	if err := o.Validate(); err != nil {
		logger.Fatalf("Invalid options: %v", err)
	}

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
		logrus.WithError(err).Fatal("failed to set up scheme")
	}

	getWebhookHMAC := secret.GetTokenGenerator(o.webhookSecretFile)

	githubClient, err := o.github.GitHubClient(false)
	if err != nil {
		logger.WithError(err).Fatal("Error getting GitHub client.")
	}

	kubeconfigChangedCallBack := func() {
		logrus.Info("Kubeconfig changed, exiting to get restarted by Kubelet and pick up the changes")
		interrupts.Terminate()
	}
	kubeconfigs, err := o.kubernetesOptions.LoadClusterConfigs(kubeconfigChangedCallBack)
	if err != nil {
		logrus.WithError(err).Fatal("failed to load kubeconfigs")
	}

	inClusterConfig, hasInClusterConfig := kubeconfigs[kube.InClusterContext]
	delete(kubeconfigs, kube.InClusterContext)
	delete(kubeconfigs, kube.DefaultClusterAlias)

	if _, hasAppCi := kubeconfigs[appCIContextName]; !hasAppCi {
		if !hasInClusterConfig {
			logrus.WithError(err).Fatalf("had no context for '%s' and loading InClusterConfig failed", appCIContextName)
		}
		logrus.Infof("use InClusterConfig for %s", appCIContextName)
		kubeconfigs[appCIContextName] = inClusterConfig
	}

	kubeConfig := kubeconfigs[appCIContextName]
	kubeClient, err := ctrlruntimeclient.New(&kubeConfig, ctrlruntimeclient.Options{})
	if err != nil {
		logrus.WithError(err).WithField("context", appCIContextName).Fatal("could not get client for kube config")
	}

	jobResolver, err := newReleaseControllerJobResolver()
	if err != nil {
		logrus.WithError(err).Fatal("could not create job resolver")
	}

	testResolver, err := newFileTestResolver(o.ciOpConfigDir)
	if err != nil {
		logrus.WithError(err).Fatal("could not create test resolver")
	}

	serv := &server{
		ghc:          githubClient,
		kubeClient:   kubeClient,
		ctx:          controllerruntime.SetupSignalHandler(),
		namespace:    o.namespace,
		jobResolver:  jobResolver,
		testResolver: testResolver,
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
