package ephemeralcluster

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/ghodss/yaml"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/sirupsen/logrus"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/utils/ptr"
	ctrlbldr "sigs.k8s.io/controller-runtime/pkg/builder"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	ctrlruntimeutil "sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	prowv1 "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	prowconfig "sigs.k8s.io/prow/pkg/config"
	"sigs.k8s.io/prow/pkg/pjutil"

	"github.com/openshift/ci-tools/pkg/api"
	ephemeralclusterv1 "github.com/openshift/ci-tools/pkg/api/ephemeralcluster/v1"
	"github.com/openshift/ci-tools/pkg/jobconfig"
	"github.com/openshift/ci-tools/pkg/load/agents"
	"github.com/openshift/ci-tools/pkg/prowgen"
	"github.com/openshift/ci-tools/pkg/steps"
	cislices "github.com/openshift/ci-tools/pkg/util/slices"
)

const (
	ControllerName            = "ephemeral_cluster_provisioner"
	WaitTestStepName          = "wait-test-complete"
	HiveKubeconfigSecret      = EphemeralClusterTestName + "-" + api.HiveAdminKubeconfigSecret
	HiveAdminPasswdSecret     = EphemeralClusterTestName + "-" + api.HiveAdminPasswordSecret
	EphemeralClusterTestName  = "cluster-provisioning"
	EphemeralClusterLabel     = "ci.openshift.io/ephemeral-cluster"
	EphemeralClusterNamespace = "ephemeral-cluster"
	DependentProwJobFinalizer = "ephemeralcluster.ci.openshift.io/dependent-prowjob"
	UnresolvedConfigVar       = "UNRESOLVED_CONFIG"
	ProwJobCreatingDoneReason = "ProwJob has been properly created"
	ProwJobNamePrefix         = "ephemeralcluster"
	GitHubGUID                = "X-Github-Delivery"
	TooManyPJsBoundErrMsg     = "Too many ProwJobs bound, this is a bug"
	PROrgRepoNumRegexpPattern = `/([a-zA-Z0-9\-_\.]+)/([a-zA-Z0-9\-_\.]+)/pulls/(\d+)`
)

var (
	//go:embed wait-test-complete.sh
	waitKubeconfigSh string

	defaultResources = api.ResourceConfiguration{
		"*": api.ResourceRequirements{
			Requests: api.ResourceList{"cpu": "200m"},
			Limits:   api.ResourceList{"memory": "400Mi"},
		},
	}

	defaultReconcilerOpts = reconcilerOptions{
		polling:           3 * time.Second,
		privilegedTenants: sets.New[string](),
	}

	isTagRegexp = regexp.MustCompile(`(?P<Namespace>.+)/(?P<Name>.+)\:(?P<Tag>.+)`)
)

type buildClients map[string]ctrlruntimeclient.Client

func (bc buildClients) forCluster(cluster string) (ctrlruntimeclient.Client, error) {
	buildClient, ok := bc[cluster]
	if !ok {
		return nil, fmt.Errorf("unknown cluster %s", cluster)
	}
	return buildClient, nil
}

type reconcilerOptions struct {
	polling           time.Duration
	cliISTagRef       string
	privilegedTenants sets.Set[string]
}

type ReconcilerOption func(*reconcilerOptions)

func WithPolling(polling time.Duration) ReconcilerOption {
	return func(o *reconcilerOptions) {
		o.polling = polling
	}
}

// WithCLIISTagRef sets the image stream tag reference for the `cli` image in the form of `ocp/4.22:cli`.
func WithCLIISTagRef(isTagRef string) ReconcilerOption {
	return func(o *reconcilerOptions) {
		o.cliISTagRef = isTagRef
	}
}

// WithPrivilegedTenants sets a list of tenants that are allowed to use any cluster profile.
func WithPrivilegedTenants(tenants []string) ReconcilerOption {
	return func(o *reconcilerOptions) {
		o.privilegedTenants.Insert(tenants...)
	}
}

type NewProwJobFunc func(spec prowv1.ProwJobSpec, extraLabels, extraAnnotations map[string]string, modifiers ...pjutil.Modifier) prowv1.ProwJob

func clusterProfileResolverAdapter(registryAgent agents.RegistryAgent) func(string) (*api.ClusterProfile, error) {
	return func(name string) (*api.ClusterProfile, error) {
		cp, err := registryAgent.ResolveClusterProfile(name)
		if err != nil {
			return nil, err
		}
		return &cp, nil
	}
}

type reconciler struct {
	logger                 *logrus.Entry
	masterClient           ctrlruntimeclient.Client
	buildClients           buildClients
	newProwJob             NewProwJobFunc
	prowConfigAgent        *prowconfig.Agent
	clusterProfileResolver prowgen.ClusterProfileResolver
	scheme                 *runtime.Scheme
	cliISTagRef            api.ImageStreamTagReference
	privilegedTenants      sets.Set[string]

	// Mock for testing
	now     func() time.Time
	polling func() time.Duration
}

func ECPredicateFilter(object ctrlruntimeclient.Object) bool {
	return object.GetNamespace() == EphemeralClusterNamespace
}

