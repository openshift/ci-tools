package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"cloud.google.com/go/storage"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/sirupsen/logrus"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"google.golang.org/api/option"

	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/kubernetes/scheme"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/prow/pkg/config"
	"sigs.k8s.io/prow/pkg/config/secret"
	"sigs.k8s.io/prow/pkg/flagutil"
	prowflagutil "sigs.k8s.io/prow/pkg/flagutil"
	configflagutil "sigs.k8s.io/prow/pkg/flagutil/config"
	"sigs.k8s.io/prow/pkg/interrupts"
	"sigs.k8s.io/prow/pkg/logrusutil"
	"sigs.k8s.io/prow/pkg/metrics"
	"sigs.k8s.io/prow/pkg/pjutil"
	"sigs.k8s.io/prow/pkg/pjutil/pprof"
	"sigs.k8s.io/prow/pkg/simplifypath"

	userv1 "github.com/openshift/api/user/v1"

	"github.com/openshift/ci-tools/pkg/dispatcher"
	"github.com/openshift/ci-tools/pkg/jira"
	"github.com/openshift/ci-tools/pkg/pagerdutyutil"
	alertproxyforward "github.com/openshift/ci-tools/pkg/slack/alertproxy"
	dispatchcommand "github.com/openshift/ci-tools/pkg/slack/dispatcher"
	eventhandler "github.com/openshift/ci-tools/pkg/slack/events"
	"github.com/openshift/ci-tools/pkg/slack/events/helpdesk"
	eventrouter "github.com/openshift/ci-tools/pkg/slack/events/router"
	interactionhandler "github.com/openshift/ci-tools/pkg/slack/interactions"
	interactionrouter "github.com/openshift/ci-tools/pkg/slack/interactions/router"
	"github.com/openshift/ci-tools/pkg/util"
)

type options struct {
	port int

	logLevel               string
	gracePeriod            time.Duration
	instrumentationOptions prowflagutil.InstrumentationOptions
	jiraOptions            prowflagutil.JiraOptions
	pagerDutyOptions       pagerdutyutil.Options

	prowconfig configflagutil.ConfigOptions

	slackTokenPath         string
	slackSigningSecretPath string

	keywordsConfigPath      string
	helpdeskAlias           string
	forumChannelId          string
	reviewRequestWorkflowID string
	namespace               string
	requireWorkflowsInForum bool
	supportRequestChannelID string
	supportRequestThreshold int

	dispatcherControlURL          string
	dispatcherControlTokenPath    string
	dispatchCommandChannelID      string
	enableDispatchCapacity        bool
	enableDispatchDrain           bool
	enableDispatchCapabilityScope bool

	alertProxyInteractionURL      string
	alertProxyMentionURL          string
	alertProxyForwarderSecretPath string
	alertProxyChannelID           string
}

func validateDispatcherControlURL(value string) error {
	parsed, err := url.ParseRequestURI(value)
	if err != nil || !parsed.IsAbs() || parsed.Host == "" {
		return fmt.Errorf("must be an absolute URL: %q", value)
	}
	switch strings.ToLower(parsed.Scheme) {
	case "https":
		return nil
	case "http":
		host := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
		labels := strings.Split(host, ".")
		inClusterService := len(labels) >= 3 && strings.HasSuffix(host, ".svc") ||
			len(labels) >= 5 && strings.HasSuffix(host, ".svc.cluster.local")
		if inClusterService {
			return nil
		}
		return fmt.Errorf("HTTP is allowed only for Kubernetes service DNS names: %q", value)
	default:
		return fmt.Errorf("scheme must be HTTPS, or HTTP for an in-cluster Kubernetes service: %q", value)
	}
}

const (
	alertProxyServiceHost               = "alert-proxy.ci.svc"
	alertProxyServiceFullyQualifiedHost = "alert-proxy.ci.svc.cluster.local"
	alertProxyServicePort               = "8080"
)

