package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	url "net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/felixge/httpsnoop"
	"github.com/sirupsen/logrus"

	authenticationv1 "k8s.io/api/authentication/v1"
	authorizationv1 "k8s.io/api/authorization/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/test-infra/prow/config/secret"
	"k8s.io/test-infra/prow/interrupts"
	"k8s.io/test-infra/prow/logrusutil"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openshift/ci-tools/pkg/util"
)

type options struct {
	listenAddr        string
	exposedHost       string
	gracePeriod       time.Duration
	robotUsernameFile string
	robotPasswordFile string
	tlsCertFile       string
	tlsKeyFile        string
	intervalRaw       string
	interval          time.Duration
}

func gatherOptions() (*options, error) {
	o := &options{}
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	fs.StringVar(&o.listenAddr, "listen-addr", "127.0.0.1:8400", "The address the proxy shall listen on")
	fs.StringVar(&o.exposedHost, "exposed-host", "quay-proxy.ci.openshift.org", "The exposed host for the tool")
	fs.DurationVar(&o.gracePeriod, "gracePeriod", time.Second*10, "Grace period for server shutdown")
	fs.StringVar(&o.robotUsernameFile, "robot-username-file", "", "Path to a robot username file. Must not be empty.")
	fs.StringVar(&o.robotPasswordFile, "robot-password-file", "", "Path to a robot password file. Must not be empty.")
	fs.StringVar(&o.tlsCertFile, "tls-cert-file", "", "Path to a tls cert file. Must not be empty.")
	fs.StringVar(&o.tlsKeyFile, "tls-key-file", "", "Path to a tls key file. Must not be empty.")
	fs.StringVar(&o.intervalRaw, "interval", "30s", "Parseable duration string that specifies the period to refresh robot's quay.io bearer token")
	if err := fs.Parse(os.Args[1:]); err != nil {
		return nil, fmt.Errorf("failed to parse flags: %w", err)
	}
	return o, nil
}

func (o *options) validate() error {
	if o.exposedHost == "" {
		return errors.New("--exposed-host must not be empty")
	}
	if o.robotUsernameFile == "" {
		return errors.New("--robot-username-file must not be empty")
	}
	if o.robotPasswordFile == "" {
		return errors.New("--robot-password-file must not be empty")
	}
	if o.tlsCertFile == "" {
		return errors.New("--tls-cert-file must not be empty")
	}
	if o.tlsKeyFile == "" {
		return errors.New("--tls-key-file must not be empty")
	}
	interval, err := time.ParseDuration(o.intervalRaw)
	if err != nil {
		return fmt.Errorf("failed to parse interal: %w", err)
	}
	o.interval = interval
	return nil
}

func main() {
	logrusutil.ComponentInit()
	logrus.SetLevel(logrus.DebugLevel)
	opts, err := gatherOptions()
	if err != nil {
		logrus.WithError(err).Fatal("Failed to get opts")
	}
	if err := opts.validate(); err != nil {
		logrus.WithError(err).Fatal("Failed to complete opts")
	}

	for _, file := range []string{opts.robotUsernameFile, opts.robotPasswordFile} {
		if err := secret.Add(file); err != nil {
			logrus.WithError(err).WithField("file", file).Fatal("Failed to add secret file")
		}
	}

	ctx := interrupts.Context()
	tokenMaintainer := newRobotTokenMaintainer(opts.robotUsernameFile, opts.robotPasswordFile, secret.GetSecret)
	interrupts.Tick(func() { execute(ctx, tokenMaintainer) }, func() time.Duration { return opts.interval })

	inClusterConfig, err := util.LoadClusterConfig()
	if err != nil {
		logrus.WithError(err).Fatal("Failed to load in-cluster config")
	}
	ocClient, err := ctrlruntimeclient.New(inClusterConfig, ctrlruntimeclient.Options{})
	if err != nil {
		logrus.WithError(err).Fatal("Failed to create oc client")
	}

	proxyHandler, err := proxyHandler("https://quay.io", newTokenService(ctx, ocClient), tokenMaintainer)
	if err != nil {
		logrus.WithError(err).Fatal("Failed to create proxy handler")
	}
	handler := getRouter(proxyHandler, opts.exposedHost, newTokenService(ctx, ocClient), secret.GetSecret, opts.robotUsernameFile, opts.robotPasswordFile)
	interrupts.ListenAndServeTLS(&http.Server{Addr: opts.listenAddr, Handler: handler}, opts.tlsCertFile, opts.tlsKeyFile, opts.gracePeriod)
	interrupts.WaitForGracefulShutdown()
}

