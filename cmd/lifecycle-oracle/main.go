package main

import (
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/sirupsen/logrus"

	prowflagutil "k8s.io/test-infra/prow/flagutil"
	"k8s.io/test-infra/prow/interrupts"
	"k8s.io/test-infra/prow/logrusutil"
	"k8s.io/test-infra/prow/pjutil"
	pprofutil "k8s.io/test-infra/prow/pjutil/pprof"
	"k8s.io/test-infra/prow/version"
)

type options struct {
	port                   int
	instrumentationOptions prowflagutil.InstrumentationOptions
	dataFile               string
}

func (o *options) validate() error {
	if o.port == 0 {
		return errors.New("--port is required")
	}
	if o.dataFile == "" {
		return errors.New("--data-file is required")
	}
	return nil
}

func bindOptions(fs *flag.FlagSet) *options {
	o := options{}
	o.instrumentationOptions.AddFlags(fs)
	fs.IntVar(&o.port, "port", 0, "Port to serve admission webhooks on.")
	fs.StringVar(&o.dataFile, "data-file", "", "Path to file containing lifecycle phase data to serve.")
	return &o
}

func main() {
	flagSet := flag.NewFlagSet("", flag.ExitOnError)
	opts := bindOptions(flagSet)
	if err := flagSet.Parse(os.Args[1:]); err != nil {
		logrus.WithError(err).Fatal("failed to parse flags")
	}
	if err := opts.validate(); err != nil {
		logrus.WithError(err).Fatal("Failed to validate flags")
	}
	logrusutil.ComponentInit()
	logrus.Infof("%s version %s", version.Name, version.Version)

	pprofutil.Instrument(opts.instrumentationOptions)
	health := pjutil.NewHealthOnPort(opts.instrumentationOptions.HealthPort)

	mux := http.NewServeMux()
	mux.Handle("/api/phases/", http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		data, err := os.Open(opts.dataFile)
		if err != nil {
			http.Error(writer, fmt.Sprintf("Could not open data file: %v", err), 500)
		}
		info, err := data.Stat()
		if err != nil {
			http.Error(writer, fmt.Sprintf("Could not stat data file: %v", err), 500)
		}
		http.ServeContent(writer, request, data.Name(), info.ModTime(), data)
	}))
	httpServer := &http.Server{Addr: ":" + strconv.Itoa(opts.port), Handler: mux}
	interrupts.ListenAndServe(httpServer, 5*time.Second)
	health.ServeReady()

	interrupts.WaitForGracefulShutdown()
}
