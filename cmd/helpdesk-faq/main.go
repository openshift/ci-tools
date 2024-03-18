package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"html/template"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/sirupsen/logrus"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/test-infra/prow/flagutil"
	"k8s.io/test-infra/prow/interrupts"
	"k8s.io/test-infra/prow/logrusutil"

	"github.com/openshift/ci-tools/pkg/api"
)

const (
	appCIContextName = string(api.ClusterAPPCI)
	faqConfigMap     = "helpdesk-faq"
	ci               = "ci"
)

type faqItemClient interface {
	GetFAQItems() ([]string, error)
}

type configMapClient struct {
	kubeClient  kubernetes.Interface
	cachedItems []string
	lastReload  time.Time
}

func (c *configMapClient) GetFAQItems() ([]string, error) {
	fifteenMinutesFromLastCacheReload := c.lastReload.Add(time.Minute * 15)
	if len(c.cachedItems) > 0 && time.Now().Before(fifteenMinutesFromLastCacheReload) {
		logrus.Debugf("returning faq items from cache")
		return c.cachedItems, nil
	}
	logrus.Debugf("reloading faq items from configmap")
	configMap, err := c.getConfigMap()
	if err != nil {
		return nil, err
	}
	if configMap.Data == nil {
		return nil, nil
	}
	var items []string
	for _, item := range configMap.Data {
		items = append(items, item)
	}
	c.cachedItems = items
	c.lastReload = time.Now()
	return items, nil
}

func (c *configMapClient) getConfigMap() (*v1.ConfigMap, error) {
	configMap, err := c.kubeClient.CoreV1().ConfigMaps(ci).Get(context.TODO(), faqConfigMap, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get configMap %s: %w", faqConfigMap, err)
	}
	return configMap, nil
}

type options struct {
	logLevel          string
	port              int
	gracePeriod       time.Duration
	kubernetesOptions flagutil.KubernetesOptions
}

type Page struct {
	Data []FaqItem `json:"data"`
}

// TODO(sgoeddel): these structs will be placed in a common package somewhere so slack-bot can use them too
type FaqItem struct {
	Question  string   `json:"question"`
	Timestamp string   `json:"timestamp"`
	Author    string   `json:"author"`
	Answers   []Answer `json:"answers"`
}

type Answer struct {
	Author    string `json:"author"`
	Timestamp string `json:"timestamp"`
	Body      string `json:"body"`
}

func gatherOptions() (options, error) {
	o := options{}
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	fs.StringVar(&o.logLevel, "log-level", "info", "Level at which to log output.")
	fs.IntVar(&o.port, "port", 8080, "Port to run the server on")
	o.kubernetesOptions.AddFlags(fs)
	fs.DurationVar(&o.gracePeriod, "gracePeriod", time.Second*10, "Grace period for server shutdown")
	if err := fs.Parse(os.Args[1:]); err != nil {
		return o, fmt.Errorf("failed to parse flags: %w", err)
	}
	return o, nil
}

func validateOptions(o options) error {
	_, err := logrus.ParseLevel(o.logLevel)
	if err != nil {
		return fmt.Errorf("invalid --log-level: %w", err)
	}
	return o.kubernetesOptions.Validate(false)
}

func router(client faqItemClient) *http.ServeMux {
	handler := http.NewServeMux()

	handler.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		page := map[string]bool{"ok": true}
		if err := json.NewEncoder(w).Encode(page); err != nil {
			logrus.WithError(err).WithField("page", page).Error("failed to encode page")
		}
	})

	handler.HandleFunc("/api/v1/faq-items", func(w http.ResponseWriter, r *http.Request) {
		logrus.WithField("path", "/api/v1/faq-items").Info("serving")

		items, err := client.GetFAQItems()
		if err != nil {
			logrus.WithError(err).Fatal("unable to get helpdesk-faq items")
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		page := Page{}
		for _, item := range items {
			faqItem := &FaqItem{}
			if err := json.Unmarshal([]byte(item), faqItem); err != nil {
				logrus.WithError(err).Fatal("unable to unmarshall faq item")
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			page.Data = append(page.Data, *faqItem)
		}

		if callbackName := r.URL.Query().Get("callback"); callbackName != "" {
			bytes, err := json.Marshal(page)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/javascript")
			template.JSEscape(w, []byte(callbackName))
			if n, err := fmt.Fprintf(w, "(%s);", string(bytes)); err != nil {
				logrus.WithError(err).WithField("n", n).Error("failed to write content")
			}
		} else {
			w.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(w).Encode(page); err != nil {
				logrus.WithError(err).WithField("page", page).Error("failed to encode page")
			}
		}
	})

	return handler
}

func main() {
	logrusutil.ComponentInit()
	o, err := gatherOptions()
	if err != nil {
		logrus.WithError(err).Fatal("failed go gather options")
	}
	if err := validateOptions(o); err != nil {
		logrus.WithError(err).Fatal("invalid options")
	}
	level, _ := logrus.ParseLevel(o.logLevel)
	logrus.SetLevel(level)

	kubeClient, err := o.kubernetesOptions.ClusterClientForContext(appCIContextName, false)
	if err != nil {
		logrus.WithError(err).Fatal("could not load kube config")
	}
	client := configMapClient{kubeClient: kubeClient}
	server := &http.Server{
		Addr:    ":" + strconv.Itoa(o.port),
		Handler: router(&client),
	}
	interrupts.ListenAndServe(server, o.gracePeriod)
	logrus.Debug("Server ready.")
	interrupts.WaitForGracefulShutdown()
}
