package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"

	log "github.com/sirupsen/logrus"
	"k8s.io/test-infra/prow/interrupts"

	"github.com/openshift/ci-tools/pkg/config"
	"github.com/openshift/ci-tools/pkg/load"
)

const (
	orgQuery     = "org"
	repoQuery    = "repo"
	branchQuery  = "branch"
	variantQuery = "variant"
)

type options struct {
	configPath  string
	logLevel    string
	address     string
	gracePeriod time.Duration
	cycle       time.Duration
}

func gatherOptions() options {
	o := options{}
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	fs.StringVar(&o.configPath, "config", "", "Path to config dirs")
	fs.StringVar(&o.logLevel, "log-level", "info", "Level at which to log output.")
	fs.StringVar(&o.address, "address", ":8080", "Address to run server on")
	fs.DurationVar(&o.gracePeriod, "gracePeriod", time.Second*10, "Grace period for server shutdown")
	fs.DurationVar(&o.cycle, "cycle", time.Minute*2, "Cycle duration for config reload")
	fs.Parse(os.Args[1:])
	return o
}

func validateOptions(o options) error {
	_, err := log.ParseLevel(o.logLevel)
	if err != nil {
		return fmt.Errorf("invalid --log-level: %v", err)
	}
	if o.cycle == 0 {
		return fmt.Errorf("invalid cycle: duration cannot equal 0")
	}
	return nil
}

func missingQuery(w http.ResponseWriter, field string) {
	w.WriteHeader(http.StatusBadRequest)
	fmt.Fprintf(w, "%s query missing or incorrect", field)
}

func resolveConfig(agent load.ConfigAgent) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			w.WriteHeader(http.StatusNotImplemented)
			w.Write([]byte(http.StatusText(http.StatusNotImplemented)))
			return
		}
		org := r.URL.Query().Get(orgQuery)
		if org == "" {
			missingQuery(w, orgQuery)
			return
		}
		repo := r.URL.Query().Get(repoQuery)
		if repo == "" {
			missingQuery(w, repoQuery)
			return
		}
		branch := r.URL.Query().Get(branchQuery)
		if branch == "" {
			missingQuery(w, branchQuery)
			return
		}
		variant := r.URL.Query().Get(variantQuery)
		info := config.Info{
			Org:     org,
			Repo:    repo,
			Branch:  branch,
			Variant: variant,
		}

		config, err := agent.GetConfig(info)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprintf(w, "failed to get config: %v", err)
			return
		}
		jsonConfig, err := json.MarshalIndent(config, "", "  ")
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprintf(w, "failed to marshal config to JSON: %v", err)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write(jsonConfig)
	}
}

func getGeneration(agent load.ConfigAgent) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "%d", agent.GetGeneration())
	}
}

func main() {
	o := gatherOptions()
	err := validateOptions(o)
	if err != nil {
		log.Fatalf("invalid options: %v", err)
	}
	level, _ := log.ParseLevel(o.logLevel)
	log.SetLevel(level)

	configAgent, err := load.NewConfigAgent(o.configPath, o.cycle)
	if err != nil {
		log.Fatalf("Failed to get config agent: %v", err)
	}

	http.HandleFunc("/config", resolveConfig(configAgent))
	http.HandleFunc("/generation", getGeneration(configAgent))
	interrupts.ListenAndServe(&http.Server{Addr: o.address}, o.gracePeriod)
	interrupts.WaitForGracefulShutdown()
}
