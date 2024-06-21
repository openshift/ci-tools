package main

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/go-retryablehttp"
	"github.com/hashicorp/vault/api"
	"github.com/hashicorp/vault/sdk/helper/consts"
	"github.com/sirupsen/logrus"

	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/prow/pkg/flagutil"
	"sigs.k8s.io/prow/pkg/logrusutil"
	"sigs.k8s.io/prow/pkg/version"

	"github.com/openshift/ci-tools/pkg/vaultclient"
)

type options struct {
	vaultAddr         string
	kvMountPath       string
	listenAddr        string
	tlsCertFile       string
	tlsKeyFile        string
	kubernetesOptions flagutil.KubernetesOptions
	vaultToken        string
	vaultRole         string
}

func gatherOptions() (*options, error) {
	o := &options{kubernetesOptions: flagutil.KubernetesOptions{NOInClusterConfigDefault: true}}
	fs := flag.CommandLine
	fs.StringVar(&o.vaultAddr, "vault-addr", "http://127.0.0.1:8300", "The address of the upstream vault")
	fs.StringVar(&o.kvMountPath, "kv-mount-path", "secret", "The location of the kv mount")
	fs.StringVar(&o.listenAddr, "listen-addr", "127.0.0.1:8400", "The address the proxy shall listen on")
	fs.StringVar(&o.tlsCertFile, "tls-cert-file", "", "Path to a tls cert file. If set, will server over tls. Requires --tls-key-file")
	fs.StringVar(&o.tlsKeyFile, "tls-key-file", "", "Path to a tls key file. If set, will server over tls. Requires --tls-cert-file")
	o.kubernetesOptions.AddFlags(fs)
	fs.StringVar(&o.vaultToken, "vault-token", "", "Vault token that will be used to detect conflicting secrets. Must have read access to the whole kv store. Mutually exclusive with --vault-token.")
	fs.StringVar(&o.vaultRole, "vault-role", "", "Vault role to use for detecting conflicting secrets. Must have access to the whole kv store. Mutually exclusive with --vault-token.")
	if err := fs.Parse(os.Args[1:]); err != nil {
		return nil, fmt.Errorf("failed to parse flags: %w", err)
	}
	if (o.tlsCertFile == "") != (o.tlsKeyFile == "") {
		return nil, errors.New("--tls-cert-file and --tls-key-file must be passed together")
	}
	if o.vaultToken != "" && o.vaultRole != "" {
		return nil, errors.New("--vault-token and --vault-role are mutually exclusive")
	}
	if err := o.kubernetesOptions.Validate(false); err != nil {
		return nil, err
	}
	return o, nil
}

func main() {
	version.Name = "vault-subpath-proxy"
	logrusutil.ComponentInit()
	logrus.SetLevel(logrus.DebugLevel)
	opts, err := gatherOptions()
	if err != nil {
		logrus.WithError(err).Fatal("Failed to get opts")
	}

	var privilegedVaultClient *vaultclient.VaultClient
	if opts.vaultRole != "" {
		privilegedVaultClient, err = vaultclient.NewFromKubernetesAuth(opts.vaultAddr, opts.vaultRole)
	} else if opts.vaultToken != "" {
		privilegedVaultClient, err = vaultclient.New(opts.vaultAddr, opts.vaultToken)
	}
	if err != nil {
		logrus.WithError(err).Fatal("failed to construct vault client")
	}

	var clientGetter func() map[string]ctrlruntimeclient.Client
	clientGetter, err = startLoadingKubeconfigs(opts.kubernetesOptions)
	if err != nil {
		logrus.WithError(err).Fatal("failed to load kubeconfigs")
	}

	server, err := createProxyServer(opts.vaultAddr, opts.listenAddr, opts.kvMountPath, clientGetter, privilegedVaultClient)
	if err != nil {
		logrus.WithError(err).Fatal("failed to create server")
	}
	listenFunc := server.ListenAndServe
	if opts.tlsCertFile != "" {
		reloader, err := newKeypairReloader(opts.tlsCertFile, opts.tlsKeyFile)
		if err != nil {
			logrus.WithError(err).Fatal("Failed to load tls cert and key")
		}
		server.TLSConfig = &tls.Config{GetCertificate: reloader.getCertificateFunc}
		listenFunc = func() error { return server.ListenAndServeTLS("", "") }

	}
	if err := listenFunc(); err != http.ErrServerClosed {
		logrus.WithError(err).Fatal("faield to listen and serve")
	}
}

