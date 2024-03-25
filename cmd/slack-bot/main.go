package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"time"

	"cloud.google.com/go/storage"
	"github.com/openshift/ci-tools/pkg/api"
	"github.com/sirupsen/logrus"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"google.golang.org/api/option"

	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/test-infra/pkg/flagutil"
	"k8s.io/test-infra/prow/config"
	"k8s.io/test-infra/prow/config/secret"
	prowflagutil "k8s.io/test-infra/prow/flagutil"
	configflagutil "k8s.io/test-infra/prow/flagutil/config"
	"k8s.io/test-infra/prow/interrupts"
	"k8s.io/test-infra/prow/logrusutil"
	"k8s.io/test-infra/prow/metrics"
	"k8s.io/test-infra/prow/pjutil"
	"k8s.io/test-infra/prow/pjutil/pprof"
	"k8s.io/test-infra/prow/simplifypath"

	"github.com/openshift/ci-tools/pkg/jira"
	eventhandler "github.com/openshift/ci-tools/pkg/slack/events"
	"github.com/openshift/ci-tools/pkg/slack/events/helpdesk"
	eventrouter "github.com/openshift/ci-tools/pkg/slack/events/router"
	interactionhandler "github.com/openshift/ci-tools/pkg/slack/interactions"
	interactionrouter "github.com/openshift/ci-tools/pkg/slack/interactions/router"
)

const (
	appCIContextName = string(api.ClusterAPPCI)
)

type options struct {
	port int

	logLevel               string
	gracePeriod            time.Duration
	instrumentationOptions prowflagutil.InstrumentationOptions
	jiraOptions            prowflagutil.JiraOptions
	kubernetesOptions      prowflagutil.KubernetesOptions

	prowconfig configflagutil.ConfigOptions

	slackTokenPath         string
	slackSigningSecretPath string

	keywordsConfigPath      string
	helpdeskAlias           string
	forumChannelId          string
	requireWorkflowsInForum bool
}

func (o *options) Validate() error {
	_, err := logrus.ParseLevel(o.logLevel)
	if err != nil {
		return fmt.Errorf("invalid --log-level: %w", err)
	}

	if o.slackTokenPath == "" {
		return fmt.Errorf("--slack-token-path is required")
	}

	if o.slackSigningSecretPath == "" {
		return fmt.Errorf("--slack-signing-secret-path is required")
	}

	for _, group := range []flagutil.OptionGroup{&o.instrumentationOptions, &o.jiraOptions, &o.prowconfig} {
		if err := group.Validate(false); err != nil {
			return err
		}
	}

	return o.kubernetesOptions.Validate(false)
}

func gatherOptions(fs *flag.FlagSet, args ...string) options {
	var o options
	fs.IntVar(&o.port, "port", 8888, "Port to listen on.")

	fs.StringVar(&o.logLevel, "log-level", "info", "Level at which to log output.")
	fs.DurationVar(&o.gracePeriod, "grace-period", 180*time.Second, "On shutdown, try to handle remaining events for the specified duration. ")

	o.prowconfig.ConfigPathFlagName = "prow-config-path"
	o.prowconfig.JobConfigPathFlagName = "prow-job-config-path"
	for _, group := range []flagutil.OptionGroup{&o.instrumentationOptions, &o.jiraOptions, &o.prowconfig} {
		group.AddFlags(fs)
	}

	fs.StringVar(&o.slackTokenPath, "slack-token-path", "", "Path to the file containing the Slack token to use.")
	fs.StringVar(&o.slackSigningSecretPath, "slack-signing-secret-path", "", "Path to the file containing the Slack signing secret to use.")
	fs.StringVar(&o.keywordsConfigPath, "keywords-config-path", "", "Path to the slack-bot keywords config file.")
	fs.StringVar(&o.helpdeskAlias, "helpdesk-alias", "@dptp-helpdesk", "Alias for helpdesk user(s) beginning with '@'")
	fs.StringVar(&o.forumChannelId, "forum-channel-id", "CBN38N3MW", "Channel ID for #forum-ocp-testplatform")
	fs.BoolVar(&o.requireWorkflowsInForum, "require-workflows-in-forum", true, "Require the use of workflows in the designated forum channel")

	o.kubernetesOptions.AddFlags(fs)

	if err := fs.Parse(args); err != nil {
		logrus.WithError(err).Fatal("Could not parse args.")
	}
	return o
}

