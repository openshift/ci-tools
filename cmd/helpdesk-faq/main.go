package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"html/template"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/sirupsen/logrus"

	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/prow/pkg/flagutil"
	"sigs.k8s.io/prow/pkg/interrupts"
	"sigs.k8s.io/prow/pkg/logrusutil"

	helpdeskfaq "github.com/openshift/ci-tools/pkg/helpdesk-faq"
	"github.com/openshift/ci-tools/pkg/util"
)

const (
	ci = "ci"
)

type options struct {
	logLevel          string
	port              int
	gracePeriod       time.Duration
	kubernetesOptions flagutil.KubernetesOptions
}

type Page struct {
	Data []helpdeskfaq.FaqItem `json:"data"`
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

func router(client helpdeskfaq.FaqItemClient) *http.ServeMux {
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

		items, err := client.GetSerializedFAQItems()
		if err != nil {
			logrus.WithError(err).Fatal("unable to get helpdesk-faq items")
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		page := Page{}
		for _, item := range items {
			faqItem := &helpdeskfaq.FaqItem{}
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

	inClusterConfig, err := util.LoadClusterConfig()
	if err != nil {
		logrus.WithError(err).Fatal("Failed to load in-cluster config")
	}
	kubeClient, err := ctrlruntimeclient.New(inClusterConfig, ctrlruntimeclient.Options{})
	if err != nil {
		logrus.WithError(err).Fatal("Failed to create client")
	}
	client := helpdeskfaq.NewCMClient(kubeClient, ci, logrus.WithField("client", "cm-client"))
	server := &http.Server{
		Addr:    ":" + strconv.Itoa(o.port),
		Handler: router(&client),
	}
	interrupts.ListenAndServe(server, o.gracePeriod)
	logrus.Debug("Server ready.")
	interrupts.WaitForGracefulShutdown()
}