func createProxyServer(vaultAddr string, listenAddr string, kvMountPath string, clients func() map[string]ctrlruntimeclient.Client, privilegedVaultClient *vaultclient.VaultClient) (*http.Server, error) {
	vaultClient, err := api.NewClient(&api.Config{Address: vaultAddr})
	if err != nil {
		return nil, fmt.Errorf("failed to create vault client: %w", err)
	}
	vaultURL, err := url.Parse(vaultAddr)
	if err != nil {
		return nil, fmt.Errorf("failed to parse vault addr: %w", err)
	}

	proxy := httputil.NewSingleHostReverseProxy(vaultURL)
	transport := &kvUpdateTransport{kvMountPath: kvMountPath, upstream: http.DefaultTransport, kubeClients: clients, privilegedVaultClient: privilegedVaultClient}
	transport.initialize()
	proxy.Transport = transport
	injector := &kvSubPathInjector{
		upstream:    retryablehttp.NewClient().StandardClient().Transport,
		kvMountPath: kvMountPath,
		vaultClient: vaultClient,
	}
	proxy.ModifyResponse = injector.inject
	return &http.Server{
		Addr:    listenAddr,
		Handler: proxy,
	}, nil
}

type kvSubPathInjector struct {
	upstream    http.RoundTripper
	kvMountPath string
	vaultClient *api.Client
}

func (i *kvSubPathInjector) inject(r *http.Response) error {
	logrus.WithFields(logrus.Fields{"method": r.Request.Method, "path": r.Request.URL.Path, "user-agent": r.Header.Get("User-Agent"), "status-code": r.StatusCode}).Trace("Got request")
	if r.StatusCode != 403 || r.Request.Method != http.MethodGet || !strings.HasPrefix(r.Request.URL.Path, fmt.Sprintf("/v1/%s/metadata", i.kvMountPath)) || r.Request.URL.Query().Get("list") != "true" {
		return nil
	}
	logrus.Trace("Attempting to insert additional data into request")

	bodyRaw, err := io.ReadAll(r.Body)
	if err != nil {
		// Should never happen and not recoverable, as we might already have
		// read parts of the body
		return fmt.Errorf("failed to read response body: %w", err)
	}
	log := logrus.WithField("path", r.Request.URL.Path)
	if err := r.Body.Close(); err != nil {
		log.WithError(err).Warn("Failed to close the original response body")
	}
	r.Body = io.NopCloser(bytes.NewBuffer(bodyRaw))

	if err := i.injectSubpathInfoIfNeeded(bodyRaw, r); err != nil {
		// To return or not to return?
		log.WithError(err).Error("Failed to inject subpath info")
	}

	return nil
}

func (i *kvSubPathInjector) injectSubpathInfoIfNeeded(reponseBody []byte, r *http.Response) error {
	var response api.Secret
	if err := json.Unmarshal(reponseBody, &response); err != nil {
		return fmt.Errorf("failed to unmarshal original response body: %w", err)
	}

	// If Vault already returned something, there is no need for us to do anything
	if val, ok := response.Data["keys"].([]string); ok && len(val) > 0 {
		return nil
	}

	vaultToken := r.Request.Header.Get(consts.AuthHeaderName)

	client, err := i.vaultClient.Clone()
	if err != nil {
		return fmt.Errorf("failed to clone vault client: %w", err)
	}
	client.SetToken(vaultToken)

	resultantACLRequest := client.NewRequest(http.MethodGet, "/v1/sys/internal/ui/resultant-acl")
	resultantACLHTTPResponse, err := client.RawRequest(resultantACLRequest)
	if err != nil {
		return fmt.Errorf("failed to query the resultant-acl api: %w", err)
	}
	// We can't help you or you are trying to break in :(
	if resultantACLHTTPResponse.StatusCode != http.StatusOK {
		return nil
	}

	var resultantACL resultantACLResponse
	if err := resultantACLHTTPResponse.DecodeJSON(&resultantACL); err != nil {
		return fmt.Errorf("failed to decode resultant-acl response: %w", err)
	}
	if err := resultantACLHTTPResponse.Body.Close(); err != nil {
		return fmt.Errorf("failed to close resultant acl response body: %w", err)
	}

	requestedFolder := strings.TrimPrefix(r.Request.URL.Path, fmt.Sprintf("/v1/%s/metadata", i.kvMountPath))
	requestedFolder = strings.TrimPrefix(requestedFolder, "/")

	var additionalFolders []string
	prefix := strings.Join([]string{fmt.Sprintf("%s/metadata", i.kvMountPath), requestedFolder}, "/")
	for path, perms := range resultantACL.Data.GlobPaths {
		if !strings.HasPrefix(path, prefix) || !strings.HasSuffix(path, "/") || !hasListCapability(perms.Capabilities) {
			continue
		}
		additionalFolders = append(additionalFolders, strings.TrimPrefix(path, prefix))
	}

	if len(additionalFolders) == 0 {
		return nil
	}

	response.Data = map[string]interface{}{"keys": additionalFolders}
	serializedResponse, err := json.Marshal(response)
	if err != nil {
		return fmt.Errorf("failed to serialize response after updating it: %w", err)
	}

	r.StatusCode = 200
	r.Body = io.NopCloser(bytes.NewBuffer(serializedResponse))
	r.ContentLength = int64(len(serializedResponse))
	r.Header.Set("Content-Length", strconv.Itoa(len(serializedResponse)))
	return nil
}

