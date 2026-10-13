package main

import (
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"cloud.google.com/go/auth/credentials"
	secretmanager "cloud.google.com/go/secretmanager/apiv1"
	"github.com/sirupsen/logrus"
	"google.golang.org/api/option"

	"sigs.k8s.io/prow/pkg/interrupts"
	"sigs.k8s.io/prow/pkg/logrusutil"
	"sigs.k8s.io/prow/pkg/version"

	"github.com/openshift/ci-tools/pkg/group"
	gsmproxy "github.com/openshift/ci-tools/pkg/gsm-proxy"
	gsmsecrets "github.com/openshift/ci-tools/pkg/gsm-secrets"
)

type options struct {
	listenAddr          string
	groupsOwnershipFile string
	gsmProjectID        string
	gsmCredentialsFile  string
	gracePeriod         time.Duration
}

func gatherOptions(args []string) (*options, error) {
	o := &options{}
	fs := flag.NewFlagSet(os.Args[0], flag.ContinueOnError)
	fs.StringVar(&o.listenAddr, "listen-addr", "127.0.0.1:8080", "Address to listen on. Non-loopback listeners require HTTPS ingress before accepting bearer tokens.")
	fs.StringVar(&o.groupsOwnershipFile, "groups-ownership-file", "", "Path to core-services/sync-rover-groups/_config.yaml.")
	fs.StringVar(&o.gsmProjectID, "gsm-project-id", "", "Google Cloud project ID containing the secrets.")
	fs.StringVar(&o.gsmCredentialsFile, "gsm-credentials-file", "", "Path to the proxy's Google service account credentials (JSON).")
	fs.DurationVar(&o.gracePeriod, "grace-period", 10*time.Second, "Grace period for server shutdown.")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	if fs.NArg() != 0 {
		return nil, errors.New("unexpected positional arguments")
	}
	return o, nil
}

func (o *options) validate() error {
	if o.groupsOwnershipFile == "" {
		return errors.New("--groups-ownership-file must not be empty")
	}
	if o.gsmProjectID == "" {
		return errors.New("--gsm-project-id must not be empty")
	}
	if o.gsmCredentialsFile == "" {
		return errors.New("--gsm-credentials-file must not be empty")
	}
	if o.listenAddr == "" {
		return errors.New("--listen-addr must not be empty")
	}
	if o.gracePeriod < 0 {
		return errors.New("--grace-period must not be negative")
	}
	return nil
}

func main() {
	version.Name = "gsm-proxy"
	logrusutil.ComponentInit()
	o, err := gatherOptions(os.Args[1:])
	if errors.Is(err, flag.ErrHelp) {
		return
	}
	if err != nil {
		logrus.WithError(err).Fatal("failed to gather options")
	}
	if err := o.validate(); err != nil {
		logrus.WithError(err).Fatal("invalid options")
	}

	if err := run(o); err != nil {
		logrus.WithError(err).Fatal("failed to start gsm-proxy")
	}
}

func run(o *options) error {
	config, err := group.LoadConfig(o.groupsOwnershipFile)
	if err != nil {
		return fmt.Errorf("failed to load rover groups config: %w", err)
	}
	logrus.WithField("groups", len(config.Groups)).Info("loaded rover group config")

	gcpCredentials, err := credentials.NewCredentialsFromFile(credentials.ServiceAccount, o.gsmCredentialsFile, &credentials.DetectOptions{
		Scopes: secretmanager.DefaultAuthScopes(),
		Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		// Credential parsing errors must not expose the contents of the key file.
		return errors.New("failed to load GSM service account credentials")
	}
	lister, err := gsmproxy.NewGSMSecretLister(interrupts.Context(), gsmsecrets.Config{ProjectIdString: o.gsmProjectID},
		option.WithAuthCredentials(gcpCredentials))
	if err != nil {
		return errors.New("failed to initialize GSM lister")
	}
	defer func() {
		if err := lister.Close(); err != nil {
			logrus.WithError(err).Error("failed to close GSM client")
		}
	}()

	server := newServer(o, config, lister)
	logrus.WithField("listen-addr", o.listenAddr).Info("starting gsm-proxy")
	interrupts.ListenAndServe(server, o.gracePeriod)
	interrupts.WaitForGracefulShutdown()
	return nil
}

func newServer(o *options, config *group.Config, lister gsmproxy.SecretLister) *http.Server {
	handler := &gsmproxy.ListSecretsHandler{
		Verifier:   gsmproxy.NewDexTokenVerifier(),
		Authorizer: gsmproxy.NewCollectionAuthorizer(config),
		Lister:     lister,
		Log:        logrus.NewEntry(logrus.StandardLogger()),
	}
	return &http.Server{
		Addr:              o.listenAddr,
		Handler:           gsmproxy.NewServeMux(handler),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       time.Minute,
		MaxHeaderBytes:    16 << 10,
	}
}