// l and v keep the tree legible
func l(fragment string, children ...simplifypath.Node) simplifypath.Node {
	return simplifypath.L(fragment, children...)
}

var (
	promMetrics = metrics.NewMetrics("slack_bot")
)

func main() {
	logrusutil.ComponentInit()

	o := gatherOptions(flag.NewFlagSet(os.Args[0], flag.ExitOnError), os.Args[1:]...)
	if err := o.Validate(); err != nil {
		logrus.WithError(err).Fatal("Invalid options")
	}
	level, _ := logrus.ParseLevel(o.logLevel)
	logrus.SetLevel(level)

	configAgent, err := o.prowconfig.ConfigAgent()
	if err != nil {
		logrus.WithError(err).Fatal("Error starting Prow config agent.")
	}

	kubeClient, err := o.kubernetesOptions.ClusterClientForContext(appCIContextName, false)
	if err != nil {
		logrus.WithError(err).Fatal("could not load kube config")
	}

	if err := secret.Add(o.slackTokenPath, o.slackSigningSecretPath); err != nil {
		logrus.WithError(err).Fatal("Error starting secrets agent.")
	}

	jiraClient, err := o.jiraOptions.Client()
	if err != nil {
		logrus.WithError(err).Fatal("Could not initialize Jira client.")
	}

	slackClient := slack.New(string(secret.GetSecret(o.slackTokenPath)))
	issueFiler, err := jira.NewIssueFiler(slackClient, jiraClient.JiraClient())
	if err != nil {
		logrus.WithError(err).Fatal("Could not initialize Jira issue filer.")
	}

	gcsClient, err := storage.NewClient(interrupts.Context(), option.WithoutAuthentication())
	if err != nil {
		logrus.WithError(err).Fatal("Could not initialize GCS client.")
	}

	var keywordsConfig helpdesk.KeywordsConfig
	if o.keywordsConfigPath != "" {
		if err := loadKeywordsConfig(o.keywordsConfigPath, &keywordsConfig); err != nil {
			logrus.WithError(err).Warn("Could not load keywords config.")
		}
	}

	metrics.ExposeMetrics("slack-bot", config.PushGateway{}, o.instrumentationOptions.MetricsPort)
	simplifier := simplifypath.NewSimplifier(l("", // shadow element mimicing the root
		l(""), // for black-box health checks
		l("slack",
			l("interactive-endpoint"),
			l("events-endpoint"),
		),
	))
	handler := metrics.TraceHandler(simplifier, promMetrics.HTTPRequestDuration, promMetrics.HTTPResponseSize)
	pprof.Instrument(o.instrumentationOptions)

	health := pjutil.NewHealth()

	mux := http.NewServeMux()
	// handle the root to allow for a simple uptime probe
	mux.Handle("/", handler(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) { writer.WriteHeader(http.StatusOK) })))
	mux.Handle("/slack/interactive-endpoint", handler(handleInteraction(secret.GetTokenGenerator(o.slackSigningSecretPath), interactionrouter.ForModals(issueFiler, slackClient))))
	mux.Handle("/slack/events-endpoint", handler(handleEvent(secret.GetTokenGenerator(o.slackSigningSecretPath), eventrouter.ForEvents(slackClient, kubeClient, configAgent.Config, gcsClient, keywordsConfig, o.helpdeskAlias, o.forumChannelId, o.requireWorkflowsInForum))))
	server := &http.Server{Addr: ":" + strconv.Itoa(o.port), Handler: mux}

	health.ServeReady()

	logrus.Debug("Server ready.")
	interrupts.ListenAndServe(server, o.gracePeriod)
	interrupts.WaitForGracefulShutdown()
}

func loadKeywordsConfig(configPath string, config interface{}) error {
	configContent, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("failed to read config: %w", err)
	}
	if err = yaml.Unmarshal(configContent, &config); err != nil {
		return fmt.Errorf("failed to unmarshall config: %w", err)
	}
	return nil
}