func AddToManager(log *logrus.Entry, mgr manager.Manager, allManagers map[string]manager.Manager,
	prowConfigAgent *prowconfig.Agent, registryAgent agents.RegistryAgent, opts ...ReconcilerOption) error {
	buildClients := make(map[string]ctrlruntimeclient.Client)
	for clusterName, clusterManager := range allManagers {
		buildClients[clusterName] = clusterManager.GetClient()
	}

	for _, opt := range opts {
		opt(&defaultReconcilerOpts)
	}

	cliISTagRef, err := parseCLIISTagRef(defaultReconcilerOpts.cliISTagRef)
	if err != nil {
		return err
	}

	r := reconciler{
		logger:                 log.WithField("controller", ControllerName),
		masterClient:           mgr.GetClient(),
		buildClients:           buildClients,
		prowConfigAgent:        prowConfigAgent,
		clusterProfileResolver: clusterProfileResolverAdapter(registryAgent),
		scheme:                 mgr.GetScheme(),
		newProwJob:             pjutil.NewProwJob,
		now:                    time.Now,
		polling:                func() time.Duration { return defaultReconcilerOpts.polling },
		cliISTagRef:            cliISTagRef,
		privilegedTenants:      defaultReconcilerOpts.privilegedTenants,
	}

	if err := ctrlbldr.ControllerManagedBy(mgr).
		WithOptions(controller.Options{MaxConcurrentReconciles: 1}).
		WithEventFilter(predicate.And(
			predicate.GenerationChangedPredicate{},
			predicate.NewPredicateFuncs(ECPredicateFilter),
		)).
		For(&ephemeralclusterv1.EphemeralCluster{}).
		Complete(&r); err != nil {
		return fmt.Errorf("build controller: %w", err)
	}

	if err := addPJReconcilerToManager(log, mgr, buildClients); err != nil {
		return fmt.Errorf("build prowjob controller: %w", err)
	}

	return nil
}

