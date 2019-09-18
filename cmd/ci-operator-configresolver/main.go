package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/ghodss/yaml"
	log "github.com/sirupsen/logrus"
	"k8s.io/test-infra/prow/interrupts"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/config"
)

const (
	orgQuery     = "org"
	repoQuery    = "repo"
	branchQuery  = "branch"
	variantQuery = "variant"
)

type filenameToConfig map[string]api.ReleaseBuildConfiguration

type options struct {
	configs     filenameToConfig
	configPath  string
	logLevel    string
	address     string
	gracePeriod time.Duration
}

func gatherOptions() options {
	o := options{}
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	fs.StringVar(&o.configPath, "config", "", "Path to config dirs")
	fs.StringVar(&o.logLevel, "log-level", "info", "Level at which to log output.")
	fs.StringVar(&o.address, "address", ":8080", "Address to run server on")
	fs.DurationVar(&o.gracePeriod, "gracePeriod", time.Second*10, "Grace period for server shutdown")
	fs.Parse(os.Args[1:])
	return o
}

func validateOptions(o options) error {
	_, err := log.ParseLevel(o.logLevel)
	if err != nil {
		return fmt.Errorf("invalid --log-level: %v", err)
	}
	return nil
}

func missingQuery(w http.ResponseWriter, field string) {
	w.WriteHeader(http.StatusBadRequest)
	fmt.Fprintf(w, "%s query missing or incorrect", field)
	return
}

func resolveConfig(o *options) http.HandlerFunc {
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

		config, ok := o.configs[info.Basename()]
		if !ok {
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte("config does not exist"))
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

// loadFilenameToConfig generates a new filenameToConfig map.
func loadFilenameToConfig(configPath string) (filenameToConfig, error) {
	log.Debug("Reloading configs")
	configs := filenameToConfig{}
	err := filepath.Walk(configPath, func(path string, info os.FileInfo, err error) error {
		ext := filepath.Ext(path)
		if info != nil && !info.IsDir() && (ext == ".yml" || ext == ".yaml") {
			data, err := ioutil.ReadFile(path)
			if err != nil {
				return fmt.Errorf("failed to read ci-operator config (%v)", err)
			}

			var configSpec *api.ReleaseBuildConfiguration
			if err := yaml.Unmarshal(data, &configSpec); err != nil {
				return fmt.Errorf("failed to load ci-operator config (%v)", err)
			}

			if err := configSpec.ValidateAtRuntime(); err != nil {
				return fmt.Errorf("invalid ci-operator config: %v", err)
			}
			log.Debugf("Adding %s to filenameToConfig", filepath.Base(path))
			configs[filepath.Base(path)] = *configSpec
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	log.Info("Configs reloaded")
	return configs, nil
}

func main() {
	o := gatherOptions()
	err := validateOptions(o)
	if err != nil {
		log.Fatalf("invalid options: %v", err)
	}
	level, _ := log.ParseLevel(o.logLevel)
	log.SetLevel(level)

	o.configs, err = loadFilenameToConfig(o.configPath)
	if err != nil {
		log.Fatalf("Failed to load configs: %s", err)
	}

	http.HandleFunc("/config", resolveConfig(&o))
	interrupts.ListenAndServe(&http.Server{Addr: o.address}, o.gracePeriod)
	interrupts.WaitForGracefulShutdown()
}