func validateAlertProxyEndpointURL(value, expectedPath string) error {
	parsed, err := url.ParseRequestURI(value)
	if err != nil || !parsed.IsAbs() || parsed.Host == "" {
		return fmt.Errorf("must be an absolute URL: %q", value)
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("must not contain user information, a query, or a fragment: %q", value)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("scheme must be HTTP or HTTPS: %q", value)
	}
	host := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
	if host != alertProxyServiceHost && host != alertProxyServiceFullyQualifiedHost {
		return fmt.Errorf("must target the alert-proxy service in namespace ci: %q", value)
	}
	if parsed.Port() != alertProxyServicePort {
		return fmt.Errorf("must target alert-proxy service port %s: %q", alertProxyServicePort, value)
	}
	if parsed.EscapedPath() != expectedPath {
		return fmt.Errorf("path must be %s: %q", expectedPath, value)
	}
	return nil
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
	if o.supportRequestChannelID != "" && o.supportRequestThreshold < 1 {
		return fmt.Errorf("--support-request-threshold must be >= 1")
	}
	if o.dispatcherControlURL != "" {
		if err := validateDispatcherControlURL(o.dispatcherControlURL); err != nil {
			return fmt.Errorf("invalid --dispatcher-control-url: %w", err)
		}
		if o.dispatcherControlTokenPath == "" {
			return fmt.Errorf("--dispatcher-control-token-path is required when dispatcher mentions are configured")
		}
		if o.dispatchCommandChannelID == "" {
			return fmt.Errorf("--dispatch-command-channel-id is required when dispatcher mentions are configured")
		}
	} else if o.dispatcherControlTokenPath != "" || o.dispatchCommandChannelID != "" || o.enableDispatchCapacity || o.enableDispatchDrain || o.enableDispatchCapabilityScope {
		return fmt.Errorf("--dispatcher-control-url is required when any dispatcher mention option is set")
	}
	if o.enableDispatchDrain && !o.enableDispatchCapacity {
		return fmt.Errorf("--enable-dispatch-drain requires --enable-dispatch-capacity")
	}
	if o.enableDispatchCapabilityScope && !o.enableDispatchCapacity {
		return fmt.Errorf("--enable-dispatch-capability-scope requires --enable-dispatch-capacity")
	}

	hasAlertProxyEndpoint := o.alertProxyInteractionURL != "" || o.alertProxyMentionURL != ""
	if o.alertProxyInteractionURL != "" {
		if err := validateAlertProxyEndpointURL(o.alertProxyInteractionURL, "/interactions"); err != nil {
			return fmt.Errorf("invalid --alert-proxy-interaction-url: %w", err)
		}
	}
	if o.alertProxyMentionURL != "" {
		if err := validateAlertProxyEndpointURL(o.alertProxyMentionURL, "/mentions"); err != nil {
			return fmt.Errorf("invalid --alert-proxy-mention-url: %w", err)
		}
		if o.alertProxyChannelID == "" {
			return fmt.Errorf("--alert-proxy-channel-id is required when alert-proxy mentions are configured")
		}
	}
	if hasAlertProxyEndpoint && o.alertProxyForwarderSecretPath == "" {
		return fmt.Errorf("--alert-proxy-forwarder-secret-path is required when an alert-proxy endpoint is configured")
	}
	if !hasAlertProxyEndpoint && o.alertProxyForwarderSecretPath != "" {
		return fmt.Errorf("an alert-proxy endpoint URL is required when --alert-proxy-forwarder-secret-path is set")
	}

	for _, group := range []flagutil.OptionGroup{&o.instrumentationOptions, &o.jiraOptions, &o.pagerDutyOptions, &o.prowconfig} {
		if err := group.Validate(false); err != nil {
			return err
		}
	}

	return nil
}