func (r *reconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	log := r.logger.WithField("request", req.String())

	ec := &ephemeralclusterv1.EphemeralCluster{}
	if err := r.masterClient.Get(ctx, req.NamespacedName, ec); err != nil {
		if apierrors.IsNotFound(err) {
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, reconcile.TerminalError(fmt.Errorf("get ephemeral cluster: %w", err))
	}

	if !ec.DeletionTimestamp.IsZero() {
		return r.reconcileDeleteEphemeralCluster(ctx, log, ec)
	}

	oldStatus := ec.Status
	observedStatus := ephemeralclusterv1.EphemeralClusterStatus{ProwJobID: ec.Status.ProwJobID}

	pjId, pj := ec.Status.ProwJobID, prowv1.ProwJob{}
	log = log.WithField("prowjob", pjId)
	if pjId != "" {
		nn := types.NamespacedName{Namespace: r.prowConfigAgent.Config().ProwJobNamespace, Name: pjId}
		if err := r.masterClient.Get(ctx, nn, &pj); err != nil {
			return r.handleGetProwJobError(ctx, log, ec, err)
		}
	} else {
		return r.reconcileCreateProwJob(ctx, log, ec, &observedStatus)
	}

	upsertCondition(&observedStatus, ephemeralclusterv1.ProwJobCreating, ephemeralclusterv1.ConditionFalse, r.now(), ProwJobCreatingDoneReason, "")
	observedStatus.Phase = ephemeralclusterv1.EphemeralClusterProvisioning
	observedStatus.ProwJobURL = pj.Status.URL

	if err := r.fetchSecrets(ctx, log, ec, &oldStatus, &observedStatus, &pj); err != nil {
		if updateErr := r.updateEphemeralClusterStatus(ctx, ec, &observedStatus); updateErr != nil {
			msg := utilerrors.NewAggregate([]error{updateErr, err}).Error()
			return reconcile.Result{}, errors.New(msg)
		}
		return reconcile.Result{}, err
	}

	if ec.Spec.TearDownCluster {
		if err := r.notifyTestComplete(ctx, log, &oldStatus, &observedStatus, &pj); err != nil {
			if updateErr := r.updateEphemeralClusterStatus(ctx, ec, &observedStatus); updateErr != nil {
				msg := utilerrors.NewAggregate([]error{updateErr, err}).Error()
				return reconcile.Result{}, errors.New(msg)
			}
			return reconcile.Result{}, err
		}
	}

	var requeueAfter time.Duration
	// This is a stop-polling condition: if the PJ is in a final state there is nothing to do.
	if isFinalState := r.reportProwJobFinalState(&pj, &observedStatus); isFinalState {
		if finalizers, removed := cislices.Delete(ec.Finalizers, DependentProwJobFinalizer); removed {
			log.Info("ProwJob in a definitive state, finalizer removed")
			ec.Finalizers = finalizers
			ec.Status = observedStatus
			return reconcile.Result{}, r.updateEphemeralClusterWithStatus(ctx, ec)
		}
	} else {
		requeueAfter = r.polling()
	}

	if err := r.updateEphemeralClusterStatus(ctx, ec, &observedStatus); err != nil {
		return reconcile.Result{}, err
	}

	return reconcile.Result{RequeueAfter: requeueAfter}, nil
}

func (r *reconciler) handleGetProwJobError(ctx context.Context, log *logrus.Entry, ec *ephemeralclusterv1.EphemeralCluster, err error) (reconcile.Result, error) {
	if apierrors.IsNotFound(err) {
		if finalizers, removed := cislices.Delete(ec.Finalizers, DependentProwJobFinalizer); removed {
			log.Info("ProwJob not found, removing the finalizer")
			ec.Finalizers = finalizers
			return reconcile.Result{}, r.updateEphemeralCluster(ctx, ec)
		}
		return reconcile.Result{}, nil
	}
	return reconcile.Result{}, fmt.Errorf("get prowjob: %w", err)
}

func (r *reconciler) generateCIOperatorConfig(log *logrus.Entry, ec *ephemeralclusterv1.EphemeralCluster) (*api.ReleaseBuildConfiguration, error) {
	log.Info("Generating ci-operator config")
	resources := ec.Spec.CIOperator.Resources
	if len(resources) == 0 {
		log.Info("ci-operator resources stanza is not set, using default values")
		resources = defaultResources
	}

	releases := ec.Spec.CIOperator.Releases
	baseImages := make(map[string]api.ImageStreamTagReference)
	if ec.Spec.CIOperator.BaseImages != nil {
		baseImages = ec.Spec.CIOperator.BaseImages
	}

	cliImgName := "cli"
	if ec.Spec.CIOperator.Test.ClusterClaim == nil {
		if len(releases) == 0 {
			return nil, errors.New("releases stanza not set")
		}
	} else {
		cliImgName = r.injectCLIIntoBaseImages(baseImages)
	}

	return &api.ReleaseBuildConfiguration{
		InputConfiguration: api.InputConfiguration{
			BuildRootImage: ec.Spec.CIOperator.BuildRootImage,
			BaseImages:     baseImages,
			ExternalImages: ec.Spec.CIOperator.ExternalImages,
			Releases:       releases,
		},
		Resources: resources,
		Tests: []api.TestStepConfiguration{{
			As:           EphemeralClusterTestName,
			Cron:         ptr.To("@yearly"),
			ClusterClaim: ec.Spec.CIOperator.Test.ClusterClaim,
			MultiStageTestConfiguration: &api.MultiStageTestConfiguration{
				Workflow: &ec.Spec.CIOperator.Test.Workflow,
				Test: []api.TestStep{{
					LiteralTestStep: &api.LiteralTestStep{
						As:       WaitTestStepName,
						From:     cliImgName,
						Commands: waitKubeconfigSh,
						Resources: api.ResourceRequirements{
							Requests: api.ResourceList{"cpu": "10m"},
							Limits:   api.ResourceList{"memory": "100Mi"},
						},
					},
				}},
				Environment:    ec.Spec.CIOperator.Test.Env,
				ClusterProfile: ec.Spec.CIOperator.Test.ClusterProfile,
			},
		}},
		Metadata: api.Metadata{Org: "org", Repo: "repo", Branch: "branch"},
	}, nil
}

// injectCLIIntoBaseImages makes sure the `cli` image is always available. When a cluster is
// assembled from a regular release payload, `cli` gets resolved from the payload itself.
// On the other hand that's not the case for clusters claimed from Hive, hence this routime ensure
// to import the `cli` image anyway.
func (r *reconciler) injectCLIIntoBaseImages(baseImages map[string]api.ImageStreamTagReference) string {
	// Find the longest base image whose name starts with `cli` and then append a suffix to
	// it in order to avoid collisions.
	var longestName string
	for name := range baseImages {
		if strings.HasPrefix(name, "cli") && len(name) > len(longestName) {
			longestName = name
		}
	}

	if longestName == "" {
		longestName = "cli"
	} else {
		longestName = longestName + "-2"
	}

	baseImages[longestName] = r.cliISTagRef
	return longestName
}

func (r *reconciler) validateEphemeralCluster(log *logrus.Entry, ec *ephemeralclusterv1.EphemeralCluster) error {
	log.Info("Validating the Ephemeral Cluster")

	// When the cluser claim is set, there is no need to validate the cluster profile.
	if ec.Spec.CIOperator.Test.ClusterClaim != nil {
		return nil
	}

	clusterProfileName := ec.Spec.CIOperator.Test.ClusterProfile
	if clusterProfileName == "" {
		log.Error("Cluster profile has not been set")
		return errors.New("cluster profile has not been set")
	}

	log = log.WithField("cluster_profile", clusterProfileName)

	clusterProfile, err := r.clusterProfileResolver(clusterProfileName)
	if err != nil {
		log.WithError(err).Error("Failed to resolve cluster profile")
		return fmt.Errorf("resolve cluster profile %s: %w", clusterProfileName, err)
	}

	cluster, tenant := ec.KonfluxCluster(), ec.KonfluxTenant()
	log = log.WithField("cluster", cluster).WithField("tenant", tenant)

	if r.privilegedTenants.Has(tenant) {
		log.Warn("Cluster profile is valid and owned by a privileged tenant")
		return nil
	}

	for _, owner := range clusterProfile.Owners {
		if k := owner.Konflux; k != nil {
			if tenant == k.Tenant && slices.Contains(k.ClustersResolved, cluster) {
				log.Info("Cluster profile is valid")
				return nil
			}
		}
	}

	log.Error("Konflux cluster and tenant don't own this cluster profile")
	return fmt.Errorf("konflux cluster %q and tenant %q don't own the cluster profile %q", cluster, tenant, clusterProfileName)
}

func (r *reconciler) reconcileCreateProwJob(ctx context.Context, log *logrus.Entry, ec *ephemeralclusterv1.EphemeralCluster, observedStatus *ephemeralclusterv1.EphemeralClusterStatus) (reconcile.Result, error) {
	log.Info("Starting the procedure to create a ProwJob")

	if err := r.validateEphemeralCluster(log, ec); err != nil {
		upsertCondition(observedStatus, ephemeralclusterv1.ProwJobCreating, ephemeralclusterv1.ConditionFalse, r.now(), ephemeralclusterv1.EphemeralClusterValidationFailureReason, err.Error())
		observedStatus.Phase = ephemeralclusterv1.EphemeralClusterFailed
		if updateErr := r.updateEphemeralClusterStatus(ctx, ec, observedStatus); updateErr != nil {
			msg := utilerrors.NewAggregate([]error{updateErr, err}).Error()
			return reconcile.Result{}, errors.New(msg)
		}
		return reconcile.Result{}, reconcile.TerminalError(fmt.Errorf("validate ephemeral cluster: %w", err))
	}

	pjsForEC := prowv1.ProwJobList{}
	listOpts := []ctrlruntimeclient.ListOption{
		ctrlruntimeclient.MatchingLabels{EphemeralClusterLabel: ec.Name},
		ctrlruntimeclient.InNamespace(r.prowConfigAgent.Config().ProwJobNamespace),
	}
	if err := r.masterClient.List(ctx, &pjsForEC, listOpts...); err != nil {
		log.WithError(err).Error("Failed to list ProwJobs to check whether the request has one associated already")
		return reconcile.Result{}, fmt.Errorf("list pjs to create ec: %w", err)
	}

	// This is a case that should never happen but if it does it means that either someone
	// is creating PJs manually or there is a bug in this controller.
	if len(pjsForEC.Items) > 1 {
		log.Error(TooManyPJsBoundErrMsg)
		upsertCondition(observedStatus, ephemeralclusterv1.ProwJobCreating, ephemeralclusterv1.ConditionFalse, r.now(), ephemeralclusterv1.TooManyProwJobsBoundReason, TooManyPJsBoundErrMsg)
		observedStatus.Phase = ephemeralclusterv1.EphemeralClusterFailed
		err := errors.New("too many ProwJobs associated")
		if updateErr := r.updateEphemeralClusterStatus(ctx, ec, observedStatus); updateErr != nil {
			msg := utilerrors.NewAggregate([]error{updateErr, err}).Error()
			return reconcile.Result{}, errors.New(msg)
		}
		return reconcile.Result{}, reconcile.TerminalError(err)
	}

	// This is another case that should be very unlikely to happen. We expect this controller
	// to bind the PJ to an EC as soon as the former gets created.
	if len(pjsForEC.Items) == 1 {
		log.Info("ProwJob found but was not bound to the EC, binding now")
		ec.Finalizers, _ = cislices.UniqueAdd(ec.Finalizers, DependentProwJobFinalizer)
		upsertCondition(&ec.Status, ephemeralclusterv1.ProwJobCreating, ephemeralclusterv1.ConditionTrue, r.now(), "", "")
		observedStatus.ProwJobID = pjsForEC.Items[0].Name
		observedStatus.Phase = ephemeralclusterv1.EphemeralClusterProvisioning
		ec.Status = *observedStatus
		if err := r.updateEphemeralClusterWithStatus(ctx, ec); err != nil {
			return reconcile.Result{}, err
		}
		return reconcile.Result{RequeueAfter: r.polling()}, nil
	}

	log.Info("ProwJob doesn't exist, creating")
	err := r.createProwJob(ctx, log, ec)
	if updateErr := r.updateEphemeralClusterWithStatus(ctx, ec); updateErr != nil {
		msg := utilerrors.NewAggregate([]error{updateErr, err}).Error()
		return reconcile.Result{}, errors.New(msg)
	}

	requeueAfter := r.polling()
	if err != nil {
		requeueAfter = 0
	}

	return reconcile.Result{RequeueAfter: requeueAfter}, err
}

func (r *reconciler) createProwJob(ctx context.Context, log *logrus.Entry, ec *ephemeralclusterv1.EphemeralCluster) error {
	upsertProvisioningCond := func(status ephemeralclusterv1.ConditionStatus, reason, msg string) {
		upsertCondition(&ec.Status, ephemeralclusterv1.ProwJobCreating, status, r.now(), reason, msg)
	}

	ciOperatorConfig, err := r.generateCIOperatorConfig(log, ec)
	if err != nil {
		log.WithError(err).Error("generate ci-operator config")
		err = fmt.Errorf("generate ci-operator config: %w", err)
		upsertProvisioningCond(ephemeralclusterv1.ConditionFalse, ephemeralclusterv1.CIOperatorJobsGenerateFailureReason, err.Error())
		ec.Status.Phase = ephemeralclusterv1.EphemeralClusterFailed
		return reconcile.TerminalError(err)
	}

	pj, err := r.makeProwJob(ciOperatorConfig, ec)
	if err != nil {
		log.WithError(err).Error("make prowjob")
		upsertProvisioningCond(ephemeralclusterv1.ConditionFalse, ephemeralclusterv1.CIOperatorJobsGenerateFailureReason, err.Error())
		ec.Status.Phase = ephemeralclusterv1.EphemeralClusterFailed
		return reconcile.TerminalError(err)
	}

	log = log.WithField("prowjob_name", pj.Name)

	if err := r.masterClient.Create(ctx, pj); err != nil {
		log.WithError(err).Error("create prowjob")
		err = fmt.Errorf("create prowjob: %w", err)
		upsertProvisioningCond(ephemeralclusterv1.ConditionFalse, ephemeralclusterv1.CIOperatorJobsGenerateFailureReason, err.Error())
		return err
	}
	log.Info("The ProwJob has been created")

	ec.Status.ProwJobID = pj.Name
	ec.Finalizers, _ = cislices.UniqueAdd(ec.Finalizers, DependentProwJobFinalizer)
	upsertProvisioningCond(ephemeralclusterv1.ConditionTrue, "", "")
	ec.Status.Phase = ephemeralclusterv1.EphemeralClusterProvisioning
	return nil
}

func (r *reconciler) prowJobName(periodic *prowconfig.Periodic, ec *ephemeralclusterv1.EphemeralCluster) (string, error) {
	if pipelineRunName := ec.PipelineRunName(); pipelineRunName != "" {
		return ProwJobNamePrefix + "-ci-" + pipelineRunName, nil
	}

	if taskRunName := ec.TaskRunName(); taskRunName != "" {
		return ProwJobNamePrefix + "-ci-" + taskRunName, nil
	}

	jobNameWithoutPrefix, prefixCut := strings.CutPrefix(periodic.JobBase.Name, jobconfig.PeriodicPrefix)
	if !prefixCut {
		return "", fmt.Errorf("failed to strip %s prefix from %s", jobconfig.PeriodicPrefix, periodic.JobBase.Name)
	}

	return ProwJobNamePrefix + jobNameWithoutPrefix, nil
}

func (r *reconciler) makeProwJob(ciOperatorConfig *api.ReleaseBuildConfiguration, ec *ephemeralclusterv1.EphemeralCluster) (*prowv1.ProwJob, error) {
	jobConfig, err := prowgen.GenerateJobs(ciOperatorConfig, &api.Metadata{
		Org:    "org",
		Repo:   "repo",
		Branch: "branch",
	}, r.clusterProfileResolver)
	if err != nil {
		return nil, fmt.Errorf("generate jobs: %w", err)
	}

	if len(jobConfig.Periodics) != 1 {
		return nil, errors.New("periodic job not found")
	}

	periodic := &jobConfig.Periodics[0]
	if err := r.prowConfigAgent.Config().DefaultPeriodic(periodic); err != nil {
		return nil, fmt.Errorf("default periodic: %w", err)
	}

	pjName, err := r.prowJobName(periodic, ec)
	if err != nil {
		return nil, fmt.Errorf("generate prowjob name: %w", err)
	}

	periodic.JobBase.Name = pjName
	periodic.UtilityConfig.ExtraRefs = []prowv1.Refs{}

	labels := make(map[string]string)
	maps.Copy(labels, periodic.Labels)
	labels[EphemeralClusterLabel] = ec.Name

	pj := r.newProwJob(pjutil.PeriodicSpec(*periodic), labels, periodic.Annotations, pjutil.RequireScheduling(true))
	// The cluster will be chosen by the dispatcher. Set a default one here in case things go sideways.
	pj.Spec.Cluster = string(api.ClusterBuild01)
	pj.Namespace = r.prowConfigAgent.Config().ProwJobNamespace
	pj.Spec.Report = false
	pj.Spec.Refs = nil

	// Inline ci-operator config
	ciOperatorConfigYaml, err := yaml.Marshal(ciOperatorConfig)
	if err != nil {
		return nil, fmt.Errorf("marshal ci-operator config: %w", err)
	}

	ciOperatorContainer := &pj.Spec.PodSpec.Containers[0]
	ciOperatorContainer.Env = append(ciOperatorContainer.Env, corev1.EnvVar{
		Name:  UnresolvedConfigVar,
		Value: string(ciOperatorConfigYaml),
	})

	// This make sure that Ephemeral cluster requests never share a ci-operator's namespace.
	// If this wasn't the case then two different requests would race at provisioning a cluster
	// and the result is undefined.
	ciOperatorContainer.Args = append(ciOperatorContainer.Args, "--input-hash="+ec.Name)

	return &pj, nil
}

func (r *reconciler) fetchSecrets(ctx context.Context, log *logrus.Entry, ec *ephemeralclusterv1.EphemeralCluster, oldStatus, ecStatus *ephemeralclusterv1.EphemeralClusterStatus, pj *prowv1.ProwJob) error {
	buildClient, err := r.buildClients.forCluster(pj.Spec.Cluster)
	if err != nil {
		log.WithField("cluster", pj.Spec.Cluster).WithError(err).Error("Build client not found")
		upsertCondition(ecStatus, ephemeralclusterv1.ClusterReady, ephemeralclusterv1.ConditionFalse, r.now(), ephemeralclusterv1.SecretsFetchFailureReason, err.Error())
		ecStatus.Phase = ephemeralclusterv1.EphemeralClusterFailed
		return reconcile.TerminalError(err)
	}

	ns, err := r.findCIOperatorTestNS(ctx, buildClient, pj)
	if err != nil {
		upsertCondition(ecStatus, ephemeralclusterv1.ClusterReady, ephemeralclusterv1.ConditionFalse, r.now(), ephemeralclusterv1.SecretsFetchFailureReason, ephemeralclusterv1.CIOperatorNSNotFoundMsg)
		if !hasCondition(oldStatus, ephemeralclusterv1.ClusterReady, ephemeralclusterv1.ConditionFalse, r.now(), ephemeralclusterv1.SecretsFetchFailureReason, ephemeralclusterv1.CIOperatorNSNotFoundMsg) {
			log.Info("Fetching cluster credentials but ci-operator NS didn't show up yet")
		}
		return nil
	}

	log = log.WithField("namespace", ns)

	if ec.Spec.CIOperator.Test.ClusterClaim != nil {
		r.fetchHiveSecrets(ctx, log, ec, buildClient, ns, oldStatus, ecStatus)
	} else {
		r.fetchClusterKubeconfig(ctx, log, ec, buildClient, ns, oldStatus, ecStatus)
	}

	return nil
}

func (r *reconciler) fetchHiveSecrets(
	ctx context.Context,
	log *logrus.Entry,
	ec *ephemeralclusterv1.EphemeralCluster,
	buildClient ctrlruntimeclient.Client,
	ns string,
	oldStatus, ecStatus *ephemeralclusterv1.EphemeralClusterStatus,
) {
	secretsReady := true
	var kubeconfig, passwd []byte

	for _, secret := range []struct {
		name string
		key  string
		data *[]byte
	}{
		{name: HiveKubeconfigSecret, key: api.HiveAdminKubeconfigSecretKey, data: &kubeconfig},
		{name: HiveAdminPasswdSecret, key: api.HiveAdminPasswordSecretKey, data: &passwd},
	} {
		s := corev1.Secret{}
		if err := buildClient.Get(ctx, types.NamespacedName{Name: secret.name, Namespace: ns}, &s); err != nil {
			secretsReady = false
			if !apierrors.IsNotFound(err) {
				log.WithField("secret", secret.name).WithError(err).Error("Failed to read secret")
				readSecretErr := fmt.Errorf("read secret %s/%s: %w", secret.name, ns, err)
				upsertCondition(ecStatus, ephemeralclusterv1.ClusterReady, ephemeralclusterv1.ConditionFalse, r.now(), ephemeralclusterv1.SecretsFetchFailureReason, readSecretErr.Error())
				return
			}
			break
		}

		data, ok := s.Data[secret.key]
		if !ok {
			secretsReady = false
			break
		}

		*secret.data = slices.Clone(data)
	}

	if !secretsReady {
		upsertCondition(ecStatus, ephemeralclusterv1.ClusterReady, ephemeralclusterv1.ConditionFalse, r.now(), ephemeralclusterv1.SecretsFetchFailureReason, ephemeralclusterv1.HiveSecretsNotReadyMsg)
		return
	}

	secretData := map[string][]byte{
		"kubeconfig":        kubeconfig,
		"kubeAdminPassword": passwd,
	}
	if err := r.createCredentialsSecret(ctx, log, ec, secretData); err != nil {
		log.WithError(err).Error("Failed to create credentials secret")
		upsertCondition(ecStatus, ephemeralclusterv1.ClusterReady, ephemeralclusterv1.ConditionFalse, r.now(), ephemeralclusterv1.SecretsFetchFailureReason, err.Error())
		return
	}

	ecStatus.SecretRef = credentialsSecretName(ec)
	upsertCondition(ecStatus, ephemeralclusterv1.ClusterReady, ephemeralclusterv1.ConditionTrue, r.now(), "", "")
	if !hasCondition(oldStatus, ephemeralclusterv1.ClusterReady, ephemeralclusterv1.ConditionTrue, r.now(), "", "") {
		log.Info("Hive secrets fetched, the cluster is ready")
	}
	ecStatus.Phase = ephemeralclusterv1.EphemeralClusterReady
}

func (r *reconciler) fetchClusterKubeconfig(
	ctx context.Context,
	log *logrus.Entry,
	ec *ephemeralclusterv1.EphemeralCluster,
	buildClient ctrlruntimeclient.Client,
	ns string,
	oldStatus, ecStatus *ephemeralclusterv1.EphemeralClusterStatus,
) {
	kubeconfigSecret := corev1.Secret{}
	// The secret is named after the test name.
	if err := buildClient.Get(ctx, types.NamespacedName{Name: EphemeralClusterTestName, Namespace: ns}, &kubeconfigSecret); err != nil {
		log.WithField("secret", EphemeralClusterTestName).WithError(err).Error("Failed to read secret")
		upsertCondition(ecStatus, ephemeralclusterv1.ClusterReady, ephemeralclusterv1.ConditionFalse, r.now(), ephemeralclusterv1.SecretsFetchFailureReason, err.Error())
		return
	}

	kubeconfig, ok := kubeconfigSecret.Data["kubeconfig"]
	if !ok {
		// The kubeconfig takes time before being stored into the secret, requeuing.
		upsertCondition(ecStatus, ephemeralclusterv1.ClusterReady, ephemeralclusterv1.ConditionFalse, r.now(), ephemeralclusterv1.SecretsFetchFailureReason, ephemeralclusterv1.KubeconfigNotReadyMsg)
		return
	}

	secretData := map[string][]byte{
		"kubeconfig":        kubeconfig,
		"kubeAdminPassword": {},
	}
	if err := r.createCredentialsSecret(ctx, log, ec, secretData); err != nil {
		log.WithError(err).Error("Failed to create credentials secret")
		upsertCondition(ecStatus, ephemeralclusterv1.ClusterReady, ephemeralclusterv1.ConditionFalse, r.now(), ephemeralclusterv1.SecretsFetchFailureReason, err.Error())
		return
	}

	ecStatus.SecretRef = credentialsSecretName(ec)
	upsertCondition(ecStatus, ephemeralclusterv1.ClusterReady, ephemeralclusterv1.ConditionTrue, r.now(), "", "")
	if !hasCondition(oldStatus, ephemeralclusterv1.ClusterReady, ephemeralclusterv1.ConditionTrue, r.now(), "", "") {
		log.Info("Kubeconfig fetched, the cluster is ready")
	}
	ecStatus.Phase = ephemeralclusterv1.EphemeralClusterReady
}

func (r *reconciler) updateEphemeralClusterStatus(ctx context.Context, ec *ephemeralclusterv1.EphemeralCluster, observedStatus *ephemeralclusterv1.EphemeralClusterStatus) error {
	cmpECStatusOpts := []cmp.Option{
		cmpopts.SortSlices(func(a, b ephemeralclusterv1.EphemeralClusterCondition) int {
			return strings.Compare(string(a.Type), string(b.Type))
		}),
		cmpopts.IgnoreTypes(metav1.Time{}),
	}

	if !cmp.Equal(&ec.Status, observedStatus, cmpECStatusOpts...) {
		ec.Status = *observedStatus
		if err := r.masterClient.Status().Update(ctx, ec); err != nil {
			return fmt.Errorf("update ephemeral cluster status: %w", err)
		}
	}
	return nil
}

func (r *reconciler) updateEphemeralCluster(ctx context.Context, ec *ephemeralclusterv1.EphemeralCluster) error {
	if err := r.masterClient.Update(ctx, ec); err != nil {
		return fmt.Errorf("update ephemeral cluster: %w", err)
	}
	return nil
}

func (r *reconciler) updateEphemeralClusterWithStatus(ctx context.Context, ec *ephemeralclusterv1.EphemeralCluster) error {
	// Save the status since the ec resource has the `status` subresource. The apiserver will
	// persist only the `.spec` stanza and return object with the old status. This means
	// we lose our status, therefore a deep copy here is needed.
	ecStatus := ec.Status.DeepCopy()
	if err := r.masterClient.Update(ctx, ec); err != nil {
		return fmt.Errorf("update ephemeral cluster: %w", err)
	}

	ec.Status = *ecStatus
	if err := r.masterClient.Status().Update(ctx, ec); err != nil {
		return fmt.Errorf("update ephemeral cluster and status: %w", err)
	}

	return nil
}

func credentialsSecretName(ec *ephemeralclusterv1.EphemeralCluster) string {
	return ec.Name + "-credentials"
}

func (r *reconciler) createCredentialsSecret(
	ctx context.Context,
	log *logrus.Entry,
	ec *ephemeralclusterv1.EphemeralCluster,
	data map[string][]byte,
) error {
	secretName := credentialsSecretName(ec)

	existing := corev1.Secret{}
	err := r.masterClient.Get(ctx, types.NamespacedName{Name: secretName, Namespace: ec.Namespace}, &existing)
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("check credentials secret: %w", err)
	}
	if err == nil {
		if !metav1.IsControlledBy(&existing, ec) {
			return fmt.Errorf("credentials secret %s/%s exists but is not owned by ephemeralcluster %s (uid=%s)",
				ec.Namespace, secretName, ec.Name, ec.UID)
		}
		return nil
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: ec.Namespace,
		},
		Data: data,
	}
	if err := ctrlruntimeutil.SetControllerReference(ec, secret, r.scheme); err != nil {
		return fmt.Errorf("set owner reference on credentials secret: %w", err)
	}

	if err := r.masterClient.Create(ctx, secret); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("create credentials secret: %w", err)
		}
	} else {
		log.WithField("secret", secretName).Info("Cluster credentials secret created")
	}

	return nil
}

