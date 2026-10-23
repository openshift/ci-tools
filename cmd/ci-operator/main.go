package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"time"

	"github.com/bombsimon/logrusr/v3"
	"github.com/go-logr/logr"
	egressfirewallv1 "github.com/ovn-org/ovn-kubernetes/go-controller/pkg/crd/egressfirewall/v1"
	"github.com/sirupsen/logrus"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/klog/v2"
	controllerruntime "sigs.k8s.io/controller-runtime"
	ctrlruntimelog "sigs.k8s.io/controller-runtime/pkg/log"
	_ "sigs.k8s.io/prow/pkg/cache"
	"sigs.k8s.io/prow/pkg/logrusutil"
	"sigs.k8s.io/prow/pkg/version"
	csiapi "sigs.k8s.io/secrets-store-csi-driver/apis/v1"

	buildv1 "github.com/openshift/api/build/v1"
	configv1 "github.com/openshift/api/config/v1"
	imageapi "github.com/openshift/api/image/v1"
	routev1 "github.com/openshift/api/route/v1"
	hivev1 "github.com/openshift/hive/apis/hive/v1"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/metrics"
	"github.com/openshift/ci-tools/pkg/results"
	"github.com/openshift/ci-tools/pkg/secrets"
)

const usage = `Orchestrate multi-stage image-based builds

The ci-operator reads a declarative configuration YAML file and executes a set of build
steps on an OpenShift cluster for image-based components. By default, all steps are run,
but a caller may select one or more targets (image names or test names) to limit to only
steps that those targets depend on. The build creates a new project to run the builds in
and can automatically clean up the project when the build completes.

ci-operator leverages declarative OpenShift builds and images to reuse previously compiled
artifacts. It makes building multiple images that share one or more common base layers
simple as well as running tests that depend on those images.

Since the command is intended for use in CI environments it requires an input environment
variable called the JOB_SPEC that defines the GitHub project to execute and the commit,
branch, and any PRs to merge onto the branch. See the kubernetes/test-infra project for
a description of JOB_SPEC.

The inputs of the build (source code, tagged images, configuration) are combined to form
a consistent name for the target namespace that will change if any of the inputs change.
This allows multiple test jobs to share common artifacts and still perform retries.

The standard build steps are designed for simple command-line actions (like invoking
"make test").

Artifacts can be gathered from test pods. Set --artifact-dir to define the top level
artifact directory, and any test task that defines artifact_dir will have artifacts
extracted after the container has completed. Errors in artifact extraction will not
cause build failures.

In CI environments the inputs to a job may be different than what a normal
development workflow would use. The --override file will override fields
defined in the config file, such as base images and the release tag configuration.

After a successful build the --promote will tag each built image (in "images")
to the image stream(s) identified by the "promotion" config. You may add
additional images to promote and their target names via the "additional_images"
map.
`

const (
	leaseAcquireTimeout = 120 * time.Minute
)

var (
	// leaseServerAddress is the default lease server in app.ci
	leaseServerAddress = api.URLForService(api.ServiceBoskos)
	// configResolverAddress is the default configresolver address in app.ci
	configResolverAddress = api.URLForService(api.ServiceConfig)
)

func main() {
	censor, closer, err := setupLogger()
	if err != nil {
		logrus.WithError(err).Fatal("Could not set up logging.")
	}
	if closer != nil {
		defer func() {
			if err := closer.Close(); err != nil {
				logrus.WithError(err).Warn("Could not close ci-operator log file.")
			}
		}()
	}
	// "i just don't want spam"
	klog.LogToStderr(false)
	logrus.Infof("%s version %s", version.Name, version.Version)
	flagSet := flag.NewFlagSet("", flag.ExitOnError)
	opt := bindOptions(flagSet)
	opt.censor = censor
	if err := flagSet.Parse(os.Args[1:]); err != nil {
		logrus.WithError(err).Fatal("failed to parse flags")
	}

	ctrlruntimelog.SetLogger(logr.New(ctrlruntimelog.NullLogSink{}))
	if opt.verbose {
		fs := flag.NewFlagSet("", flag.ExitOnError)
		klog.InitFlags(fs)
		if err := fs.Set("alsologtostderr", "true"); err != nil {
			logrus.WithError(err).Fatal("could not set klog alsologtostderr")
		}
		if err := fs.Set("v", "10"); err != nil {
			logrus.WithError(err).Fatal("could not set klog v")
		}
		if err := fs.Parse([]string{}); err != nil {
			logrus.WithError(err).Fatal("failed to parse klog flags")
		}

		logrus.SetLevel(logrus.TraceLevel)
		logrus.SetFormatter(&logrus.JSONFormatter{})
		logrus.SetReportCaller(true)
		controllerruntime.SetLogger(logrusr.New(logrus.StandardLogger()))
	}
	if opt.help {
		fmt.Print(usage)
		flagSet.SetOutput(os.Stdout)
		flagSet.Usage()
		os.Exit(0)
	}
	flagSet.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "delete-when-idle":
			opt.idleCleanupDurationSet = true
		case "delete-after":
			opt.cleanupDurationSet = true
		}
	})
	if err := addSchemes(); err != nil {
		logrus.WithError(err).Fatal("failed to set up scheme")
	}

	rand.Seed(time.Now().UnixNano())

	if err := opt.Complete(); err != nil {
		logrus.WithError(err).Error("Failed to load arguments.")
		opt.Report(results.ForReason("loading_args").ForError(err))
		os.Exit(1)
	}

	ctx := context.TODO()
	opt.metricsAgent, err = metrics.NewMetricsAgent(ctx, opt.clusterConfig, opt.censor)
	if err != nil {
		logrus.WithError(err).Error("Failed to create metrics agent...Skipping metrics.")
	} else {
		go opt.metricsAgent.Run()
	}

	opt.metricsAgent.Record(metrics.NewInsightsEvent(metrics.InsightStarted, metrics.Context{"job_spec": opt.jobSpec.MetricsData()}))
	if errs := opt.Run(); len(errs) > 0 {
		var defaulted []error
		for _, err := range errs {
			defaulted = append(defaulted, results.DefaultReason(err))
		}

		message := bytes.Buffer{}
		for _, err := range errs {
			message.WriteString(fmt.Sprintf("\n  * %s", err.Error()))
		}
		logrus.Error("Some steps failed:")
		logrus.Error(message.String())

		opt.Report(defaulted...)

		os.Exit(1)
	}

	opt.Report()
}

