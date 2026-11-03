package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	secretmanager "cloud.google.com/go/secretmanager/apiv1"
	"github.com/sirupsen/logrus"
	"google.golang.org/api/option"

	coreapi "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	authclientset "k8s.io/client-go/kubernetes/typed/authorization/v1"
	coreclientset "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/api/configresolver"
	"github.com/openshift/ci-tools/pkg/defaults"
	"github.com/openshift/ci-tools/pkg/interrupt"
	"github.com/openshift/ci-tools/pkg/lease"
	"github.com/openshift/ci-tools/pkg/metrics"
	"github.com/openshift/ci-tools/pkg/registry/server"
	"github.com/openshift/ci-tools/pkg/results"
	"github.com/openshift/ci-tools/pkg/steps"
	"github.com/openshift/ci-tools/pkg/steps/csi_secrets"
	tooldetector "github.com/openshift/ci-tools/pkg/tool-detector"
)

func excludeContextCancelledErrors(errs []error) []error {
	var ret []error
	for _, err := range errs {
		if !errors.Is(err, context.Canceled) {
			ret = append(ret, err)
		}
	}
	return ret
}

func (o *options) Report(errs ...error) {
	if len(errs) > 0 {
		o.writeFailingJUnit(errs)
	}

	reporter, loadErr := o.resultsOptions.Reporter(o.jobSpec, o.consoleHost)
	if loadErr != nil {
		logrus.WithError(loadErr).Warn("Could not load result reporting options.")
		return
	}

	errorToReport := excludeContextCancelledErrors(errs)
	for _, err := range errorToReport {
		reporter.Report(err)
	}

	if len(errorToReport) == 0 {
		reporter.Report(nil)
	}
}

// startHTTPServer start the HTTP server asynchronously and returns the address it
// is listening to.
func (o *options) startHTTPServer(cancel func(), srv *http.Server) (string, error) {
	srvIP := os.Getenv(api.CIOperatorHTTPServerIPEnvVarName)
	if srvIP == "" {
		return "", nil
	}

	ipAndPort := srvIP + ":" + strconv.Itoa(api.CIOperatorHTTPServerPort)
	ln, err := net.Listen("tcp", ipAndPort)
	if err != nil {
		return "", fmt.Errorf("listen tcp on %s: %w", ipAndPort, err)
	}

	go func() {
		logrus.Infof("Running the http server at %s", ipAndPort)
		if err := srv.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
			logrus.WithError(err).Error("HTTP server has been aborted unexpectedly. Terminating.")
			cancel()
		}
	}()

	addr := "http://" + ipAndPort
	return addr, nil
}