// reportProwJobFinalState reports whether the pj is in a final state or not.
func (r *reconciler) reportProwJobFinalState(pj *prowv1.ProwJob, observedStatus *ephemeralclusterv1.EphemeralClusterStatus) bool {
	addCondition := func(status ephemeralclusterv1.ConditionStatus, reason, msg string) {
		upsertCondition(observedStatus, ephemeralclusterv1.ProwJobCompleted, status, r.now(), reason, msg)
	}

	switch pj.Status.State {
	case prowv1.AbortedState, prowv1.ErrorState, prowv1.FailureState:
		msg := "prowjob state: " + string(pj.Status.State)
		addCondition(ephemeralclusterv1.ConditionTrue, ephemeralclusterv1.ProwJobFailureReason, msg)
		observedStatus.Phase = ephemeralclusterv1.EphemeralClusterFailed
		return true
	case prowv1.SuccessState:
		msg := "prowjob state: " + string(pj.Status.State)
		addCondition(ephemeralclusterv1.ConditionTrue, ephemeralclusterv1.ProwJobCompletedReason, msg)
		observedStatus.Phase = ephemeralclusterv1.EphemeralClusterDeprovisioned
		return true
	}
	return false
}

func (r *reconciler) reconcileDeleteEphemeralCluster(ctx context.Context, log *logrus.Entry, ec *ephemeralclusterv1.EphemeralCluster) (reconcile.Result, error) {
	removeFinalizer := func() bool {
		finalizers, removed := cislices.Delete(ec.Finalizers, DependentProwJobFinalizer)
		if removed {
			ec.Finalizers = finalizers
		}
		return removed
	}

	pjId := ec.Status.ProwJobID
	if pjId == "" {
		if removeFinalizer() {
			log.Info("ProwJob ID is empty, removing the finalizer")
			return reconcile.Result{}, r.updateEphemeralCluster(ctx, ec)
		}
		return reconcile.Result{}, nil
	}

	pj := prowv1.ProwJob{}
	nn := types.NamespacedName{Namespace: r.prowConfigAgent.Config().ProwJobNamespace, Name: pjId}
	if err := r.masterClient.Get(ctx, nn, &pj); err != nil {
		if apierrors.IsNotFound(err) {
			if removeFinalizer() {
				log.Info("ProwJob not found, removing the finalizer")
				return reconcile.Result{}, r.updateEphemeralCluster(ctx, ec)
			}
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, fmt.Errorf("get prowjob: %w", err)
	}

	oldStatus := ec.Status
	observedStatus := ec.Status.DeepCopy()
	log = log.WithField("prowjob_name", pj.Name)

	if isFinalState := r.reportProwJobFinalState(&pj, observedStatus); isFinalState {
		if removeFinalizer() {
			log.Info("ProwJob in a definitive state, removing the finalizer")
			return reconcile.Result{}, r.updateEphemeralCluster(ctx, ec)
		}
		return reconcile.Result{}, r.updateEphemeralClusterStatus(ctx, ec, observedStatus)
	}

	if err := r.notifyTestComplete(ctx, log, &oldStatus, observedStatus, &pj); err != nil {
		if updateErr := r.updateEphemeralClusterStatus(ctx, ec, observedStatus); updateErr != nil {
			msg := utilerrors.NewAggregate([]error{updateErr, err}).Error()
			return reconcile.Result{}, errors.New(msg)
		}
		return reconcile.Result{}, err
	}

	if err := r.updateEphemeralClusterStatus(ctx, ec, observedStatus); err != nil {
		return reconcile.Result{}, err
	}

	return reconcile.Result{RequeueAfter: r.polling()}, nil
}

func (r *reconciler) notifyTestComplete(ctx context.Context, log *logrus.Entry, oldECStatus, ecStatus *ephemeralclusterv1.EphemeralClusterStatus, pj *prowv1.ProwJob) error {
	ecStatus.Phase = ephemeralclusterv1.EphemeralClusterDeprovisioning

	buildClient, err := r.buildClients.forCluster(pj.Spec.Cluster)
	if err != nil {
		log.WithField("cluster", pj.Spec.Cluster).WithError(err).Warn("Build client not found")
		upsertCondition(ecStatus, ephemeralclusterv1.TestCompleted, ephemeralclusterv1.ConditionFalse, r.now(), ephemeralclusterv1.CreateTestCompletedSecretFailureReason, err.Error())
		ecStatus.Phase = ephemeralclusterv1.EphemeralClusterFailed
		return reconcile.TerminalError(err)
	}

	ns, err := r.findCIOperatorTestNS(ctx, buildClient, pj)
	if err != nil {
		upsertCondition(ecStatus, ephemeralclusterv1.TestCompleted, ephemeralclusterv1.ConditionFalse, r.now(), ephemeralclusterv1.CreateTestCompletedSecretFailureReason, ephemeralclusterv1.CIOperatorNSNotFoundMsg)
		ecStatus.Phase = ephemeralclusterv1.EphemeralClusterFailed
		return nil
	}

	log = log.WithField("namespace", ns).WithField("secret", api.EphemeralClusterTestDoneSignalSecretName)

	if err := buildClient.Create(ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
		Name:      api.EphemeralClusterTestDoneSignalSecretName,
		Namespace: ns,
	}}); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			log.WithError(err).Warn("Failed to create the secret")
			upsertCondition(ecStatus, ephemeralclusterv1.TestCompleted, ephemeralclusterv1.ConditionFalse, r.now(), ephemeralclusterv1.CreateTestCompletedSecretFailureReason, err.Error())
			return err
		}
	}

	upsertCondition(ecStatus, ephemeralclusterv1.TestCompleted, ephemeralclusterv1.ConditionTrue, r.now(), "", "")
	if !hasCondition(oldECStatus, ephemeralclusterv1.TestCompleted, ephemeralclusterv1.ConditionTrue, r.now(), "", "") {
		log.Info("Secret to signal deprovisioning procedures created")
	}

	return nil
}