func setupLogger() (*secrets.DynamicCensor, io.Closer, error) {
	logrus.SetLevel(logrus.TraceLevel)
	censor := secrets.NewDynamicCensor()
	logrus.SetFormatter(logrusutil.NewFormatterWithCensor(logrus.StandardLogger().Formatter, &censor))
	logrus.SetOutput(io.Discard)
	logrus.AddHook(&formattingHook{
		formatter: logrusutil.NewFormatterWithCensor(&logrus.TextFormatter{
			ForceColors:     true,
			DisableQuote:    true,
			FullTimestamp:   true,
			TimestampFormat: time.RFC3339,
		}, &censor),
		writer: os.Stdout,
		logLevels: []logrus.Level{
			logrus.InfoLevel,
			logrus.WarnLevel,
			logrus.ErrorLevel,
			logrus.FatalLevel,
			logrus.PanicLevel,
		},
	})
	artifactDir, set := api.Artifacts()
	if !set {
		return &censor, nil, nil
	}
	if err := os.MkdirAll(artifactDir, 0777); err != nil {
		return nil, nil, err
	}
	verboseFile, err := os.Create(filepath.Join(artifactDir, "ci-operator.log"))
	if err != nil {
		return nil, nil, err
	}
	logrus.AddHook(&formattingHook{
		formatter: logrusutil.NewFormatterWithCensor(&logrus.JSONFormatter{}, &censor),
		writer:    verboseFile,
		logLevels: logrus.AllLevels,
	})
	return &censor, verboseFile, nil
}

type formattingHook struct {
	formatter logrus.Formatter
	writer    io.Writer
	logLevels []logrus.Level
}

func (hook *formattingHook) Fire(entry *logrus.Entry) error {
	line, err := hook.formatter.Format(entry)
	if err != nil {
		return err
	}
	_, err = hook.writer.Write(line)
	return err
}

func (hook *formattingHook) Levels() []logrus.Level {
	return hook.logLevels
}

func addSchemes() error {
	if err := imageapi.AddToScheme(scheme.Scheme); err != nil {
		return fmt.Errorf("failed to add imagev1 to scheme: %w", err)
	}
	if err := routev1.AddToScheme(scheme.Scheme); err != nil {
		return fmt.Errorf("failed to add routev1 to scheme: %w", err)
	}
	if err := appsv1.AddToScheme(scheme.Scheme); err != nil {
		return fmt.Errorf("failed to add appsv1 to scheme: %w", err)
	}
	if err := buildv1.AddToScheme(scheme.Scheme); err != nil {
		return fmt.Errorf("failed to add buildv1 to scheme: %w", err)
	}
	if err := hivev1.AddToScheme(scheme.Scheme); err != nil {
		return fmt.Errorf("failed to add hivev1 to scheme: %w", err)
	}
	if err := egressfirewallv1.AddToScheme(scheme.Scheme); err != nil {
		return fmt.Errorf("failed to add egressfirewallv1 to scheme: %w", err)
	}
	if err := csiapi.AddToScheme(scheme.Scheme); err != nil {
		return fmt.Errorf("failed to add secrets-store-csi-driver to scheme: %w", err)
	}
	if err := configv1.AddToScheme(scheme.Scheme); err != nil {
		return fmt.Errorf("failed to add configv1 to scheme: %w", err)
	}

	return nil
}
