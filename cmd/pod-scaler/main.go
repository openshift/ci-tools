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
	"k8s.io/client-go/kubernetes"
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

	"github.com/openshift/ci-tools/pkg/prowconfigutils"
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

	failureEscalationFactor   float64
	failureEscalationMaxLevel int
	cpuThrottleThreshold      float64

	resultsOptions results.Options
}

type producerOptions struct {
	kubernetesOptions prowflagutil.KubernetesOptions
	once              bool
	ignoreLatest      time.Duration
	maxDataAge        time.Duration
}

type consumerOptions struct {
	port   int
	uiPort int

	dataDir                                       string
	certDir                                       string
	mutateResourceLimits                          bool
	cpuCap                                        int64
	memoryCap                                     string
	cpuPriorityScheduling                         int64
	percentageMeasured                            float64
	measuredPodCPUIncrease                        float64
	systemReservedCPU                             int64
	authoritativeCPU                              bool
	authoritativeMemory                           bool
	authoritativeCPURequest                       bool
	authoritativeCPULimit                         bool
	authoritativeMemoryRequest                    bool
	authoritativeMemoryLimit                      bool
	authoritativeCPURequestMaxReductionPercent    float64
	authoritativeCPULimitMaxReductionPercent      float64
	authoritativeMemoryRequestMaxReductionPercent float64
	authoritativeMemoryLimitMaxReductionPercent   float64
	authoritativeDecreaseUsageBasis               string
	skipWorkloadTypeRequestDecrease               string
	skipWorkloadTypeLimitDecrease                 string
	skipWorkloadClassRequestDecrease              string
	skipWorkloadClassLimitDecrease                string
}

func (o *consumerOptions) authoritativeConfig() authoritativeConfig {
	pair := func(apply bool, maxReduction float64, legacyApply bool) authoritativePair {
		return authoritativePair{apply: apply || legacyApply, maxReduction: maxReduction}
	}
	return authoritativeConfig{
		cpuRequest:    pair(o.authoritativeCPURequest, o.authoritativeCPURequestMaxReductionPercent, o.authoritativeCPU),
		cpuLimit:      pair(o.authoritativeCPULimit, o.authoritativeCPULimitMaxReductionPercent, o.authoritativeCPU),
		memoryRequest: pair(o.authoritativeMemoryRequest, o.authoritativeMemoryRequestMaxReductionPercent, o.authoritativeMemory),
		memoryLimit:   pair(o.authoritativeMemoryLimit, o.authoritativeMemoryLimitMaxReductionPercent, o.authoritativeMemory),
	}
}

func (o *consumerOptions) authoritativeSkipConfig() authoritativeSkipConfig {
	return parseAuthoritativeSkipConfig(
		o.skipWorkloadTypeLimitDecrease,
		o.skipWorkloadClassLimitDecrease,
		o.skipWorkloadTypeRequestDecrease,
		o.skipWorkloadClassRequestDecrease,
	)
}