func (r *reconciler) findCIOperatorTestNS(ctx context.Context, buildClient ctrlruntimeclient.Client, pj *prowv1.ProwJob) (string, error) {
	nss := corev1.NamespaceList{}
	if err := buildClient.List(ctx, &nss, ctrlruntimeclient.MatchingLabels{steps.LabelJobID: pj.Name}); err != nil {
		return "", fmt.Errorf("get namespace for %s: %w", pj.Name, err)
	}

	// The NS hasn't been created yet, requeuing.
	if len(nss.Items) == 0 {
		return "", errors.New("ci-operator NS not found")
	}

	return nss.Items[0].Name, nil
}

func upsertCondition(ecStatus *ephemeralclusterv1.EphemeralClusterStatus, t ephemeralclusterv1.EphemeralClusterConditionType, status ephemeralclusterv1.ConditionStatus, now time.Time, reason, msg string) {
	newCond := ephemeralclusterv1.EphemeralClusterCondition{
		Type:               t,
		Status:             status,
		LastTransitionTime: metav1.NewTime(now),
		Reason:             reason,
		Message:            msg,
	}

	for i := range ecStatus.Conditions {
		cond := &ecStatus.Conditions[i]
		if cond.Type == newCond.Type {
			if conditionsEqual(cond, &newCond) {
				return
			}
			ecStatus.Conditions[i] = newCond
			return
		}
	}

	ecStatus.Conditions = append(ecStatus.Conditions, newCond)
}