type resultantACLResponse struct {
	Data ResultantACLData `json:"data"`
}

type ResultantACLData struct {
	ExactPaths map[string]PathPerms `json:"exact_paths"`
	GlobPaths  map[string]PathPerms `json:"glob_paths"`
}

type PathPerms struct {
	Capabilities []string `json:"capabilities"`
}

func hasListCapability(capabilities []string) bool {
	for _, capability := range capabilities {
		if capability == "list" {
			return true
		}
	}

	return false
}

type keypairReloader struct {
	certMu   sync.RWMutex
	cert     *tls.Certificate
	certPath string
	keyPath  string
}

func newKeypairReloader(certPath, keyPath string) (*keypairReloader, error) {
	reloader := &keypairReloader{
		certPath: certPath,
		keyPath:  keyPath,
	}
	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return nil, err
	}
	reloader.cert = &cert
	go func() {
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			if err := reloader.maybeReload(); err != nil {
				logrus.WithError(err).Error("Failed to load tls cert and key")
			}
		}
	}()
	return reloader, nil
}

func (kpr *keypairReloader) maybeReload() error {
	newCert, err := tls.LoadX509KeyPair(kpr.certPath, kpr.keyPath)
	if err != nil {
		return err
	}
	kpr.certMu.Lock()
	defer kpr.certMu.Unlock()
	kpr.cert = &newCert
	return nil
}

func (kpr *keypairReloader) getCertificateFunc(_ *tls.ClientHelloInfo) (*tls.Certificate, error) {
	kpr.certMu.RLock()
	defer kpr.certMu.RUnlock()
	return kpr.cert, nil
}

func startLoadingKubeconfigs(kubernetesOptions flagutil.KubernetesOptions) (func() map[string]ctrlruntimeclient.Client, error) {
	clients := map[string]ctrlruntimeclient.Client{}
	clientsLock := sync.RWMutex{}

	clients, err := loadKubeconfigs(kubernetesOptions, func() {
		newClients, err := loadKubeconfigs(kubernetesOptions, nil)
		if err != nil {
			logrus.WithError(err).Error("failed to reload kubeconfigs after fsnotify event")
			return
		}
		clientsLock.Lock()
		clients = newClients
		clientsLock.Unlock()
	})
	if err != nil {
		return nil, fmt.Errorf("failed to load kubeconfigs: %w", err)
	}

	return func() map[string]ctrlruntimeclient.Client {
		clientsLock.RLock()
		defer clientsLock.RUnlock()
		return clients
	}, nil
}

func loadKubeconfigs(kubernetesOptions flagutil.KubernetesOptions, callBack func()) (map[string]ctrlruntimeclient.Client, error) {
	kubeconfigs, err := kubernetesOptions.LoadClusterConfigs(callBack)
	if err != nil {
		return nil, err
	}

	clients := map[string]ctrlruntimeclient.Client{}
	for cluster, config := range kubeconfigs {
		cluster, config := cluster, config
		client, err := ctrlruntimeclient.New(&config, ctrlruntimeclient.Options{})
		if err != nil {
			return nil, fmt.Errorf("failed to construct client for cluster %s: %w", cluster, err)
		}
		clients[cluster] = client
	}

	return clients, nil
}