type robotTokenMaintainer struct {
	mutex        sync.Mutex
	token        string
	usernameFile string
	passwordFile string
	secretGetter func(secretPath string) []byte
	logger       *logrus.Entry
}

func newRobotTokenMaintainer(usernameFile string, passwordFile string, secretGetter func(secretPath string) []byte) *robotTokenMaintainer {
	return &robotTokenMaintainer{
		usernameFile: usernameFile,
		passwordFile: passwordFile,
		secretGetter: secretGetter,
		logger:       logrus.WithField("subComponent", "robotTokenMaintainer"),
	}
}

func execute(ctx context.Context, c *robotTokenMaintainer) {
	if err := wait.ExponentialBackoffWithContext(ctx, wait.Backoff{Duration: 2 * time.Second, Factor: 2, Steps: 3}, func(ctx context.Context) (done bool, err error) {
		if err := c.Run(); err != nil {
			logrus.WithError(err).Error("Failed to run robot token maintainer")
			return false, nil
		}
		logrus.Info("Succeeded running robot token maintainer")
		return true, nil
	}); err != nil {
		logrus.WithError(err).Error("Failed on running robot token maintainer even with retires")
	}
}

func (c *robotTokenMaintainer) GetRobotToken() (string, error) {
	if valid, err := c.isValid(); !valid || err != nil {
		if err != nil {
			c.logger.WithError(err).Error("Failed to validate the robot's token")
		}
		if err := c.renew(); err != nil {
			return "", fmt.Errorf("failed to renew token: %w", err)
		}
	}
	return c.token, nil
}

func (c *robotTokenMaintainer) Run() error {
	//https://access.redhat.com/solutions/3625131
	valid, err := c.isValid()
	if err != nil {
		return fmt.Errorf("failed to check if the token is still valid: %w", err)
	}
	if !valid {
		if err := c.renew(); err != nil {
			return fmt.Errorf("failed to renew the token: %w", err)
		}
	}
	return nil
}
func (c *robotTokenMaintainer) isValid() (valid bool, ret error) {
	if c.token == "" {
		return false, nil
	}
	req, err := http.NewRequest("GET", "https://quay.io/v2", nil)
	if err != nil {
		return false, fmt.Errorf("failed to create http request: %w", err)
	}
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", c.token))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false, fmt.Errorf("failed to connect to quay.io: %w", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			ret = err
		}
	}()
	if resp.StatusCode == http.StatusUnauthorized {
		return false, nil
	}
	if resp.StatusCode != http.StatusOK {
		if bodyBytes, err := io.ReadAll(resp.Body); err == nil {
			return false, fmt.Errorf("got unexpected status code %d from quay.io with response's body: %s", resp.StatusCode, string(bodyBytes))
		} else {
			return false, fmt.Errorf("got unexpected status code %d from quay.io and failed to read its body: %w", resp.StatusCode, err)
		}
	}
	return true, nil
}

func (c *robotTokenMaintainer) renew() (ret error) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.logger.Info("Renewing token ...")
	req, err := http.NewRequest("GET", "https://quay.io/v2/auth?service=quay.io&scope=repository:openshift/ci:pull", nil)
	if err != nil {
		return fmt.Errorf("failed to create http request: %w", err)
	}
	req.SetBasicAuth(string(c.secretGetter(c.usernameFile)), string(c.secretGetter(c.passwordFile)))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to connect to quay.io: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("got unexpected status code form quay.io: %d", resp.StatusCode)
	}
	bytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response body: %w", err)
	}
	r := TokenResponse{}
	if err := json.Unmarshal(bytes, &r); err != nil {
		return fmt.Errorf("failed to unmarshal response body: %w", err)
	}
	c.token = r.Token
	c.logger.Info("Renewed token")
	defer func() {
		if err := resp.Body.Close(); err != nil {
			ret = err
		}
	}()
	return nil
}