func (o *options) Run() (errs []error) {
	start := time.Now()
	var httpSrv *http.Server

	defer func() {
		logrus.Infof("Ran for %s", time.Since(start).Truncate(time.Second))
		o.metricsAgent.Stop()
		if httpSrv != nil {
			if err := httpSrv.Close(); err != nil {
				errs = append(errs, fmt.Errorf("close http server: %w", err))
			}
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	handler := func(s os.Signal) {
		logrus.Infof("error: Process interrupted with signal %s, cancelling execution...", s)
		cancel()
	}

	httpSrvMux := http.NewServeMux()
	httpSrv = &http.Server{
		Handler:     httpSrvMux,
		BaseContext: func(net.Listener) context.Context { return ctx },
	}

	httpSrvAddr, err := o.startHTTPServer(cancel, httpSrv)
	if err != nil {
		errs = append(errs, fmt.Errorf("run http server: %w", err))
		return
	}

	var leaseClient *lease.Client
	if o.isLeaseClientAvailable() {
		leaseClient = &o.leaseClient
	}

	o.resolveConsoleHost()

	streams, err := integratedStreams(o.configSpec, o.resolverClient, o.clusterConfig)
	if err != nil {
		errs = append(errs, results.ForReason("config_resolver").WithError(err).Errorf("failed to generate integrated streams: %v", err))
		return
	}

	client, err := coreclientset.NewForConfig(o.clusterConfig)
	if err != nil {
		errs = append(errs, fmt.Errorf("could not get core client for cluster config: %w", err))
		return
	}

	nodeArchitectures, err := resolveNodeArchitectures(ctx, client.Nodes())
	if err != nil {
		errs = append(errs, fmt.Errorf("could not resolve the node architectures: %w", err))
		return
	}

	var gsmConfig *csi_secrets.GSMConfiguration
	if o.enableSecretsStoreCSIDriver {
		var opts []option.ClientOption
		if o.gsmCredentialsFile != "" {
			opts = append(opts, option.WithCredentialsFile(o.gsmCredentialsFile))
		}
		gsmClient, err := secretmanager.NewClient(ctx, opts...)
		if err != nil {
			return []error{results.ForReason("gsm_client").WithError(err).Errorf("failed to initialize GSM client: %v", err)}
		}
		defer func() {
			if err := gsmClient.Close(); err != nil {
				logrus.WithError(err).Warn("Failed to close GSM client")
			}
		}()
		gsmConfig = &csi_secrets.GSMConfiguration{
			Config:          &o.gsmConfig,
			CredentialsFile: o.gsmCredentialsFile,
			ProjectConfig:   o.gsmProjectConfig,
			Client:          gsmClient,
			Censor:          o.censor,
		}
	}

	cfg := o.ToGraphConfig()
	cfg.LeaseClient = leaseClient
	cfg.NodeArchitectures = nodeArchitectures
	cfg.IntegratedStreams = streams
	cfg.InjectedTest = o.injectTest != ""
	cfg.EnableSecretsStoreCSIDriver = o.enableSecretsStoreCSIDriver
	cfg.GSMConfig = gsmConfig
	cfg.HTTPServerAddr = httpSrvAddr
	cfg.HTTPServerMux = httpSrvMux
	// load the graph from the configuration
	buildSteps, promotionSteps, err := defaults.FromConfig(cfg)
	if err != nil {
		errs = append(errs, results.ForReason("defaulting_config").WithError(err).Errorf("failed to generate steps from config: %v", err))
		return
	}

	// Before we create the namespace, we need to ensure all inputs to the graph
	// have been resolved. We must run this step before we resolve the partial
	// graph or otherwise two jobs with different targets would create different
	// artifact caches.
	if err := o.resolveInputs(buildSteps); err != nil {
		errs = append(errs, results.ForReason("resolving_inputs").WithError(err).Errorf("could not resolve inputs: %v", err))
		return
	}

	if err := o.writeMetadataJSON(); err != nil {
		errs = append(errs, fmt.Errorf("unable to write metadata.json for build: %w", err))
		return
	}
	// convert the full graph into the subset we must run
	nodes, err := api.BuildPartialGraph(buildSteps, o.targets.values)
	if err != nil {
		errs = append(errs, results.ForReason("building_graph").WithError(err).Errorf("could not build execution graph: %v", err))
		return
	}

	// Resolve which of the steps should enable multi arch based on the graph build steps.
	api.ResolveMultiArch(nodes)

	stepList, sortErrs := nodes.TopologicalSort()
	if len(sortErrs) > 0 {
		errs = append(errs, append([]error{results.ForReason("building_graph").ForError(errors.New("could not sort nodes"))}, sortErrs...)...)
		return
	}
	logrus.Infof("Running %s", strings.Join(nodeNames(stepList), ", "))
	if o.printGraph {
		if err := printDigraph(os.Stdout, stepList); err != nil {
			errs = append(errs, fmt.Errorf("could not print graph: %w", err))
			return
		}
		return
	}
	graph, calculateGraphErrs := calculateGraph(stepList)
	if len(calculateGraphErrs) > 0 {
		errs = append(errs, calculateGraphErrs...)
		return
	}
	defer func() {
		serializedGraph, err := json.Marshal(graph)
		if err != nil {
			logrus.WithError(err).Error("Failed to marshal graph")
			errs = append(errs, fmt.Errorf("failed to marshal graph: %w", err))
			return
		}

		_ = api.SaveArtifact(o.censor, api.CIOperatorStepGraphJSONFilename, serializedGraph)
	}()
	// initialize the namespace if necessary and create any resources that must
	// exist prior to execution
	if err := o.initializeNamespace(); err != nil {
		errs = append(errs, results.ForReason("initializing_namespace").WithError(err).Errorf("could not initialize namespace: %v", err))
		return
	}
	o.metricsAgent.Record(metrics.NewInsightsEvent(metrics.InsightNamespaceCreated, metrics.Context{"namespace": o.namespace}))
	info := o.getResolverInfo(o.jobSpec)
	o.metricsAgent.RecordConfigurationInsight(o.targets.values, o.promote, info.Org, info.Repo, info.Branch, info.Variant, o.baseNamespace, o.consoleHost, o.nodeName, o.clusterProfiles)

	o.adjustLeaseAcquireTimeout()

	if runErrs := interrupt.New(handler, o.saveNamespaceArtifacts).Run(func() []error {
		if leaseClient != nil {
			if err := o.initializeLeaseClient(); err != nil {
				return []error{fmt.Errorf("failed to create the lease client: %w", err)}
			}
			o.metricsAgent.RegisterLeaseClient(o.leaseClient)
			stopHeartbeating := o.startLeaseHearthbeating()
			defer func() {
				close(stopHeartbeating)
				released, err := o.leaseClient.ReleaseAll()
				if err != nil {
					logrus.WithError(err).Errorf("Failed to release leaked leases")
				}
				if leaked := o.leaseClient.Leases(); len(leaked) > 0 {
					o.metricsAgent.Record(metrics.NewInsightsEvent(metrics.InsightLeaseReleased, metrics.Context{"released_count": len(released)}))
					logrus.Warnf("Would leak leases: %v", leaked)
				}
			}()
		}
		go monitorNamespace(ctx, cancel, o.namespace, client.Namespaces())
		authClient, err := authclientset.NewForConfig(o.clusterConfig)
		if err != nil {
			return []error{fmt.Errorf("could not get auth client for cluster config: %w", err)}
		}
		eventRecorder, err := eventRecorder(client, authClient, o.namespace)
		if err != nil {
			return []error{fmt.Errorf("could not create event recorder: %w", err)}
		}
		runtimeObject := &coreapi.ObjectReference{Namespace: o.namespace}
		eventRecorder.Event(runtimeObject, coreapi.EventTypeNormal, "CiJobStarted", eventJobDescription(o.jobSpec, o.namespace))
		o.metricsAgent.Record(metrics.NewInsightsEvent(metrics.InsightExecutionStarted, metrics.Context{"started_after": time.Since(start).Seconds()}))
		// execute the graph
		suites, graphDetails, errs := steps.Run(ctx, nodes, o.metricsAgent)
		if err := o.writeJUnit(suites, "operator"); err != nil {
			logrus.WithError(err).Warn("Unable to write JUnit result.")
		}
		graph.MergeFrom(graphDetails...)
		// Rewrite the Metadata JSON to catch custom metadata if it has been generated by the job
		if err := o.writeMetadataJSON(); err != nil {
			logrus.WithError(err).Warn("Unable to update metadata.json for build")
		}
		if len(errs) > 0 {
			o.metricsAgent.Record(metrics.NewInsightsEvent(metrics.InsightExecutionCompleted, metrics.Context{"duration_seconds": time.Since(start).Seconds(), "success": false, "error_count": len(errs)}))
			eventRecorder.Event(runtimeObject, coreapi.EventTypeWarning, "CiJobFailed", eventJobDescription(o.jobSpec, o.namespace))
			var wrapped []error
			for _, err := range errs {
				wrapped = append(wrapped, &errWroteJUnit{wrapped: results.ForReason("executing_graph").WithError(err).Errorf("could not run steps: %v", err)})
			}
			return wrapped
		}

		// Run each of the promotion steps concurrently
		lenOfPromotionSteps := len(promotionSteps)
		detailsChan := make(chan api.CIOperatorStepDetails, lenOfPromotionSteps)
		errChan := make(chan error, lenOfPromotionSteps)
		for _, step := range promotionSteps {
			go runPromotionStep(ctx, step, detailsChan, errChan, o.metricsAgent)
		}
		for i := 0; i < lenOfPromotionSteps; i++ {
			select {
			case details := <-detailsChan:
				graph.MergeFrom(details)
			case err := <-errChan:
				errorDesc := fmt.Sprintf("post step failed while %s. with error: %v", eventJobDescription(o.jobSpec, o.namespace), err)
				eventRecorder.Event(runtimeObject, coreapi.EventTypeWarning, "PostStepFailed", errorDesc)
				return []error{results.ForReason("executing_post").WithError(err).Unwrap()} // If any of the promotion steps fail, it is considered a failure
			}
		}

		eventRecorder.Event(runtimeObject, coreapi.EventTypeNormal, "CiJobSucceeded", eventJobDescription(o.jobSpec, o.namespace))
		o.metricsAgent.Record(metrics.NewInsightsEvent(metrics.InsightExecutionCompleted, metrics.Context{"duration_seconds": time.Since(start).Seconds(), "success": true}))

		return nil
	}); len(runErrs) > 0 {
		errs = append(errs, runErrs...)
	}

	return
}

// determineSkippedImages determines which images can be skipped when
// build_images_if_affected is enabled and the [images] target is requested.
func determineSkippedImages(config *api.ReleaseBuildConfiguration, jobSpec *api.JobSpec, targets []string) sets.Set[string] {
	if config == nil || jobSpec == nil || !config.Images.BuildIfAffected {
		return nil
	}

	if !slices.Contains(targets, "[images]") {
		return nil
	}

	detector := tooldetector.New(jobSpec, config)
	affectedTools, err := detector.AffectedTools()
	if err != nil {
		logrus.WithError(err).Warn("Failed to detect affected tools; building all images")
		return nil
	}

	skipped := sets.New[string]()
	for _, img := range config.Images.Items {
		if !affectedTools.Has(string(img.To)) {
			skipped.Insert(string(img.To))
		}
	}
	return skipped
}

func runPromotionStep(ctx context.Context, step api.Step, detailsChan chan<- api.CIOperatorStepDetails, errChan chan<- error, metricsAgent *metrics.MetricsAgent) {
	details, err := runStep(ctx, step, metricsAgent)
	if err != nil {
		errChan <- fmt.Errorf("could not run promotion step %s: %w", step.Name(), err)
	} else {
		detailsChan <- details
	}
}

func integratedStreams(config *api.ReleaseBuildConfiguration, client server.ResolverClient, clusterConfig *rest.Config) (map[string]*configresolver.IntegratedStream, error) {
	if config == nil {
		return nil, errors.New("unable to get integrated stream for nil config")
	}
	if client == nil {
		return nil, errors.New("unable to get integrated stream with nil client")
	}
	if clusterConfig == nil {
		return nil, errors.New("unable to get integrated stream with nil rest config")
	}
	ocClient, err := ctrlruntimeclient.New(clusterConfig, ctrlruntimeclient.Options{})
	if err != nil {
		return nil, fmt.Errorf("unable to create oc client to get integreated streams: %w", err)
	}
	ret := map[string]*configresolver.IntegratedStream{}
	var objectKeys []ctrlruntimeclient.ObjectKey
	if config.ReleaseTagConfiguration != nil {
		objectKeys = append(objectKeys, ctrlruntimeclient.ObjectKey{Namespace: config.ReleaseTagConfiguration.Namespace, Name: config.ReleaseTagConfiguration.Name})
	}
	for _, release := range config.Releases {
		if release.Integration != nil {
			objectKeys = append(objectKeys, ctrlruntimeclient.ObjectKey{Namespace: release.Integration.Namespace, Name: release.Integration.Name})
		}
	}
	for _, key := range objectKeys {
		if api.IsCreatedForClusterBotJob(key.Namespace) {
			stream, err := configresolver.LocalIntegratedStream(context.TODO(), ocClient, key.Namespace, key.Name)
			if err != nil {
				return nil, fmt.Errorf("failed to get integrated stream %s/%s for a cluster bot job: %w", key.Namespace, key.Name, err)
			}
			ret[fmt.Sprintf("%s/%s", key.Namespace, key.Name)] = stream
			continue
		}
		integratedStream, err := client.IntegratedStream(key.Namespace, key.Name)
		if err != nil {
			return nil, fmt.Errorf("failed to get integrated stream %s/%s: %w", key.Namespace, key.Name, err)
		}
		ret[fmt.Sprintf("%s/%s", key.Namespace, key.Name)] = integratedStream
	}
	return ret, nil
}

// runStep mostly duplicates steps.runStep. The latter uses an *api.StepNode though and we only have an api.Step for the PostSteps
// so we can not re-use it.
func runStep(ctx context.Context, step api.Step, metricsAgent *metrics.MetricsAgent) (api.CIOperatorStepDetails, error) {
	start := time.Now()
	metricsAgent.Record(metrics.NewInsightsEvent(metrics.InsightStepStarted, metrics.Context{"step_name": step.Name(), "description": step.Description()}))

	err := step.Run(ctx)
	duration := time.Since(start)
	failed := err != nil

	metricsAgent.Record(metrics.NewInsightsEvent(metrics.InsightStepCompleted, metrics.Context{"step_name": step.Name(), "description": step.Description(), "duration_seconds": duration.Seconds(), "success": !failed}))

	var subSteps []api.CIOperatorStepDetailInfo
	if x, ok := step.(steps.SubStepReporter); ok {
		subSteps = x.SubSteps()
	}

	return api.CIOperatorStepDetails{
		CIOperatorStepDetailInfo: api.CIOperatorStepDetailInfo{
			StepName:    step.Name(),
			Description: step.Description(),
			StartedAt:   &start,
			FinishedAt: func() *time.Time {
				ret := start.Add(duration)
				return &ret
			}(),
			Duration: &duration,
			Failed:   &failed,
		},
		Substeps: subSteps,
	}, err
}
