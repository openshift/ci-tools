package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"sync"

	"github.com/sirupsen/logrus"
	"gopkg.in/yaml.v3"

	"sigs.k8s.io/prow/pkg/github"
)

type router struct {
	log        *logrus.Entry
	m          *sync.RWMutex
	configPath string
	conf       *Config
}

func (r *router) rewrite(pr *httputil.ProxyRequest) {
	var targetURL *url.URL
	var body []byte
	conf := r.config()

	defer func() {
		if body != nil {
			pr.Out.Body = io.NopCloser(bytes.NewBuffer(body))
		}

		if targetURL == nil {
			targetURL = conf.DefaultRoute.Target.URL
		}

		pr.SetXForwarded()
		pr.SetURL(targetURL)
		// Overwrite the URL since we don't want the path-joining feature of SetURL()
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

	targetURL = r.matchRoute(log, conf, event)
}

func (r *router) matchRoute(log *logrus.Entry, conf *Config, event github.GenericEvent) *url.URL {
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

	for _, route := range conf.Routes {
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

func (r *router) config() *Config {
	r.m.RLock()
	defer r.m.RUnlock()
	return r.conf
}

func (r *router) loadConfig() error {
	configBytes, err := os.ReadFile(r.configPath)
	if err != nil {
		return fmt.Errorf("read config %s: %w", r.configPath, err)
	}

	c := Config{}
	if err := yaml.Unmarshal(configBytes, &c); err != nil {
		return fmt.Errorf("unmarshal config: %w", err)
	}

	if err := c.validate(); err != nil {
		return fmt.Errorf("validate config: %w", err)
	}

	r.m.Lock()
	defer r.m.Unlock()
	r.conf = &c

	return nil
}

func newRouter(log *logrus.Entry, configPath string) (*router, error) {
	r := router{
		log:        log,
		m:          &sync.RWMutex{},
		configPath: configPath,
	}

	if err := r.loadConfig(); err != nil {
		return nil, fmt.Errorf("load config %s: %w", configPath, err)
	}

	return &r, nil
}
