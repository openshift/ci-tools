package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"cloud.google.com/go/storage"
	"github.com/bombsimon/logrusr"
	prometheusclient "github.com/prometheus/client_golang/api"
	prometheusapi "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/sirupsen/logrus"
	"google.golang.org/api/option"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/transport"
	prowConfig "k8s.io/test-infra/prow/config"
	prowflagutil "k8s.io/test-infra/prow/flagutil"
	"k8s.io/test-infra/prow/interrupts"
	"k8s.io/test-infra/prow/logrusutil"
	"k8s.io/test-infra/prow/metrics"
	pprofutil "k8s.io/test-infra/prow/pjutil/pprof"
	"k8s.io/test-infra/prow/version"
	controllerruntime "sigs.k8s.io/controller-runtime"

	buildclientset "github.com/openshift/client-go/build/clientset/versioned/typed/build/v1"
	routeclientset "github.com/openshift/client-go/route/clientset/versioned/typed/route/v1"

	"github.com/openshift/ci-tools/pkg/util"
)

type options struct {
	mode string
	producerOptions
	consumerOptions

	instrumentationOptions prowflagutil.InstrumentationOptions

	loglevel string
	logStyle string

	cacheDir           string
	cacheBucket        string
	gcsCredentialsFile string
}

type producerOptions struct {
	kubernetesOptions prowflagutil.KubernetesOptions
	once              bool
	ignoreLatest      time.Duration
}

type consumerOptions struct {
	port   int
	uiPort int

	dataDir              string
	certDir              string
	mutateResourceLimits bool
}

func bindOptions(fs *flag.FlagSet) *options {
	o := options{producerOptions: producerOptions{kubernetesOptions: prowflagutil.KubernetesOptions{NOInClusterConfigDefault: true}}}
	o.instrumentationOptions.AddFlags(fs)
	fs.StringVar(&o.mode, "mode", "", "Which mode to run in.")
	o.producerOptions.kubernetesOptions.AddFlags(fs)
	fs.DurationVar(&o.ignoreLatest, "ignore-latest", 0, "Duration of latest time series to ignore when querying Prometheus. For instance, 1h will ignore the latest hour of data.")
	fs.BoolVar(&o.once, "produce-once", false, "Query Prometheus and refresh cached data only once before exiting.")
	fs.IntVar(&o.port, "port", 0, "Port to serve admission webhooks on.")
	fs.IntVar(&o.uiPort, "ui-port", 0, "Port to serve frontend on.")
	fs.StringVar(&o.certDir, "serving-cert-dir", "", "Path to directory with serving certificate and key for the admission webhook server.")
	fs.BoolVar(&o.mutateResourceLimits, "mutate-resource-limits", false, "Enable resource limit mutation in the admission webhook.")
	fs.StringVar(&o.loglevel, "loglevel", "debug", "Logging level.")
	fs.StringVar(&o.logStyle, "log-style", "json", "Logging style: json or text.")
	fs.StringVar(&o.cacheDir, "cache-dir", "", "Local directory holding cache data (for development mode).")
	fs.StringVar(&o.dataDir, "data-dir", "", "Local directory to cache UI data into.")
	fs.StringVar(&o.cacheBucket, "cache-bucket", "", "GCS bucket name holding cached Prometheus data.")
	fs.StringVar(&o.gcsCredentialsFile, "gcs-credentials-file", "", "File where GCS credentials are stored.")
	return &o
}

const (
	logStyleJson = "json"
	logStyleText = "text"
)