func gatherOptions(fs *flag.FlagSet, args ...string) options {
	var o options
	fs.IntVar(&o.port, "port", 8888, "Port to listen on.")

	fs.StringVar(&o.logLevel, "log-level", "info", "Level at which to log output.")
	fs.DurationVar(&o.gracePeriod, "grace-period", 180*time.Second, "On shutdown, try to handle remaining events for the specified duration. ")

	o.prowconfig.ConfigPathFlagName = "prow-config-path"
	o.prowconfig.JobConfigPathFlagName = "prow-job-config-path"
	for _, group := range []flagutil.OptionGroup{&o.instrumentationOptions, &o.jiraOptions, &o.pagerDutyOptions, &o.prowconfig} {
		group.AddFlags(fs)
	}

	fs.StringVar(&o.slackTokenPath, "slack-token-path", "", "Path to the file containing the Slack token to use.")
	fs.StringVar(&o.slackSigningSecretPath, "slack-signing-secret-path", "", "Path to the file containing the Slack signing secret to use.")
	fs.StringVar(&o.keywordsConfigPath, "keywords-config-path", "", "Path to the slack-bot keywords config file.")
	fs.StringVar(&o.helpdeskAlias, "helpdesk-alias", "@dptp-helpdesk", "Alias for helpdesk user(s) beginning with '@'")
	fs.StringVar(&o.forumChannelId, "forum-channel-id", "CBN38N3MW", "Channel ID for #forum-ocp-testplatform")
	fs.StringVar(&o.reviewRequestWorkflowID, "review-request-workflow-id", "B06T46F374N", "ID for the 'Review Request' slack workflow")
	fs.StringVar(&o.namespace, "namespace", "ci", "Namespace to store helpdesk-faq items")
	fs.BoolVar(&o.requireWorkflowsInForum, "require-workflows-in-forum", true, "Require the use of workflows in the designated forum channel")
	fs.StringVar(&o.supportRequestChannelID, "support-request-channel-id", "CBN38N3MW", "Channel ID where support request mode watches long threads (defaults to #forum-ocp-testplatform)")
	fs.IntVar(&o.supportRequestThreshold, "support-request-threshold", 12, "Create a support-request Jira when a thread has more than this many messages (total count includes the root message)")
	fs.StringVar(&o.dispatcherControlURL, "dispatcher-control-url", "", "Base URL of the authenticated dispatcher control API. Empty disables @dptp-bot tp-dispatch mentions.")
	fs.StringVar(&o.dispatcherControlTokenPath, "dispatcher-control-token-path", "", "Path to the bearer token used only for the dispatcher control API.")
	fs.StringVar(&o.dispatchCommandChannelID, "dispatch-command-channel-id", "", "Immutable Slack channel ID for #team-dp-testplatform. Required when @dptp-bot tp-dispatch is enabled.")
	fs.BoolVar(&o.enableDispatchCapacity, "enable-dispatch-capacity", false, "Enable applying and cancelling whole-cluster capacity overrides after shadow validation.")
	fs.BoolVar(&o.enableDispatchDrain, "enable-dispatch-drain", false, "Enable whole-cluster drains after capacity operations meet their SLO.")
	fs.BoolVar(&o.enableDispatchCapabilityScope, "enable-dispatch-capability-scope", false, "Enable capability-scoped controls after metadata coverage is audited.")
	fs.StringVar(&o.alertProxyInteractionURL, "alert-proxy-interaction-url", "", "Full in-cluster URL for forwarding alert-proxy Slack interactions. Empty disables forwarding.")
	fs.StringVar(&o.alertProxyMentionURL, "alert-proxy-mention-url", "", "Full in-cluster URL for forwarding @dptp-bot ci-alerts mentions. Empty disables forwarding.")
	fs.StringVar(&o.alertProxyForwarderSecretPath, "alert-proxy-forwarder-secret-path", "", "Path to the HMAC secret used only for alert-proxy forwarding.")
	fs.StringVar(&o.alertProxyChannelID, "alert-proxy-channel-id", "CHY2E1BL4", "Immutable Slack channel ID for #ops-testplatform.")

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
	promMetrics            = metrics.NewMetrics("slack_bot")
	dispatchCommandDenials = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "slack_bot_dispatch_command_denials_total",
		Help: "Number of tp-dispatch mentions rejected at the Slack channel boundary.",
	})
	alertProxyForwards = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "slack_bot_interaction_forwards_total",
		Help: "Number of alert-proxy Slack callbacks forwarded by kind and result.",
	}, []string{"kind", "result"})
)

func init() {
	prometheus.MustRegister(dispatchCommandDenials)
	prometheus.MustRegister(alertProxyForwards)
}

