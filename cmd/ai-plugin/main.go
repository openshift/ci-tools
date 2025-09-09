package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/sirupsen/logrus"

	"sigs.k8s.io/prow/pkg/config/secret"
	prowflagutil "sigs.k8s.io/prow/pkg/flagutil"
	"sigs.k8s.io/prow/pkg/githubeventserver"
	"sigs.k8s.io/prow/pkg/interrupts"
	"sigs.k8s.io/prow/pkg/logrusutil"
	"sigs.k8s.io/prow/pkg/pjutil"

	"github.com/openshift/ci-tools/cmd/ai-plugin/provider"
)

const (
	pluginName = "ai-plugin"
)

type options struct {
	webhookSecretFile        string
	githubEventServerOptions githubeventserver.Options
	github                   prowflagutil.GitHubOptions

	logLevel    string
	aiURL       string
	aiTokenFile string
	dryRun      bool
}

func gatherOptions() options {
	o := options{}
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	fs.StringVar(&o.logLevel, "log-level", "info", "Level at which to log output.")
	fs.BoolVar(&o.dryRun, "dry-run", true, "Dry run for testing.")
	fs.StringVar(&o.aiURL, "ai-url", "https://bedrock-runtime.us-west-2.amazonaws.com/model/openai.gpt-oss-20b-1:0/converse", "URL of the AWS Bedrock model to use.")
	fs.StringVar(&o.webhookSecretFile, "hmac-secret-file", "/etc/webhook/hmac", "Path to the file containing the GitHub HMAC secret.")
	fs.StringVar(&o.aiTokenFile, "ai-token-file", "/etc/ai-token", "Path to the file containing the AI token.")

	o.github.AddFlags(fs)
	o.githubEventServerOptions.Bind(fs)

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

	webhookTokenGenerator := secret.GetTokenGenerator(o.webhookSecretFile)

	aiTokenBytes, err := os.ReadFile(o.aiTokenFile)
	if err != nil {
		logrus.WithError(err).Fatal("failed to read AI token file")
	}

	githubClient, err := o.github.GitHubClient(o.dryRun)
	if err != nil {
		logger.WithError(err).Fatal("Error getting GitHub client.")
	}

	serv := &server{
		ghc:      githubClient,
		aiURL:    o.aiURL,
		aiToken:  string(aiTokenBytes),
		provider: provider.NewAWSBedrockProvider(),
		dry:      o.dryRun,
	}

	eventServer := githubeventserver.New(o.githubEventServerOptions, webhookTokenGenerator, logger)
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