func (o *options) validate() error {
	switch o.mode {
	case "producer":
		return o.kubernetesOptions.Validate(false)
	case "consumer.ui":
		if o.uiPort == 0 {
			return errors.New("--ui-port is required")
		}
		if o.dataDir == "" {
			return errors.New("--data-dir is required")
		}
	case "consumer.admission":
		if o.port == 0 {
			return errors.New("--port is required")
		}
		if o.certDir == "" {
			return errors.New("--serving-cert-dir is required")
		}
	default:
		return errors.New("--mode must be either \"producer\", \"consumer.ui\", or \"consumer.admission\"")
	}
	if o.cacheDir == "" {
		if o.cacheBucket == "" {
			return errors.New("--cache-bucket is required")
		}
		if o.gcsCredentialsFile == "" {
			return errors.New("--gcs-credentials-file is required")
		}
	}
	if level, err := logrus.ParseLevel(o.loglevel); err != nil {
		return fmt.Errorf("--loglevel invalid: %w", err)
	} else {
		logrus.SetLevel(level)
	}
	if o.logStyle != logStyleJson && o.logStyle != logStyleText {
		return fmt.Errorf("--log-style must be one of %s or %s, not %s", logStyleText, logStyleJson, o.logStyle)
	}

	return o.instrumentationOptions.Validate(false)
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
	switch opts.logStyle {
	case logStyleJson:
		logrusutil.ComponentInit()
	case logStyleText:
		logrus.SetFormatter(&logrus.TextFormatter{
			ForceColors:     true,
			DisableQuote:    true,
			FullTimestamp:   true,
			TimestampFormat: time.RFC3339,
		})
	}
	logrus.Infof("%s version %s", version.Name, version.Version)

	pprofutil.Instrument(opts.instrumentationOptions)
	metrics.ExposeMetrics("pod-scaler", prowConfig.PushGateway{}, opts.instrumentationOptions.MetricsPort)

	var cache cache
	if opts.cacheDir != "" {
		cache = &localCache{dir: opts.cacheDir}
	} else {
		gcsClient, err := storage.NewClient(interrupts.Context(), option.WithCredentialsFile(opts.gcsCredentialsFile))
		if err != nil {
			logrus.WithError(err).Fatal("Could not initialize GCS client.")
		}
		bucket := gcsClient.Bucket(opts.cacheBucket)
		cache = &bucketCache{bucket: bucket}
	}

	switch opts.mode {
	case "producer":
		mainProduce(opts, cache)
	case "consumer.ui":
		mainUI(opts, cache)
	case "consumer.admission":
		mainAdmission(opts, cache)
	}
	if !opts.once {
		interrupts.WaitForGracefulShutdown()
	}
}

func mainProduce(opts *options, cache cache) {
	kubeconfigChangedCallBack := func() {
		logrus.Fatal("Kubeconfig changed, exiting to get restarted by Kubelet and pick up the changes")
	}

	kubeconfigs, err := opts.kubernetesOptions.LoadClusterConfigs(kubeconfigChangedCallBack)
	if err != nil {
		logrus.WithError(err).Fatal("Failed to load kubeconfigs")
	}

	clients := map[string]prometheusapi.API{}
	for cluster, config := range kubeconfigs {
		logger := logrus.WithField("cluster", cluster)
		client, err := routeclientset.NewForConfig(&config)
		if err != nil {
			logger.WithError(err).Fatal("Failed to construct client.")
		}
		route, err := client.Routes("openshift-monitoring").Get(interrupts.Context(), "prometheus-k8s", metav1.GetOptions{})
		if err != nil {
			logger.WithError(err).Fatal("Failed to get Prometheus route.")
		}
		var addr string
		if route.Spec.TLS != nil {
			addr = "https://" + route.Spec.Host
		} else {
			addr = "http://" + route.Spec.Host
		}
		promClient, err := prometheusclient.NewClient(prometheusclient.Config{
			Address:      addr,
			RoundTripper: transport.NewBearerAuthRoundTripper(config.BearerToken, prometheusclient.DefaultRoundTripper),
		})
		if err != nil {
			logger.WithError(err).Fatal("Failed to get Prometheus client.")
		}
		clients[cluster] = prometheusapi.NewAPI(promClient)
		logger.Debugf("Loaded Prometheus client.")
	}

	produce(clients, cache, opts.ignoreLatest, opts.once)
}

func mainUI(opts *options, cache cache) {
	go serveUI(opts.uiPort, opts.instrumentationOptions.HealthPort, opts.dataDir, loaders(cache))
}

func mainAdmission(opts *options, cache cache) {
	controllerruntime.SetLogger(logrusr.NewLogger(logrus.StandardLogger()))

	restConfig, err := util.LoadClusterConfig()
	if err != nil {
		logrus.WithError(err).Fatal("Failed to load cluster config.")
	}
	client, err := buildclientset.NewForConfig(restConfig)
	if err != nil {
		logrus.WithError(err).Fatal("Failed to construct client.")
	}

	go admit(opts.port, opts.instrumentationOptions.HealthPort, opts.certDir, client, loaders(cache), opts.mutateResourceLimits)
}

func loaders(cache cache) map[string][]*cacheReloader {
	l := map[string][]*cacheReloader{}
	for _, prefix := range []string{prowjobsCachePrefix, podsCachePrefix, stepsCachePrefix} {
		l[MetricNameCPUUsage] = append(l[MetricNameCPUUsage], newReloader(prefix+"/"+MetricNameCPUUsage, cache))
		l[MetricNameMemoryWorkingSet] = append(l[MetricNameMemoryWorkingSet], newReloader(prefix+"/"+MetricNameMemoryWorkingSet, cache))
	}
	return l
}