func addSchemes() error {
	if err := userv1.AddToScheme(scheme.Scheme); err != nil {
		return fmt.Errorf("failed to add userv1 to scheme: %w", err)
	}
	return nil
}

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

	inClusterConfig, err := util.LoadClusterConfig()
	if err != nil {
		logrus.WithError(err).Fatal("Failed to load in-cluster config")
	}
	kubeClient, err := ctrlruntimeclient.New(inClusterConfig, ctrlruntimeclient.Options{})
	if err != nil {
		logrus.WithError(err).Fatal("Failed to create client")
	}
	if err = addSchemes(); err != nil {
		logrus.WithError(err).Fatal("couldn't add schemes")
	}

	secretPaths := []string{o.slackTokenPath, o.slackSigningSecretPath}
	if o.dispatcherControlTokenPath != "" {
		secretPaths = append(secretPaths, o.dispatcherControlTokenPath)
	}
	if o.alertProxyForwarderSecretPath != "" {
		secretPaths = append(secretPaths, o.alertProxyForwarderSecretPath)
	}
	if err := secret.Add(secretPaths...); err != nil {
		logrus.WithError(err).Fatal("Error starting secrets agent.")
	}

	jiraClient, err := o.jiraOptions.Client()
	if err != nil {
		logrus.WithError(err).Fatal("Could not initialize Jira client.")
	}
	pagerDutyClient, err := o.pagerDutyOptions.Client()
	if err != nil {
		logrus.WithError(err).Fatal("Could not initialize PagerDuty client.")
	}

	slackClient := slack.New(string(secret.GetSecret(o.slackTokenPath)))
	issueFiler, err := jira.NewIssueFiler(slackClient, jiraClient.JiraClient(), pagerDutyClient)
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

	eventRoutes := eventrouter.ForEvents(slackClient, issueFiler, kubeClient, configAgent.Config, gcsClient, keywordsConfig, o.helpdeskAlias, o.forumChannelId, o.reviewRequestWorkflowID, o.namespace, o.supportRequestChannelID, o.supportRequestThreshold, o.requireWorkflowsInForum)
	var dispatchMentionHandler eventhandler.PartialHandler
	if o.dispatcherControlURL != "" {
		controlClient := dispatcher.NewControlClient(o.dispatcherControlURL, secret.GetTokenGenerator(o.dispatcherControlTokenPath))
		dispatchHandler, err := dispatchcommand.NewHandler(controlClient, dispatchcommand.Options{
			ChannelID:             o.dispatchCommandChannelID,
			EnableApply:           o.enableDispatchCapacity || o.enableDispatchDrain || o.enableDispatchCapabilityScope,
			EnableCapacity:        o.enableDispatchCapacity,
			EnableDrain:           o.enableDispatchDrain,
			EnableCapabilityScope: o.enableDispatchCapabilityScope,
			Messenger:             slackClient,
			OnDenial: func(command dispatchcommand.Command) {
				dispatchCommandDenials.Inc()
				logrus.WithFields(logrus.Fields{"channel_id": command.ChannelID, "user_id": command.UserID, "route": "tp-dispatch"}).Warn("denied dispatcher Slack mention outside the configured channel")
			},
		})
		if err != nil {
			logrus.WithError(err).Fatal("Failed to configure dispatcher Slack mentions")
		}
		go dispatchHandler.Run(interrupts.Context())
		dispatchMentionHandler = dispatchHandler.MentionHandler()
	}

	var alertProxyRelay *alertproxyforward.Relay
	var alertProxyMentionHandler eventhandler.PartialHandler
	if o.alertProxyForwarderSecretPath != "" {
		alertProxyRelay = alertproxyforward.NewRelay(secret.GetTokenGenerator(o.alertProxyForwarderSecretPath))
	}
	if o.alertProxyMentionURL != "" {
		alertProxyMentionHandler = alertProxyRelay.MentionHandler(o.alertProxyMentionURL, o.alertProxyChannelID, func(result string) {
			alertProxyForwards.WithLabelValues("mention", result).Inc()
		})
	}
	eventRoutes = composeEventRoutes(eventRoutes, alertProxyMentionHandler, dispatchMentionHandler)

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
	mux.Handle("/slack/interactive-endpoint", handler(handleInteraction(secret.GetTokenGenerator(o.slackSigningSecretPath), o.alertProxyInteractionURL, alertProxyRelay, slackClient, interactionrouter.ForModals(issueFiler, slackClient))))
	mux.Handle("/slack/events-endpoint", handler(handleEvent(secret.GetTokenGenerator(o.slackSigningSecretPath), eventRoutes)))
	server := &http.Server{Addr: ":" + strconv.Itoa(o.port), Handler: mux}

	health.ServeReady()

	logrus.Debug("Server ready.")
	interrupts.ListenAndServe(server, o.gracePeriod)
	interrupts.WaitForGracefulShutdown()
}

