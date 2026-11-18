package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/sirupsen/logrus"

	"github.com/openshift/ci-tools/pkg/efsclusterwriter"
)

type options struct {
	efsRoot        string
	backupDir      string
	interval       time.Duration
	maxMoves       int
	maxMovePercent float64
	dryRun         bool
	assignmentsURL string
	rescanURL      string
	controlToken   string
	listenAddress  string
}

func gatherOptions() options {
	o := options{}
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	fs.StringVar(&o.efsRoot, "efs-root", "", "Root of the EFS job-config tree.")
	fs.StringVar(&o.backupDir, "backup-dir", "", "Directory for per-generation backups; should be under a '..'-prefixed path.")
	fs.DurationVar(&o.interval, "interval", 5*time.Minute, "Safety-net reconcile interval.")
	fs.IntVar(&o.maxMoves, "max-moves", 5, "Minimum number of job moves allowed per pass.")
	fs.Float64Var(&o.maxMovePercent, "max-move-percent", 25, "Maximum percentage of jobs moved per pass; the larger of this and --max-moves is used.")
	fs.BoolVar(&o.dryRun, "dry-run", false, "Report changes without writing.")
	fs.StringVar(&o.assignmentsURL, "assignments-url", "http://prowjob-dispatcher.ci.svc.cluster.local:8080/assignments", "Dispatcher bulk assignments endpoint.")
	fs.StringVar(&o.rescanURL, "rescan-url", "http://prowjob-dispatcher.ci.svc.cluster.local:8080/control/v1/rescan", "Dispatcher rescan endpoint.")
	fs.StringVar(&o.controlToken, "control-token-path", "", "Path to the dispatcher control bearer token.")
	fs.StringVar(&o.listenAddress, "listen-address", ":8080", "HTTP listen address.")
	if err := fs.Parse(os.Args[1:]); err != nil {
		logrus.WithError(err).Fatal("could not parse flags")
	}
	return o
}

func (o options) validate() error {
	if o.efsRoot == "" {
		return fmt.Errorf("--efs-root is required")
	}
	if o.interval < 10*time.Second {
		return fmt.Errorf("--interval must be at least 10s")
	}
	return nil
}

type status struct {
	mu         sync.Mutex
	lastRun    time.Time
	lastResult efsclusterwriter.Result
	lastError  string
	ready      bool
}

func (s *status) set(res efsclusterwriter.Result, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastRun = time.Now()
	s.lastResult = res
	s.lastError = ""
	if err != nil {
		s.lastError = err.Error()
	}
	s.ready = true
}

var st = &status{}

func main() {
	o := gatherOptions()
	if err := o.validate(); err != nil {
		logrus.WithError(err).Fatal("invalid options")
	}
	logger := logrus.WithField("component", "efs-cluster-writer")

	token := ""
	if o.controlToken != "" {
		data, err := os.ReadFile(o.controlToken)
		if err != nil {
			logger.WithError(err).Fatal("could not read control token")
		}
		token = string(data)
	}
	client := &efsclusterwriter.HTTPClient{
		AssignmentsURL: o.assignmentsURL,
		RescanURL:      o.rescanURL,
		Token:          token,
	}
	runner := &efsclusterwriter.Runner{
		Client: client,
		Config: efsclusterwriter.Config{
			EFSRoot: o.efsRoot, BackupDir: o.backupDir,
			MaxMoves: o.maxMoves, MaxMovePercent: o.maxMovePercent,
			DryRun: o.dryRun, Logger: logger,
		},
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// Serialize all reconcile passes: the periodic tick and HTTP triggers share
	// one worker, so a single process never runs two passes at once.
	triggerCh := make(chan struct{}, 1)
	run := func() {
		result, err := runner.Reconcile(ctx)
		if err != nil {
			logger.WithError(err).Error("reconcile failed")
		}
		st.set(result, err)
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(o.interval)
		defer ticker.Stop()
		run()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				run()
			case <-triggerCh:
				run()
			}
		}
	}()

	mux := http.NewServeMux()
	mux.HandleFunc("/reconcile", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		select {
		case triggerCh <- struct{}{}:
		default:
		}
		w.WriteHeader(http.StatusAccepted)
	})
	mux.HandleFunc("/status", func(w http.ResponseWriter, _ *http.Request) {
		st.mu.Lock()
		defer st.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"lastRun":%q,"generation":%d,"files":%d,"updated":%d,"missing":%d,"blocked":%d,"error":%q}`,
			st.lastRun.UTC().Format(time.RFC3339), st.lastResult.Generation, st.lastResult.Files,
			st.lastResult.Updated, st.lastResult.Missing, st.lastResult.Blocked, st.lastError)
	})
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		st.mu.Lock()
		defer st.mu.Unlock()
		if !st.ready {
			http.Error(w, "not reconciled yet", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	})

	srv := &http.Server{Addr: o.listenAddress, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.WithError(err).Fatal("http server failed")
		}
	}()
	logger.WithField("address", o.listenAddress).Info("efs cluster writer started")
	<-ctx.Done()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()
	_ = srv.Shutdown(shutdownCtx)
	wg.Wait()
}