func hasCondition(ecStatus *ephemeralclusterv1.EphemeralClusterStatus, t ephemeralclusterv1.EphemeralClusterConditionType, status ephemeralclusterv1.ConditionStatus, now time.Time, reason, msg string) bool {
	newCond := ephemeralclusterv1.EphemeralClusterCondition{
		Type:               t,
		Status:             status,
		LastTransitionTime: metav1.NewTime(now),
		Reason:             reason,
		Message:            msg,
	}

	for i := range ecStatus.Conditions {
		cond := &ecStatus.Conditions[i]
		if cond.Type == newCond.Type {
			if conditionsEqual(cond, &newCond) {
				return true
			}
		}
	}

	return false
}

func conditionsEqual(a, b *ephemeralclusterv1.EphemeralClusterCondition) bool {
	return a.Message == b.Message && a.Reason == b.Reason &&
		a.Status == b.Status && a.Type == b.Type
}

func parseCLIISTagRef(isTag string) (api.ImageStreamTagReference, error) {
	isTagRef := api.ImageStreamTagReference{}
	matches := isTagRegexp.FindStringSubmatch(isTag)
	if matches == nil || len(matches) != 4 {
		return isTagRef, fmt.Errorf("invalid istag for the cli image: %s", isTag)
	}

	isTagRef.Namespace = matches[1]
	isTagRef.Name = matches[2]
	isTagRef.Tag = matches[3]

	return isTagRef, nil
}