func loadConfig(configPath string, config interface{}) error {
	configContent, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("failed to read config: %w", err)
	}
	if err = yaml.Unmarshal(configContent, &config); err != nil {
		return fmt.Errorf("failed to unmarshall config: %w", err)
	}
	return nil
}

func loadKeywordsConfig(configPath string, config interface{}) error {
	return loadConfig(configPath, config)
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

// composeEventRoutes keeps the reserved ci-alerts route ahead of the optional
// dispatcher route and the generic mention handler inside base.
func composeEventRoutes(base eventhandler.Handler, alertProxy, dispatcher eventhandler.PartialHandler) eventhandler.Handler {
	var routes []eventhandler.PartialHandler
	if alertProxy != nil {
		routes = append(routes, alertProxy)
	}
	if dispatcher != nil {
		routes = append(routes, dispatcher)
	}
	if len(routes) == 0 {
		return base
	}
	routes = append(routes, eventhandler.PartialFromHandler(base))
	return eventhandler.MultiHandler(routes...)
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

type ephemeralMessenger interface {
	PostEphemeralContext(ctx context.Context, channelID, userID string, options ...slack.MsgOption) (string, error)
}

func handleInteraction(signingSecret func() []byte, proxyURL string, relay *alertproxyforward.Relay, messenger ephemeralMessenger, handler interactionhandler.Handler) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		logger := logrus.WithField("api", "interactionhandler")
		logger.Debug("Got an interaction payload.")
		body, ok := verifiedBody(logger, request, signingSecret)
		if !ok {
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

		if proxyURL != "" && alertproxyforward.Claims(&callback) {
			response, err := relay.RelayInteraction(request.Context(), proxyURL, request.Header, body)
			if err != nil || response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
				alertProxyForwards.WithLabelValues("interaction", "error").Inc()
				if err != nil {
					logger.WithError(err).Error("Failed to forward alert-proxy interaction")
				} else {
					logger.WithField("status", response.StatusCode).Error("Alert-proxy interaction endpoint returned an error")
				}
				writer.WriteHeader(http.StatusOK)
				postAlertProxyUnavailable(messenger, &callback, logger)
				return
			}
			alertProxyForwards.WithLabelValues("interaction", "success").Inc()
			if contentType := response.Header.Get("Content-Type"); contentType != "" {
				writer.Header().Set("Content-Type", contentType)
			}
			writer.Header().Set("Content-Length", strconv.Itoa(len(response.Body)))
			writer.WriteHeader(response.StatusCode)
			if _, err := writer.Write(response.Body); err != nil {
				logger.WithError(err).Error("Failed to send alert-proxy interaction response")
			}
			return
		}
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

func postAlertProxyUnavailable(messenger ephemeralMessenger, callback *slack.InteractionCallback, logger *logrus.Entry) {
	if messenger == nil {
		return
	}
	channelID := callback.Channel.ID
	if channelID == "" {
		channelID = callback.Container.ChannelID
	}
	if channelID == "" || callback.User.ID == "" {
		logger.Warn("Cannot post alert-proxy unavailable response without a Slack channel and user")
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if _, err := messenger.PostEphemeralContext(
			ctx, channelID, callback.User.ID,
			slack.MsgOptionText("Alert actions are temporarily unavailable. Please try again.", false),
		); err != nil {
			logger.WithError(err).Warn("Failed to post alert-proxy unavailable response")
		}
	}()
}

func fieldsFor(interactionCallback *slack.InteractionCallback) logrus.Fields {
	return logrus.Fields{
		"trigger_id":  interactionCallback.TriggerID,
		"callback_id": interactionCallback.CallbackID,
		"action_id":   interactionCallback.ActionID,
		"type":        interactionCallback.Type,
	}
}