type TokenResponse struct {
	Token string `json:"token"`
}

func proxyHandler(target string, clusterTokenService ClusterTokenService, quayService QuayService) (http.Handler, error) {
	repoURL, err := url.Parse(target)
	if err != nil {
		return nil, fmt.Errorf("failed to parse qci-appci's url: %w", err)
	}
	proxy := httputil.NewSingleHostReverseProxy(repoURL)
	originalDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		originalDirector(req)
		modifyRequest(req, clusterTokenService, quayService)
	}
	proxy.ModifyResponse = modifyResponse
	return proxy, nil
}

func modifyRequest(req *http.Request, clusterTokenService ClusterTokenService, quayService QuayService) {
	l := logrus.WithFields(logrus.Fields{"path": req.URL.Path})
	l.Debug("Proxy received request")
	if path := req.URL.Path; path == "/v2/auth" {
		key := "service"
		value := "quay.io"
		logrus.WithField("path", path).WithField("key", key).WithField("value", value).Debug("Replacing params ...")
		values := req.URL.Query()
		values.Set(key, value)
		req.URL.RawQuery = values.Encode()
	} else {
		value := req.Header.Get("Authorization")
		if strings.HasPrefix(value, "Bearer ") {
			clusterToken := strings.TrimPrefix(value, "Bearer ")
			if valid, err := clusterTokenService.Validate(clusterToken); err != nil {
				logrus.WithError(err).Error("Failed to validate token")
			} else if valid {
				if quayToken, err := quayService.GetRobotToken(); err != nil {
					logrus.WithError(err).Error("Failed to get robot token")
				} else {
					logrus.Debug("Replacing bearer token ...")
					req.Header.Set("Authorization", strings.ReplaceAll(value, clusterToken, quayToken))
				}
			}
		}
	}
}

func modifyResponse(resp *http.Response) error {
	// Only logging here for debugging, nothing is modified
	statusCode := resp.StatusCode
	l := logrus.WithField("statusCode", statusCode)
	if statusCode == http.StatusUnauthorized {
		l = logrus.WithField("authenticateHeader", resp.Header.Get("www-authenticate"))
	}
	l.Debug("Proxy responded")
	return nil
}

type QuayService interface {
	GetRobotToken() (string, error)
}

type ClusterTokenService interface {
	Validate(token string) (bool, error)
}

type SimpleClusterTokenService struct {
	ctx    context.Context
	client ctrlruntimeclient.Client
}

func newTokenService(ctx context.Context, client ctrlruntimeclient.Client) ClusterTokenService {
	return &SimpleClusterTokenService{ctx: ctx, client: client}
}

func (s *SimpleClusterTokenService) Validate(token string) (bool, error) {
	tr := &authenticationv1.TokenReview{
		Spec: authenticationv1.TokenReviewSpec{
			Token: token,
		},
	}
	if err := s.client.Create(s.ctx, tr); err != nil {
		return false, fmt.Errorf("failed to check token: %w", err)
	}

	if !tr.Status.Authenticated {
		return false, nil
	}

	username := tr.Status.User.Username
	// SAR check only applies to human users
	if strings.HasPrefix(username, "system:serviceaccount:") {
		return true, nil
	}

	sar := &authorizationv1.SubjectAccessReview{
		Spec: authorizationv1.SubjectAccessReviewSpec{
			User:   username,
			Groups: tr.Status.User.Groups,
			ResourceAttributes: &authorizationv1.ResourceAttributes{
				Group:       "image.openshift.io",
				Version:     "v1",
				Resource:    "imagestreams",
				Subresource: "layers",
				Verb:        "get",
			},
		},
	}

	if err := s.client.Create(s.ctx, sar); err != nil {
		return false, fmt.Errorf("failed to create SubjectAccessReview for user %s: %w", username, err)
	}

	return sar.Status.Allowed, nil
}