func bindOptions(fs *flag.FlagSet) *options {
	o := options{producerOptions: producerOptions{kubernetesOptions: prowflagutil.KubernetesOptions{NOInClusterConfigDefault: true}}}
	o.instrumentationOptions.AddFlags(fs)
	fs.StringVar(&o.mode, "mode", "", "Which mode to run in.")
	o.producerOptions.kubernetesOptions.AddFlags(fs)
	fs.DurationVar(&o.ignoreLatest, "ignore-latest", 0, "Duration of latest time series to ignore when querying Prometheus. For instance, 1h will ignore the latest hour of data.")
	fs.DurationVar(&o.maxDataAge, "max-data-age", 90*24*time.Hour, "Maximum age of data to retain and query. Caps the Prometheus query range and prunes older cached data.")
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
	fs.Float64Var(&o.percentageMeasured, "percentage-measured", 0, "Percentage of pods to mark as measured (0-100). Measured pods get increased CPU requests and anti-affinity rules.")
	fs.Float64Var(&o.measuredPodCPUIncrease, "measured-pod-cpu-increase", 50, "Percentage increase in CPU requests for measured pods (default: 50%).")
	fs.Int64Var(&o.systemReservedCPU, "system-reserved-cpu", 2, "CPU cores to reserve for system overhead when capping measured pod CPU.")
	fs.BoolVar(&o.authoritativeCPURequest, "authoritative-cpu-request", false, "When true, apply CPU request decreases from measured usage. When false, log would-be decreases without mutating (dry-run).")
	fs.BoolVar(&o.authoritativeCPULimit, "authoritative-cpu-limit", false, "When true, apply CPU limit decreases from measured usage. When false, log would-be decreases without mutating (dry-run).")
	fs.BoolVar(&o.authoritativeCPU, "authoritative-cpu", false, "Deprecated: enables both --authoritative-cpu-request and --authoritative-cpu-limit.")
	fs.BoolVar(&o.authoritativeMemoryRequest, "authoritative-memory-request", false, "When true, apply memory request decreases from measured usage. When false, log would-be decreases without mutating (dry-run).")
	fs.BoolVar(&o.authoritativeMemoryLimit, "authoritative-memory-limit", false, "When true, apply memory limit decreases from measured usage. When false, log would-be decreases without mutating (dry-run).")
	fs.BoolVar(&o.authoritativeMemory, "authoritative-memory", false, "Deprecated: enables both --authoritative-memory-request and --authoritative-memory-limit.")
	fs.Float64Var(&o.authoritativeCPURequestMaxReductionPercent, "authoritative-cpu-request-max-reduction-percent", 1.0, "Maximum CPU request reduction per admission in authoritative mode, as a fraction (0.25 = 25%, 1.0 = no cap).")
	fs.Float64Var(&o.authoritativeCPULimitMaxReductionPercent, "authoritative-cpu-limit-max-reduction-percent", 1.0, "Maximum CPU limit reduction per admission in authoritative mode, as a fraction (0.25 = 25%, 1.0 = no cap).")
	fs.Float64Var(&o.authoritativeCPULimitMaxReductionPercent, "authoritative-cpu-max-reduction-percent", 1.0, "Deprecated: use --authoritative-cpu-limit-max-reduction-percent.")
	fs.Float64Var(&o.authoritativeMemoryRequestMaxReductionPercent, "authoritative-memory-request-max-reduction-percent", 1.0, "Maximum memory request reduction per admission in authoritative mode, as a fraction (0.25 = 25%, 1.0 = no cap).")
	fs.Float64Var(&o.authoritativeMemoryLimitMaxReductionPercent, "authoritative-memory-limit-max-reduction-percent", 1.0, "Maximum memory limit reduction per admission in authoritative mode, as a fraction (0.25 = 25%, 1.0 = no cap).")
	fs.Float64Var(&o.authoritativeMemoryLimitMaxReductionPercent, "authoritative-memory-max-reduction-percent", 1.0, "Deprecated: use --authoritative-memory-limit-max-reduction-percent.")
	fs.StringVar(&o.authoritativeDecreaseUsageBasis, "pod-scaler-authoritative-decrease-usage-basis", "p80", "Usage basis for authoritative decreases before the 1.2x multiplier: p80 (default) or peak (histogram max/burst).")
	fs.StringVar(&o.skipWorkloadTypeLimitDecrease, "pod-scaler-skip-workload-type-limit-decrease", "", "Comma-separated workload types that skip authoritative limit decreases (e.g. build).")
	fs.StringVar(&o.skipWorkloadClassLimitDecrease, "pod-scaler-skip-workload-class-limit-decrease", "", "Comma-separated ci-workload classes that skip authoritative limit decreases (e.g. builds,tests).")
	fs.StringVar(&o.skipWorkloadTypeRequestDecrease, "pod-scaler-skip-workload-type-request-decrease", "", "Comma-separated workload types that skip authoritative request decreases.")
	fs.StringVar(&o.skipWorkloadClassRequestDecrease, "pod-scaler-skip-workload-class-request-decrease", "", "Comma-separated ci-workload classes that skip authoritative request decreases.")
	fs.Float64Var(&o.failureEscalationFactor, "failure-escalation-factor", 1.5, "Multiplier applied per escalation level after OOM or CPU throttle (1.5 = 50% increase per level).")
	fs.IntVar(&o.failureEscalationMaxLevel, "failure-escalation-max-level", 10, "Maximum escalation level tracked for a workload.")
	fs.Float64Var(&o.cpuThrottleThreshold, "cpu-throttle-threshold", 0.25, "Minimum throttled/total CPU CFS period ratio to count as CPU deprived.")
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
		if o.percentageMeasured < 0 || o.percentageMeasured > 100 {
			return errors.New("--percentage-measured must be between 0 and 100")
		}
		if o.measuredPodCPUIncrease < 0 {
			return errors.New("--measured-pod-cpu-increase must be >= 0")
		}
		if o.authoritativeCPURequestMaxReductionPercent < 0 || o.authoritativeCPURequestMaxReductionPercent > 1 {
			return errors.New("--authoritative-cpu-request-max-reduction-percent must be between 0 and 1")
		}
		if o.authoritativeCPULimitMaxReductionPercent < 0 || o.authoritativeCPULimitMaxReductionPercent > 1 {
			return errors.New("--authoritative-cpu-limit-max-reduction-percent must be between 0 and 1")
		}
		if o.authoritativeMemoryRequestMaxReductionPercent < 0 || o.authoritativeMemoryRequestMaxReductionPercent > 1 {
			return errors.New("--authoritative-memory-request-max-reduction-percent must be between 0 and 1")
		}
		if o.authoritativeMemoryLimitMaxReductionPercent < 0 || o.authoritativeMemoryLimitMaxReductionPercent > 1 {
			return errors.New("--authoritative-memory-limit-max-reduction-percent must be between 0 and 1")
		}
		if _, err := parseAuthoritativeDecreaseUsageBasis(o.authoritativeDecreaseUsageBasis); err != nil {
			return err
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
	if o.failureEscalationFactor <= 1 {
		return errors.New("--failure-escalation-factor must be greater than 1")
	}
	if o.failureEscalationMaxLevel < 0 {
		return errors.New("--failure-escalation-max-level must be >= 0")
	}
	if o.cpuThrottleThreshold <= 0 || o.cpuThrottleThreshold > 1 {
		return errors.New("--cpu-throttle-threshold must be between 0 and 1")
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

	var cache Cache
	if opts.cacheDir != "" {
		cache = &LocalCache{Dir: opts.cacheDir}
	} else {
		gcsClient, err := storage.NewClient(interrupts.Context(), option.WithCredentialsFile(opts.gcsCredentialsFile))
		if err != nil {
			logrus.WithError(err).Fatal("Could not initialize GCS client.")
		}
		bucket := gcsClient.Bucket(opts.cacheBucket)
		cache = &BucketCache{Bucket: bucket}
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

func mainProduce(opts *options, cache Cache) {
	kubeconfigChangedCallBack := func() {
		logrus.Fatal("Kubeconfig changed, exiting to get restarted by Kubelet and pick up the changes")
	}

	_, err := prowconfigutils.ProwDisabledClusters(&opts.kubernetesOptions)
	if err != nil {
		logrus.WithError(err).Warn("Failed to get Prow disable clusters")
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
			logger.WithError(err).Error("Failed to construct client, skipping cluster.")
			continue
		}
		route, err := client.Routes("openshift-monitoring").Get(interrupts.Context(), "prometheus-k8s", metav1.GetOptions{})
		if err != nil {
			logger.WithError(err).Error("Failed to get Prometheus route, skipping cluster.")
			continue
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
			logger.WithError(err).Error("Failed to get Prometheus client, skipping cluster.")
			continue
		}
		clients[cluster] = prometheusapi.NewAPI(promClient)
		logger.Debugf("Loaded Prometheus client.")
	}

	produce(clients, cache, opts.ignoreLatest, opts.maxDataAge, opts.once, opts.failureEscalationMaxLevel, opts.cpuThrottleThreshold)

}

func mainUI(opts *options, cache Cache) {
	go serveUI(opts.uiPort, opts.instrumentationOptions.HealthPort, opts.dataDir, loaders(cache))
}

func mainAdmission(opts *options, cache Cache) {
	controllerruntime.SetLogger(logrusr.New(logrus.StandardLogger()))

	restConfig, err := util.LoadClusterConfig()
	if err != nil {
		logrus.WithError(err).Fatal("Failed to load cluster config.")
	}
	client, err := buildclientset.NewForConfig(restConfig)
	if err != nil {
		logrus.WithError(err).Fatal("Failed to construct client.")
	}
	kubeClient, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		logrus.WithError(err).Fatal("Failed to create Kubernetes client.")
	}
	reporter, err := opts.resultsOptions.PodScalerReporter()
	if err != nil {
		logrus.WithError(err).Fatal("Failed to create pod-scaler reporter.")
	}

	escalations := newEscalationServer(cache, opts.failureEscalationFactor)

	usageBasis, err := parseAuthoritativeDecreaseUsageBasis(opts.authoritativeDecreaseUsageBasis)
	if err != nil {
		logrus.WithError(err).Fatal("Failed to parse authoritative decrease usage basis.")
	}

	go admit(opts.port, opts.instrumentationOptions.HealthPort, opts.certDir, client, kubeClient, loaders(cache), opts.mutateResourceLimits, opts.cpuCap, opts.memoryCap, opts.cpuPriorityScheduling, opts.percentageMeasured, opts.measuredPodCPUIncrease, opts.systemReservedCPU, opts.authoritativeConfig(), usageBasis, opts.authoritativeSkipConfig(), escalations, reporter)
}

func loaders(cache Cache) map[string][]*cacheReloader {
	l := map[string][]*cacheReloader{}
	for _, prefix := range []string{ProwjobsCachePrefix, PodsCachePrefix, StepsCachePrefix} {
		l[MetricNameCPUUsage] = append(l[MetricNameCPUUsage], newReloader(prefix+"/"+MetricNameCPUUsage, cache))
		l[MetricNameMemoryWorkingSet] = append(l[MetricNameMemoryWorkingSet], newReloader(prefix+"/"+MetricNameMemoryWorkingSet, cache))
	}
	return l
}
