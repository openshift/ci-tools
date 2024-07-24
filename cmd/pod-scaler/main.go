package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"cloud.google.com/go/storage"
	"github.com/bombsimon/logrusr/v3"
	prometheusclient "github.com/prometheus/client_golang/api"
	prometheusapi "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/sirupsen/logrus"
	"google.golang.org/api/option"

	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/transport"
	controllerruntime "sigs.k8s.io/controller-runtime"
	prowConfig "sigs.k8s.io/prow/pkg/config"
	prowflagutil "sigs.k8s.io/prow/pkg/flagutil"
	"sigs.k8s.io/prow/pkg/interrupts"
	"sigs.k8s.io/prow/pkg/logrusutil"
	"sigs.k8s.io/prow/pkg/metrics"
	pprofutil "sigs.k8s.io/prow/pkg/pjutil/pprof"
	"sigs.k8s.io/prow/pkg/version"

	buildclientset "github.com/openshift/client-go/build/clientset/versioned/typed/build/v1"
	routeclientset "github.com/openshift/client-go/route/clientset/versioned/typed/route/v1"

	v1 "github.com/openshift/ci-tools/cmd/pod-scaler/v1"
	"github.com/openshift/ci-tools/pkg/results"
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

	resultsOptions results.Options
}

type producerOptions struct {
	kubernetesOptions prowflagutil.KubernetesOptions
	once              bool
	ignoreLatest      time.Duration
}

type consumerOptions struct {
	port   int
	uiPort int

	dataDir               string
	certDir               string
	mutateResourceLimits  bool
	cpuCap                int64
	memoryCap             string
	cpuPriorityScheduling int64
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
	fs.Int64Var(&o.cpuCap, "cpu-cap", 10, "The maximum CPU request value, ex: 10")
	fs.StringVar(&o.memoryCap, "memory-cap", "20Gi", "The maximum memory request value, ex: '20Gi'")
	fs.Int64Var(&o.cpuPriorityScheduling, "cpu-priority-scheduling", 8, "Pods with CPU requests at, or above, this value will be admitted with priority scheduling")
	o.resultsOptions.Bind(fs)
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
		if cpuCap := resource.NewQuantity(o.cpuCap, resource.DecimalSI); cpuCap.Sign() <= 0 {
			return errors.New("--cpu-cap must be greater than 0")
		}
		if memoryCap := resource.MustParse(o.memoryCap); memoryCap.Sign() <= 0 {
			return errors.New("--memory-cap must be greater than 0")
		}
		if err := o.resultsOptions.Validate(); err != nil {
			return err
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

	var cache v1.Cache
	if opts.cacheDir != "" {
		cache = &v1.LocalCache{Dir: opts.cacheDir}
	} else {
		gcsClient, err := storage.NewClient(interrupts.Context(), option.WithCredentialsFile(opts.gcsCredentialsFile))
		if err != nil {
			logrus.WithError(err).Fatal("Could not initialize GCS client.")
		}
		bucket := gcsClient.Bucket(opts.cacheBucket)
		cache = &v1.BucketCache{Bucket: bucket}
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

func mainProduce(opts *options, cache v1.Cache) {
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

	v1.Produce(clients, cache, opts.ignoreLatest, opts.once)
}

func mainUI(opts *options, cache v1.Cache) {
	go serveUI(opts.uiPort, opts.instrumentationOptions.HealthPort, opts.dataDir, loaders(cache))
}

func mainAdmission(opts *options, cache v1.Cache) {
	controllerruntime.SetLogger(logrusr.New(logrus.StandardLogger()))

	restConfig, err := util.LoadClusterConfig()
	if err != nil {
		logrus.WithError(err).Fatal("Failed to load cluster config.")
	}
	client, err := buildclientset.NewForConfig(restConfig)
	if err != nil {
		logrus.WithError(err).Fatal("Failed to construct client.")
	}
	reporter, err := opts.resultsOptions.PodScalerReporter()
	if err != nil {
		logrus.WithError(err).Fatal("Failed to create pod-scaler reporter.")
	}

	go admit(opts.port, opts.instrumentationOptions.HealthPort, opts.certDir, client, loaders(cache), opts.mutateResourceLimits, opts.cpuCap, opts.memoryCap, opts.cpuPriorityScheduling, reporter)
}

func loaders(cache v1.Cache) map[string][]*cacheReloader {
	l := map[string][]*cacheReloader{}
	for _, prefix := range []string{v1.ProwjobsCachePrefix, v1.PodsCachePrefix, v1.StepsCachePrefix} {
		l[v1.MetricNameCPUUsage] = append(l[v1.MetricNameCPUUsage], newReloader(prefix+"/"+v1.MetricNameCPUUsage, cache))
		l[v1.MetricNameMemoryWorkingSet] = append(l[v1.MetricNameMemoryWorkingSet], newReloader(prefix+"/"+v1.MetricNameMemoryWorkingSet, cache))
	}
	return l
}
