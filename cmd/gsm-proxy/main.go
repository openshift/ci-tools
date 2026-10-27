package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
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

	gsmproxy "github.com/openshift/ci-tools/pkg/gsm-proxy"
	gsmsecrets "github.com/openshift/ci-tools/pkg/gsm-secrets"
)

type options struct {
	listenAddr          string
	groupsOwnershipFile string
	gsmProjectID        string
	gsmCredentialsFile  string
	gracePeriod         time.Duration
	tlsCertFile         string
	tlsKeyFile          string
}

func gatherOptions(args []string) (*options, error) {
	o := &options{}
	fs := flag.NewFlagSet(os.Args[0], flag.ContinueOnError)
	fs.StringVar(&o.listenAddr, "listen-addr", "127.0.0.1:8080", "Address to listen on. Without TLS, only loopback IP addresses are allowed.")
	fs.StringVar(&o.groupsOwnershipFile, "groups-ownership-file", "", "Path to core-services/sync-rover-groups/_config.yaml.")
	fs.StringVar(&o.gsmProjectID, "gsm-project-id", "", "Google Cloud project ID containing the secrets.")
	fs.StringVar(&o.gsmCredentialsFile, "gsm-credentials-file", "", "Path to the proxy's Google service account credentials (JSON).")
	fs.DurationVar(&o.gracePeriod, "grace-period", 10*time.Second, "Grace period for server shutdown.")
	fs.StringVar(&o.tlsCertFile, "tls-cert-file", "", "Path to the TLS certificate PEM file. Requires --tls-key-file.")
	fs.StringVar(&o.tlsKeyFile, "tls-key-file", "", "Path to the TLS private key PEM file. Requires --tls-cert-file.")
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
	if (o.tlsCertFile == "") != (o.tlsKeyFile == "") {
		return errors.New("--tls-cert-file and --tls-key-file must be provided together")
	}
	if o.tlsCertFile == "" {
		host, _, err := net.SplitHostPort(o.listenAddr)
		if err != nil {
			return fmt.Errorf("invalid --listen-addr: %w", err)
		}
		if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
			return errors.New("non-loopback listeners require --tls-cert-file and --tls-key-file")
		}
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
	ctx, cancel := context.WithCancel(interrupts.Context())
	defer cancel()
	authorizer, err := gsmproxy.NewReloadingCollectionAuthorizer(ctx, o.groupsOwnershipFile, logrus.NewEntry(logrus.StandardLogger()))
	if err != nil {
		return fmt.Errorf("failed to initialize ownership configuration: %w", err)
	}

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
		return fmt.Errorf("failed to initialize GSM lister: %w", err)
	}
	defer func() {
		if err := lister.Close(); err != nil {
			logrus.WithError(err).Error("failed to close GSM client")
		}
	}()

	server := newServer(o, authorizer, lister)
	var listener interrupts.ListenAndServer = server
	if o.tlsCertFile != "" {
		tlsServer, err := newTLSServer(server, o.tlsCertFile, o.tlsKeyFile)
		if err != nil {
			return fmt.Errorf("failed to initialize HTTPS server: %w", err)
		}
		listener = tlsServer
	}
	logrus.WithFields(logrus.Fields{"listen-addr": o.listenAddr, "tls": o.tlsCertFile != ""}).Info("starting gsm-proxy")
	interrupts.ListenAndServe(listener, o.gracePeriod)
	interrupts.WaitForGracefulShutdown()
	return nil
}

func newServer(o *options, authorizer gsmproxy.Authorizer, lister gsmproxy.SecretLister) *http.Server {
	log := logrus.NewEntry(logrus.StandardLogger())
	handler := gsmproxy.Audit("list-secrets", log, gsmproxy.RequireCollectionAccess(
		gsmproxy.NewDexTokenVerifier(),
		authorizer,
		log,
		&gsmproxy.ListSecretsHandler{Lister: lister, Log: log},
	))
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