type appHandler struct {
	proxy               http.Handler
	host                string
	clusterTokenService ClusterTokenService
	secretGetter        func(string) []byte
	robotUsernameFile   string
	robotPasswordFile   string
}

func (h *appHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	l := logrus.WithFields(logrus.Fields{"path": path})

	if path == "/healthz" {
		if _, err := fmt.Fprintln(w, http.StatusText(http.StatusOK)); err != nil {
			l.WithError(err).Error("failed to write response")
		}
		return
	}

	if path == "/v2/auth" {
		username, password, ok := r.BasicAuth()
		if !ok {
			w.Header().Add("Www-Authenticate", fmt.Sprintf("Bearer realm=\"https://%s/v2/auth\",service=\"%s\"", h.host, h.host))
			http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
			l.WithField("basic", ok).Debug("Failed to get basic auth")
			return
		}
		if username != string(h.secretGetter(h.robotUsernameFile)) || password != string(h.secretGetter(h.robotPasswordFile)) {
			valid, err := h.clusterTokenService.Validate(password)
			if err != nil || !valid {
				w.Header().Add("Www-Authenticate", fmt.Sprintf("Bearer realm=\"https://%s/v2/auth\",service=\"%s\"", h.host, h.host))
				http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
				l.WithField("username", username).WithError(err).WithField("valid", valid).Debug("Failed to validate the user")
				return
			}
			w.Header().Set("Content-Type", "application/json")
			body := map[string]string{"token": password}
			if err := json.NewEncoder(w).Encode(body); err != nil {
				l.WithError(err).Error("failed to encode body")
				return
			}
			l.WithField("username", username).Debug("Returned password as token")
			return
		}
		l.WithField("username", username).Debug("Provide token for the robot user with proxy")
		r.URL.Host = "quay.io"
		http.Redirect(w, r, r.URL.RequestURI(), http.StatusMovedPermanently)
		return
	}

	v := r.Header.Get("Authorization")
	hasToken := strings.HasPrefix(v, "Bearer ") && strings.TrimPrefix(v, "Bearer ") != ""
	if !hasToken && path != "/v2/auth" {
		w.Header().Add("Www-Authenticate", fmt.Sprintf("Bearer realm=\"https://%s/v2/auth\",service=\"%s\"", h.host, h.host))
		http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
		l.Debug("Found no token")
		return
	}

	h.proxy.ServeHTTP(w, r)
}

func getRouter(proxy http.Handler, host string, clusterTokenService ClusterTokenService, secretGetter func(string) []byte, robotUsernameFile, robotPasswordFile string) http.Handler {
	serveMux := http.NewServeMux()
	appHandler := &appHandler{proxy: proxy, host: host, clusterTokenService: clusterTokenService, secretGetter: secretGetter, robotUsernameFile: robotUsernameFile, robotPasswordFile: robotPasswordFile}
	logHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m := httpsnoop.CaptureMetrics(appHandler, w, r)
		h, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			host = r.RemoteAddr
		}
		uri := r.RequestURI
		if r.ProtoMajor == 2 && r.Method == "CONNECT" {
			uri = r.Host
		}
		if uri == "" {
			uri = r.URL.RequestURI()
		}
		v := r.Header.Get("Authorization")
		hasToken := strings.HasPrefix(v, "Bearer ") && strings.TrimPrefix(v, "Bearer ") != ""
		logrus.WithFields(
			logrus.Fields{
				"method":   r.Method,
				"uri":      uri,
				"code":     m.Code,
				"size":     m.Written,
				"duration": m.Duration,
				"token":    hasToken,
				"host":     h,
			}).Debug("Access log")
	})
	serveMux.Handle("/", logHandler)
	return serveMux
}