func verifiedBody(logger *logrus.Entry, request *http.Request, signingSecret func() []byte) ([]byte, bool) {
	verifier, err := slack.NewSecretsVerifier(request.Header, string(signingSecret()))
	if err != nil {
		logger.WithError(err).Error("Failed to create a secrets verifier.")
		return nil, false
	}

	body, err := io.ReadAll(request.Body)
	if err != nil {
		logger.WithError(err).Error("Failed to read an event payload.")
		return nil, false
	}

	// need to use body again when unmarshalling
	request.Body = io.NopCloser(bytes.NewBuffer(body))

	if _, err := verifier.Write(body); err != nil {
		logger.WithError(err).Error("Failed to hash an event payload.")
		return nil, false
	}

	if err = verifier.Ensure(); err != nil {
		logger.WithError(err).Error("Failed to verify an event payload.")
		return nil, false
	}

	return body, true
}

func handleEvent(signingSecret func() []byte, handler eventhandler.Handler) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		logger := logrus.WithField("api", "events")
		logger.Debug("Got an event payload.")
		body, ok := verifiedBody(logger, request, signingSecret)
		if !ok {
			writer.WriteHeader(http.StatusInternalServerError)
			return
		}

		// we are using the newer, more robust signing secret verification so we do
		// not use the older, deprecated verification token when loading this event
		event, err := slackevents.ParseEvent(body, slackevents.OptionNoVerifyToken())
		if err != nil {
			logger.WithError(err).WithField("body", string(body)).Error("Failed to unmarshal an event payload.")
			writer.WriteHeader(http.StatusInternalServerError)
			return
		}
		logger.WithField("event", event).Trace("Read an event payload.")

		if event.Type == slackevents.URLVerification {
			var response *slackevents.ChallengeResponse
			err := json.Unmarshal(body, &response)
			if err != nil {
				writer.WriteHeader(http.StatusInternalServerError)
				return
			}
			writer.Header().Set("Content-Type", "text")
			if _, err := writer.Write([]byte(response.Challenge)); err != nil {
				logger.WithError(err).Warn("Failed to write response.")
			}
		}

		// we always want to respond with 200 immediately
		writer.WriteHeader(http.StatusOK)

		// we don't really care how long this takes
		go func() {
			if err := handler.Handle(&event, logger); err != nil {
				logger.WithError(err).Error("Failed to handle event")
			}
		}()
	}
}

func handleInteraction(signingSecret func() []byte, handler interactionhandler.Handler) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		logger := logrus.WithField("api", "interactionhandler")
		logger.Debug("Got an interaction payload.")
		if _, ok := verifiedBody(logger, request, signingSecret); !ok {
			writer.WriteHeader(http.StatusInternalServerError)
			return
		}

		var callback slack.InteractionCallback
		payload := request.FormValue("payload")
		if err := json.Unmarshal([]byte(payload), &callback); err != nil {
			logger.WithError(err).WithField("payload", payload).Error("Failed to unmarshal an interaction payload.")
			writer.WriteHeader(http.StatusInternalServerError)
			return
		}
		logger.WithField("interaction", callback).Trace("Read an interaction payload.")
		logger = logger.WithFields(fieldsFor(&callback))
		response, err := handler.Handle(&callback, logger)
		if err != nil {
			logger.WithError(err).Error("Failed to handle interaction payload.")
		}
		if len(response) == 0 {
			writer.WriteHeader(http.StatusOK)
			return
		}
		logger.WithField("body", string(response)).Trace("Sending interaction payload response.")
		writer.Header().Set("Content-Type", "application/json")
		writer.Header().Set("Content-Length", strconv.Itoa(len(response)))
		if _, err := writer.Write(response); err != nil {
			logger.WithError(err).Error("Failed to send interaction payload response.")
		}
	}
}

func fieldsFor(interactionCallback *slack.InteractionCallback) logrus.Fields {
	return logrus.Fields{
		"trigger_id":  interactionCallback.TriggerID,
		"callback_id": interactionCallback.CallbackID,
		"action_id":   interactionCallback.ActionID,
		"type":        interactionCallback.Type,
	}
}
