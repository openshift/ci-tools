package main

import (
	"bytes"
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
	"time"

	"github.com/sirupsen/logrus"
	"gopkg.in/yaml.v3"

	aggerrs "k8s.io/apimachinery/pkg/util/errors"
	"sigs.k8s.io/prow/pkg/flagutil"
	prowflagutil "sigs.k8s.io/prow/pkg/flagutil"
	"sigs.k8s.io/prow/pkg/github"
	"sigs.k8s.io/prow/pkg/interrupts"
	"sigs.k8s.io/prow/pkg/pjutil"
	"sigs.k8s.io/prow/pkg/version"
)

// URL enhances url.URL by adding the unmarshal capability.
type URL struct {
	*url.URL
}

// UnmarshalText implements [encoding.UnmarshalText].
func (u *URL) UnmarshalText(text []byte) error {
	parsed, err := url.Parse(string(text))
	if err != nil {
		return err
	}
	u.URL = parsed
	return nil
}

type Config struct {
	Routes       []Route `yaml:"routes,omitempty"`
	DefaultRoute *Route  `yaml:"default_route,omitempty"`
}

func (c *Config) validate() error {
	var errs []error

	if c.DefaultRoute == nil {
		errs = append(errs, errors.New("default route is not defined"))
	} else if c.DefaultRoute.Target == nil {
		errs = append(errs, errors.New("default route target URL is not defined"))
	}

	for i, route := range c.Routes {
		if route.Target == nil {
			errs = append(errs, fmt.Errorf("route[%d]: target is nil", i))
		}
	}

	return aggerrs.NewAggregate(errs)
}

type Route struct {
	Target  *URL    `yaml:"target,omitempty"`
	Matches []Match `yaml:"matches,omitempty"`
}

type Match struct {
	Org   string   `yaml:"org,omitempty"`
	Repos []string `yaml:"repos,omitempty"`
}

type router struct {
	log    *logrus.Entry
	config *Config
}

func (r *router) rewrite(pr *httputil.ProxyRequest) {
	var targetURL *url.URL
	var body []byte

	defer func() {
		if body != nil {
			pr.Out.Body = io.NopCloser(bytes.NewBuffer(body))
		}

		if targetURL == nil {
			targetURL = r.config.DefaultRoute.Target.URL
		}

		pr.SetXForwarded()
		pr.SetURL(targetURL)
		pr.Out.URL = targetURL
	}()

	req := pr.In
	log := r.log

	eventType := req.Header.Get("X-GitHub-Event")
	if eventType == "" {
		log.Error("Missing X-GitHub-Event Header")
		return
	}
	log = log.WithField("event-type", eventType)

	eventGUID := req.Header.Get("X-GitHub-Delivery")
	if eventGUID == "" {
		log.Error("Missing X-GitHub-Delivery Header")
		return
	}
	log = log.WithField("event-guid", eventGUID)

	if req.Method != http.MethodPost {
		log.Error("POST method only is allowed")
		return
	}

	if req.Header.Get("content-type") != "application/json" {
		log.Error("Invalid content-type: application/json only")
		return
	}

	body, err := io.ReadAll(req.Body)
	if err != nil {
		body = nil
		log.WithError(err).Error("Failed to read request body")
		return
	}

	var event github.GenericEvent
	if err := json.Unmarshal(body, &event); err != nil {
		log.WithError(err).Error("Failed to unmarshal request in a generic event")
		return
	}

	targetURL = r.matchRoute(log, event)
}

func (r *router) matchRoute(log *logrus.Entry, event github.GenericEvent) *url.URL {
	org, repo := event.Org.Login, event.Repo.Name

	if org == "" {
		log.Info("Org is empty")
		return nil
	}

	if repo == "" {
		log.Info("Repo is empty")
		return nil
	}

	log = log.WithField("org", org).WithField("repo", repo)

	for _, route := range r.config.Routes {
		log = log.WithField("target-url", route.Target)

	matchLoop:
		for _, m := range route.Matches {
			if m.Org == "*" {
				log.Info("Match found: match * org wildcard")
				return route.Target.URL
			}

			if m.Org != org {
				continue matchLoop
			}

			for _, r := range m.Repos {
				if r == "*" {
					log.Infof("Match found: match org %q and * repo wildcard", org)
					return route.Target.URL
				}

				if r == repo {
					log.Infof("Match found: match org %q and repo %q", org, repo)
					return route.Target.URL
				}
			}
		}
	}

	return nil
}

func newRouter(log *logrus.Entry, config *Config) *router {
	return &router{
		log:    log,
		config: config,
	}
}

type options struct {
	port        int
	configPath  string
	gracePeriod time.Duration

	instrumentationOptions prowflagutil.InstrumentationOptions
}

func (o *options) Validate() error {
	return nil
}

func gatherOptions(fs *flag.FlagSet, args ...string) (options, error) {
	var o options

	for _, group := range []flagutil.OptionGroup{&o.instrumentationOptions} {
		group.AddFlags(fs)
	}

	fs.IntVar(&o.port, "port", 8888, "Port to listen on")
	fs.StringVar(&o.configPath, "config-path", "/etc/config/config.yaml", "Configuration path")
	fs.DurationVar(&o.gracePeriod, "grace-period", 180*time.Second, "On shutdown, try to handle remaining events for the specified duration.")

	if err := fs.Parse(args); err != nil {
		return o, err
	}

	return o, nil
}

func loadConfig(configPath string) (*Config, error) {
	configBytes, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", configPath, err)
	}

	c := Config{}
	if err := yaml.Unmarshal(configBytes, &c); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}

	if err := c.validate(); err != nil {
		return nil, fmt.Errorf("validate config: %w", err)
	}

	return &c, nil
}

func main() {
	logger := logrus.NewEntry(logrus.StandardLogger())
	logger.Logger.SetFormatter(&logrus.JSONFormatter{})
	logrus.Infof("%s version %s", version.Name, version.Version)

	o, err := gatherOptions(flag.NewFlagSet(os.Args[0], flag.ExitOnError), os.Args[1:]...)
	if err != nil {
		logrus.WithError(err).Fatal("Failed to gather options")
	}

	if err := o.Validate(); err != nil {
		logrus.WithError(err).Fatal("Invalid options")
	}

	config, err := loadConfig(o.configPath)
	if err != nil {
		logrus.WithError(err).Fatal("Failed to load the configuration")
	}

	router := newRouter(logger, config)
	server := &http.Server{
		Addr: ":" + strconv.Itoa(o.port),
		Handler: &httputil.ReverseProxy{
			Rewrite: router.rewrite,
		},
	}

	health := pjutil.NewHealthOnPort(o.instrumentationOptions.HealthPort)
	health.ServeReady()

	interrupts.ListenAndServe(server, o.gracePeriod)
	interrupts.WaitForGracefulShutdown()
}
