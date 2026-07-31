package main

import (
	"context"
	"errors"
	"flag"
	"net/http"
	"net/http/httputil"
	"os"
	"strconv"
	"time"

	"github.com/sirupsen/logrus"

	prowflagutil "sigs.k8s.io/prow/pkg/flagutil"
	"sigs.k8s.io/prow/pkg/interrupts"
	"sigs.k8s.io/prow/pkg/pjutil"
	"sigs.k8s.io/prow/pkg/version"
)

type options struct {
	port        int
	configPath  string
	gracePeriod time.Duration

	instrumentationOptions prowflagutil.InstrumentationOptions
}

func gatherOptions(fs *flag.FlagSet, args ...string) (options, error) {
	var o options

	for _, group := range []prowflagutil.OptionGroup{&o.instrumentationOptions} {
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

func main() {
	logger := logrus.NewEntry(logrus.StandardLogger())
	logger.Logger.SetFormatter(&logrus.JSONFormatter{})
	logrus.Infof("%s version %s", version.Name, version.Version)

	o, err := gatherOptions(flag.NewFlagSet(os.Args[0], flag.ExitOnError), os.Args[1:]...)
	if err != nil {
		logrus.WithError(err).Fatal("Failed to gather options")
	}

	router, err := newRouter(logger, o.configPath)
	if err != nil {
		logrus.WithError(err).Fatal("Failed to create the router")
	}

	ctx, cancel := context.WithCancelCause(context.Background())
	if err := watchConfig(ctx, logger, o.configPath, router.loadConfig); err != nil {
		logrus.WithError(err).Fatal("Failed to start configuration watcher")
	}

	server := &http.Server{
		Addr: ":" + strconv.Itoa(o.port),
		Handler: &httputil.ReverseProxy{
			Rewrite: router.rewrite,
		},
	}

	health := pjutil.NewHealthOnPort(o.instrumentationOptions.HealthPort)
	health.ServeReady()

	interrupts.ListenAndServe(server, o.gracePeriod)
	interrupts.OnInterrupt(func() {
		cancel(errors.New("interrupt received"))
	})
	interrupts.WaitForGracefulShutdown()
}
