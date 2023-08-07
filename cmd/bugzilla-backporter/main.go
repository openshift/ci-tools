package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/sirupsen/logrus"

	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/test-infra/pkg/flagutil"
	prowConfig "k8s.io/test-infra/prow/config"
	"k8s.io/test-infra/prow/config/secret"
	prowflagutil "k8s.io/test-infra/prow/flagutil"
	"k8s.io/test-infra/prow/interrupts"
	"k8s.io/test-infra/prow/metrics"
	"k8s.io/test-infra/prow/pjutil"
	"k8s.io/test-infra/prow/plugins"
	"k8s.io/test-infra/prow/simplifypath"
	"sigs.k8s.io/yaml"

	"github.com/openshift/ci-tools/pkg/backporter"
	"github.com/openshift/ci-tools/pkg/util/gzip"
)

var (
	bzbpMetrics = metrics.NewMetrics("bugzillabackporter")
)

type options struct {
	logLevel     string
	address      string
	gracePeriod  time.Duration
	bugzilla     prowflagutil.BugzillaOptions
	pluginConfig string
}

func gatherOptions() (options, error) {
	o := options{}
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	fs.StringVar(&o.logLevel, "log-level", "info", "Level at which to log output.")
	fs.StringVar(&o.address, "address", ":8080", "Address to run server on")
	fs.DurationVar(&o.gracePeriod, "gracePeriod", time.Second*10, "Grace period for server shutdown")
	fs.StringVar(&o.pluginConfig, "plugin-config", "/etc/plugins/plugins.yaml", "Path to plugin config file.")

	for _, group := range []flagutil.OptionGroup{&o.bugzilla} {
		group.AddFlags(fs)
	}
	err := fs.Parse(os.Args[1:])
	if err != nil {
		return o, err
	}
	return o, nil
}

func getAllTargetVersions(configFile string) ([]string, error) {
	// Get the versions from the plugins.yaml file to populate the Target Versions dropdown
	// for the CreateClone functionality
	b, err := gzip.ReadFileMaybeGZIP(configFile)
	if err != nil {
		return nil, err
	}
	np := &plugins.Configuration{}
	if err := yaml.Unmarshal(b, np); err != nil {
		return nil, fmt.Errorf("failed to unmarshal %s : %w", configFile, err)
	}

	if err := np.Validate(); err != nil {
		return nil, fmt.Errorf("failed to validate file %s : %w", configFile, err)
	}
	allTargetVersionsSet := sets.New[string]()
	// Hardcoding with just the "openshift" org here
	// Could be extended to be configurable in the future to support multiple products
	// In which case this would have to be moved to the CreateClones function.
	options := np.Bugzilla.OptionsForRepo("openshift", "")
	for _, val := range options {
		if val.TargetRelease != nil {
			allTargetVersionsSet.Insert(*val.TargetRelease)
		}
	}
	allTargetVersions := sets.List(allTargetVersionsSet)
	err = backporter.SortTargetReleases(allTargetVersions, true)
	if err != nil {
		return nil, fmt.Errorf("unable to sort discovered target_releases %v: %w", allTargetVersions, err)
	}
	return allTargetVersions, nil
}

func processOptions(o options) error {
	level, err := logrus.ParseLevel(o.logLevel)
	if err != nil {
		return fmt.Errorf("invalid --log-level '%s': %w", o.logLevel, err)
	}
	logrus.SetLevel(level)
	return nil
}

// l and v keep the simplifier tree legible
func l(fragment string, children ...simplifypath.Node) simplifypath.Node {
	return simplifypath.L(fragment, children...)
}

func v(fragment string, children ...simplifypath.Node) simplifypath.Node {
	return simplifypath.V(fragment, children...)
}

func main() {
	o, err := gatherOptions()
	if err != nil {
		logrus.Fatalf("invalid options: %v", err)
	}
	err = processOptions(o)
	if err != nil {
		logrus.Fatalf("invalid options: %v", err)
	}

	// Start the bugzilla secrets agent
	if err := secret.Add(o.bugzilla.ApiKeyPath); err != nil {
		logrus.WithError(err).Fatal("Error starting secrets agent.")
	}
	bugzillaClient, err := o.bugzilla.BugzillaClient()
	if err != nil {
		logrus.WithError(err).Fatal("Error getting Bugzilla client.")
	}
	bugzillaClient.SetRoundTripper(backporter.NewCachingTransport())
	health := pjutil.NewHealth()
	metrics.ExposeMetrics("ci-operator-bugzilla-backporter", prowConfig.PushGateway{}, prowflagutil.DefaultMetricsPort)
	allTargetVersions, err := getAllTargetVersions(o.pluginConfig)
	if err != nil {
		logrus.WithError(err).Fatal("Error parsing plugins configuration.")
	}
	simplifier := simplifypath.NewSimplifier(l("", // shadow element mimicing the root
		l(""),
		l("clones",
			v("ID"),
			l("create"),
		),
		l("bug"),
	))
	handler := metrics.TraceHandler(simplifier, bzbpMetrics.HTTPRequestDuration, bzbpMetrics.HTTPResponseSize)
	http.HandleFunc("/", handler(backporter.GetLandingHandler(bzbpMetrics)).ServeHTTP)
	http.HandleFunc("/clones", handler(backporter.GetClonesHandler(bugzillaClient, allTargetVersions, bzbpMetrics)).ServeHTTP)
	http.HandleFunc("/clones/create", handler(backporter.CreateCloneHandler(bugzillaClient, allTargetVersions, bzbpMetrics)).ServeHTTP)
	// Leaving this in here to help with future debugging. This will return bug details in JSON format
	http.HandleFunc("/help", handler(backporter.GetHelpHandler(bzbpMetrics)).ServeHTTP)
	http.HandleFunc("/bug", handler(backporter.GetBugHandler(bugzillaClient, bzbpMetrics)).ServeHTTP)
	interrupts.ListenAndServe(&http.Server{Addr: o.address}, o.gracePeriod)

	health.ServeReady()
	interrupts.WaitForGracefulShutdown()
}
