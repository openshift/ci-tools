package ephemeralcluster

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/ghodss/yaml"
	"github.com/sirupsen/logrus"

	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/utils/ptr"
	ctrlbldr "sigs.k8s.io/controller-runtime/pkg/builder"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	prowv1 "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	prowconfig "sigs.k8s.io/prow/pkg/config"
	"sigs.k8s.io/prow/pkg/github"
	"sigs.k8s.io/prow/pkg/pjutil"

	"github.com/openshift/ci-tools/pkg/api"
	ephemeralclusterv1 "github.com/openshift/ci-tools/pkg/api/ephemeralcluster/v1"
	"github.com/openshift/ci-tools/pkg/jobconfig"
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
	AbortProwJobDeleteEC      = "Ephemeral Cluster deleted"
	DependentProwJobFinalizer = "ephemeralcluster.ci.openshift.io/dependent-prowjob"
	PREventPayload            = "ephemeralcluster.ci.openshift.io/pr-event-payload"
	PREventHeaders            = "ephemeralcluster.ci.openshift.io/pr-event-headers"
	UnresolvedConfigVar       = "UNRESOLVED_CONFIG"
	ProwJobCreatingDoneReason = "ProwJob has been properly created"
	ProwJobNamePrefix         = "ephemeralcluster"
	GitHubGUID                = "X-Github-Delivery"
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
		polling: 3 * time.Second,
	}

	isTagRegexp = regexp.MustCompile(`(?P<Namespace>.+)/(?P<Name>.+)\:(?P<Tag>.+)`)
)

type reconcilerOptions struct {
	polling     time.Duration
	cliISTagRef string
}

type ReconcilerOption func(*reconcilerOptions)

func WithPolling(polling time.Duration) ReconcilerOption {
	return func(o *reconcilerOptions) {
		o.polling = polling
	}
}

// Set the image stream tag reference for the `cli` image in the form of `ocp/4.22:cli`.
func WithCLIISTagRef(isTagRef string) ReconcilerOption {
	return func(o *reconcilerOptions) {
		o.cliISTagRef = isTagRef
	}
}

type PRMeta struct {
	Event   *PullRequestEvent
	Headers map[string]string
}

func (prm PRMeta) GitHubGUID() string {
	return prm.Headers[GitHubGUID]
}

// pullRequestEvent contains more information than the `pull_request` defined here.
// So far, that's the only field we need. Check https://docs.github.com/en/rest/using-the-rest-api/github-event-types?apiVersion=2022-11-28#pullrequestevent.
type PullRequestEvent struct {
	PullRequest *github.PullRequest `json:"pull_request,omitempty"`
}

type NewPresubmitFunc func(pr github.PullRequest, baseSHA string, job prowconfig.Presubmit, eventGUID string, additionalLabels map[string]string, modifiers ...pjutil.Modifier) prowv1.ProwJob

type reconciler struct {
	logger          *logrus.Entry
	masterClient    ctrlruntimeclient.Client
	buildClients    map[string]ctrlruntimeclient.Client
	newPresubmit    NewPresubmitFunc
	prowConfigAgent *prowconfig.Agent

	// Mock for testing
	now         func() time.Time
	polling     func() time.Duration
	cliISTagRef api.ImageStreamTagReference
}

func ECPredicateFilter(object ctrlruntimeclient.Object) bool {
	return object.GetNamespace() == EphemeralClusterNamespace
}

func AddToManager(log *logrus.Entry, mgr manager.Manager, allManagers map[string]manager.Manager,
	prowConfigAgent *prowconfig.Agent, opts ...ReconcilerOption) error {
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
		logger:          log.WithField("controller", ControllerName),
		masterClient:    mgr.GetClient(),
		buildClients:    buildClients,
		prowConfigAgent: prowConfigAgent,
		newPresubmit:    pjutil.NewPresubmit,
		now:             time.Now,
		polling:         func() time.Duration { return defaultReconcilerOpts.polling },
		cliISTagRef:     cliISTagRef,
	}

	if err := ctrlbldr.ControllerManagedBy(mgr).
		WithOptions(controller.Options{MaxConcurrentReconciles: 1}).
		WithEventFilter(predicate.NewPredicateFuncs(ECPredicateFilter)).
		For(&ephemeralclusterv1.EphemeralCluster{}).
		Complete(&r); err != nil {
		return fmt.Errorf("build controller: %w", err)
	}

	if err := addPJReconcilerToManager(log, mgr); err != nil {
		return fmt.Errorf("build prowjob controller: %w", err)
	}

	return nil
}

func (r *reconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	log := r.logger.WithField("request", req.String())

	ec := &ephemeralclusterv1.EphemeralCluster{}
	if err := r.masterClient.Get(ctx, req.NamespacedName, ec); err != nil {
		return reconcile.Result{}, reconcile.TerminalError(fmt.Errorf("get ephemeral cluster: %w", err))
	}

	// TODO: Retrieve the ProwJob ID without relying on the previous state.
	// Make sure not to create a ProwJob multiple times. As an example:
	// 1. EphemeralCluster and its ProwJob exist
	// 2. The ProwJob gets deleted
	// 3. This reconcile loop runs and creates the ProwJob again
	observedStatus := ephemeralclusterv1.EphemeralClusterStatus{ProwJobID: ec.Status.ProwJobID}

	if !ec.DeletionTimestamp.IsZero() {
		return r.deleteEphemeralCluster(ctx, log, ec)
	}

	pjId, pj := ec.Status.ProwJobID, prowv1.ProwJob{}
	log = log.WithField("prowjob", pjId)
	if pjId != "" {
		nn := types.NamespacedName{Namespace: r.prowConfigAgent.Config().ProwJobNamespace, Name: pjId}
		if err := r.masterClient.Get(ctx, nn, &pj); err != nil {
			return r.handleGetProwJobError(ctx, log, ec, err)
		}
	} else {
		return r.handleCreateProwJob(ctx, log, ec, &observedStatus)
	}

	upsertCondition(&observedStatus, ephemeralclusterv1.ProwJobCreating, ephemeralclusterv1.ConditionFalse, r.now(), ProwJobCreatingDoneReason, "")

	log.Info("Fetching the secrets")
	err := r.fetchSecrets(ctx, log, ec, &observedStatus, &pj)
	if err != nil {
		if updateErr := r.updateEphemeralClusterStatus(ctx, ec, &observedStatus); updateErr != nil {
			msg := utilerrors.NewAggregate([]error{updateErr, err}).Error()
			return reconcile.Result{}, errors.New(msg)
		}
		return reconcile.Result{}, err
	}

	if ec.Spec.TearDownCluster {
		log.Info("Notifying test is completed")
		err := r.notifyTestComplete(ctx, log, &observedStatus, &pj)
		if err != nil {
			if updateErr := r.updateEphemeralClusterStatus(ctx, ec, &observedStatus); updateErr != nil {
				msg := utilerrors.NewAggregate([]error{updateErr, err}).Error()
				return reconcile.Result{}, errors.New(msg)
			}
			return reconcile.Result{}, err
		}
	}

	var requeueAfter time.Duration
	if isFinalState := r.reportProwJobFinalState(&pj, &observedStatus); !isFinalState {
		// This is a stop-polling condition: if the PJ is in a final state there is nothing to do.
		requeueAfter = r.polling()
	}

	if err := r.updateEphemeralClusterStatus(ctx, ec, &observedStatus); err != nil {
		return reconcile.Result{}, err
	}

	return reconcile.Result{RequeueAfter: requeueAfter}, nil
}

func (r *reconciler) handleGetProwJobError(ctx context.Context, log *logrus.Entry, ec *ephemeralclusterv1.EphemeralCluster, err error) (reconcile.Result, error) {
	if kerrors.IsNotFound(err) {
		finalizers, removed := cislices.Delete(ec.Finalizers, DependentProwJobFinalizer)
		if removed {
			log.Info("ProwJob not found, removing the finalizer")
			ec.Finalizers = finalizers
			return reconcile.Result{}, r.updateEphemeralCluster(ctx, ec)
		}
		return reconcile.Result{}, nil
	} else {
		return reconcile.Result{}, fmt.Errorf("get prowjob: %w", err)
	}
}

func (r *reconciler) generateCIOperatorConfig(log *logrus.Entry, ec *ephemeralclusterv1.EphemeralCluster, prMeta PRMeta) (*api.ReleaseBuildConfiguration, error) {
	resources := ec.Spec.CIOperator.Resources
	if len(resources) == 0 {
		log.Info("Resources not set, using default values")
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

	org := prMeta.Event.PullRequest.Base.Repo.Owner.Login
	repo := prMeta.Event.PullRequest.Base.Repo.Name
	branch := prMeta.Event.PullRequest.Base.Ref

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
				ClusterProfile: api.ClusterProfile(ec.Spec.CIOperator.Test.ClusterProfile),
			},
		}},
		Metadata: api.Metadata{Org: org, Repo: repo, Branch: branch},
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

func (r *reconciler) handleCreateProwJob(ctx context.Context, log *logrus.Entry, ec *ephemeralclusterv1.EphemeralCluster, observedStatus *ephemeralclusterv1.EphemeralClusterStatus) (reconcile.Result, error) {
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
		log.Error("Too many ProwJobs bound, this is a bug")
		return reconcile.Result{}, reconcile.TerminalError(errors.New("too many ProwJobs associated"))
	}

	// This is another case that should be very unlikely to happen. We expect this controller
	// to bind the PJ to an EC as soon as the former gets created.
	if len(pjsForEC.Items) == 1 {
		log.Info("ProwJob found but was not bound to the EC, binding now")
		observedStatus.ProwJobID = pjsForEC.Items[0].Name
		if err := r.updateEphemeralClusterStatus(ctx, ec, observedStatus); err != nil {
			return reconcile.Result{}, err
		}
		return reconcile.Result{RequeueAfter: r.polling()}, nil
	}

	log.Info("ProwJob not found, creating")
	err := r.createProwJob(ctx, log, ec)
	if updateErr := r.updateEphemeralCluster(ctx, ec); updateErr != nil {
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

	annotations := ec.Annotations
	if annotations == nil {
		annotations = map[string]string{}
	}

	prMeta, err := parsePRMeta(annotations[PREventPayload], annotations[PREventHeaders])
	if err != nil {
		log.WithError(err).Error("parse pull request meta")
		err = fmt.Errorf("parse pull request meta: %w", err)
		upsertProvisioningCond(ephemeralclusterv1.ConditionFalse, ephemeralclusterv1.CIOperatorJobsGenerateFailureReason, err.Error())
		ec.Status.Phase = ephemeralclusterv1.EphemeralClusterFailed
		return reconcile.TerminalError(err)
	}

	ciOperatorConfig, err := r.generateCIOperatorConfig(log, ec, prMeta)
	if err != nil {
		log.WithError(err).Error("generate ci-operator config")
		err = fmt.Errorf("generate ci-operator config: %w", err)
		upsertProvisioningCond(ephemeralclusterv1.ConditionFalse, ephemeralclusterv1.CIOperatorJobsGenerateFailureReason, err.Error())
		ec.Status.Phase = ephemeralclusterv1.EphemeralClusterFailed
		return reconcile.TerminalError(err)
	}

	pj, err := r.makeProwJob(ciOperatorConfig, ec, prMeta)
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

	ec.Status.ProwJobID = pj.Name
	ec.Finalizers, _ = cislices.UniqueAdd(ec.Finalizers, DependentProwJobFinalizer)
	upsertProvisioningCond(ephemeralclusterv1.ConditionTrue, "", "")
	ec.Status.Phase = ephemeralclusterv1.EphemeralClusterProvisioning
	return nil
}

func (r *reconciler) makeProwJob(ciOperatorConfig *api.ReleaseBuildConfiguration, ec *ephemeralclusterv1.EphemeralCluster, prMeta PRMeta) (*prowv1.ProwJob, error) {
	jobConfig, err := prowgen.GenerateJobs(ciOperatorConfig, &prowgen.ProwgenInfo{
		Metadata: api.Metadata{
			Org:    prMeta.Event.PullRequest.Base.Repo.Owner.Login,
			Repo:   prMeta.Event.PullRequest.Base.Repo.Name,
			Branch: prMeta.Event.PullRequest.Base.Ref,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("generate jobs: %w", err)
	}

	orgRepo := ciOperatorConfig.Metadata.Org + "/" + ciOperatorConfig.Metadata.Repo
	presubs, ok := jobConfig.PresubmitsStatic[orgRepo]
	if !ok {
		return nil, errors.New("no presubmits generated")
	}

	if len(presubs) != 1 {
		return nil, errors.New("presubmit job not found")
	}

	presub := &presubs[0]
	jobNameWithoutPrefix, prefixCut := strings.CutPrefix(presub.JobBase.Name, jobconfig.PresubmitPrefix)
	if !prefixCut {
		return nil, fmt.Errorf("failed to strip %s prefix from %s", jobconfig.PresubmitPrefix, presub.JobBase.Name)
	}
	presub.JobBase.Name = ProwJobNamePrefix + jobNameWithoutPrefix

	prowYAML := prowconfig.ProwYAML{Presubmits: presubs}
	// This is a workaround to apply some defaults to the prowjob
	if err := prowconfig.DefaultAndValidateProwYAML(r.prowConfigAgent.Config(), &prowYAML, ""); err != nil {
		return nil, fmt.Errorf("validate and default presubmit: %w", err)
	}

	presubmit := &prowYAML.Presubmits[0]
	labels := map[string]string{EphemeralClusterLabel: ec.Name}
	pj := r.newPresubmit(*prMeta.Event.PullRequest, prMeta.Event.PullRequest.Base.SHA, *presubmit, prMeta.GitHubGUID(), labels, pjutil.RequireScheduling(true))
	// The cluster will be chosen by the dispatcher. Set a default one here in case things go sideways.
	pj.Spec.Cluster = string(api.ClusterBuild01)
	pj.Namespace = r.prowConfigAgent.Config().ProwJobNamespace
	// Do not report, we are not managing this PR as it's likely it's not comining from the OpenShift CI.
	pj.Spec.Report = false

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

	return &pj, nil
}

func (r *reconciler) fetchSecrets(ctx context.Context, log *logrus.Entry, ec *ephemeralclusterv1.EphemeralCluster, ecStatus *ephemeralclusterv1.EphemeralClusterStatus, pj *prowv1.ProwJob) error {
	buildClient, err := r.buildClientFor(pj)
	if err != nil {
		log.WithField("cluster", pj.Spec.Cluster).WithError(err).Error("Build client not found")
		upsertCondition(ecStatus, ephemeralclusterv1.ClusterReady, ephemeralclusterv1.ConditionFalse, r.now(), ephemeralclusterv1.SecretsFetchFailureReason, err.Error())
		ecStatus.Phase = ephemeralclusterv1.EphemeralClusterFailed
		return reconcile.TerminalError(err)
	}

	ns, err := r.findCIOperatorTestNS(ctx, buildClient, pj)
	if err != nil {
		log.Info("ci-operator NS not found")
		upsertCondition(ecStatus, ephemeralclusterv1.ClusterReady, ephemeralclusterv1.ConditionFalse, r.now(), ephemeralclusterv1.SecretsFetchFailureReason, ephemeralclusterv1.CIOperatorNSNotFoundMsg)
		return nil
	}
	log.WithField("namespace", ns).Info("ci-operator namespace found")

	if ec.Spec.CIOperator.Test.ClusterClaim != nil {
		r.fetchHiveSecrets(ctx, log, buildClient, ns, ecStatus)
	} else {
		r.fetchClusterKubeconfig(ctx, log, buildClient, ns, ecStatus)
	}

	return nil
}

func (r *reconciler) fetchHiveSecrets(ctx context.Context, log *logrus.Entry, buildClient ctrlruntimeclient.Client, ns string, ecStatus *ephemeralclusterv1.EphemeralClusterStatus) {
	secretsReady := true
	var err error
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
		if err = buildClient.Get(ctx, types.NamespacedName{Name: secret.name, Namespace: ns}, &s); err != nil {
			secretsReady = false
			if kerrors.IsNotFound(err) {
				// Not available yet, do not report this as an error
				err = nil
			} else {
				log.WithField("secret", secret.name).WithError(err).Error("Failed to read secret")
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

	if err != nil {
		upsertCondition(ecStatus, ephemeralclusterv1.ClusterReady, ephemeralclusterv1.ConditionFalse, r.now(), ephemeralclusterv1.SecretsFetchFailureReason, err.Error())
		return
	}

	if !secretsReady {
		upsertCondition(ecStatus, ephemeralclusterv1.ClusterReady, ephemeralclusterv1.ConditionFalse, r.now(), ephemeralclusterv1.SecretsFetchFailureReason, ephemeralclusterv1.HiveSecretsNotReadyMsg)
		return
	}

	ecStatus.Kubeconfig = string(kubeconfig)
	ecStatus.KubeAdminPassword = string(passwd)
	log.Info("hive secrets fetched")
	upsertCondition(ecStatus, ephemeralclusterv1.ClusterReady, ephemeralclusterv1.ConditionTrue, r.now(), "", "")
	ecStatus.Phase = ephemeralclusterv1.EphemeralClusterReady
}

func (r *reconciler) fetchClusterKubeconfig(ctx context.Context, log *logrus.Entry, buildClient ctrlruntimeclient.Client, ns string, ecStatus *ephemeralclusterv1.EphemeralClusterStatus) {
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

	ecStatus.Kubeconfig = string(kubeconfig)
	log.Info("kubeconfig fetched")
	upsertCondition(ecStatus, ephemeralclusterv1.ClusterReady, ephemeralclusterv1.ConditionTrue, r.now(), "", "")
	ecStatus.Phase = ephemeralclusterv1.EphemeralClusterReady
}

func (r *reconciler) updateEphemeralClusterStatus(ctx context.Context, ec *ephemeralclusterv1.EphemeralCluster, observedStatus *ephemeralclusterv1.EphemeralClusterStatus) error {
	if !reflect.DeepEqual(&ec.Status, observedStatus) {
		ec.Status = *observedStatus
		return r.updateEphemeralCluster(ctx, ec)
	}
	return nil
}

func (r *reconciler) updateEphemeralCluster(ctx context.Context, ec *ephemeralclusterv1.EphemeralCluster) error {
	if err := r.masterClient.Update(ctx, ec); err != nil {
		return fmt.Errorf("update ephemeral cluster: %w", err)
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

func (r *reconciler) deleteEphemeralCluster(ctx context.Context, log *logrus.Entry, ec *ephemeralclusterv1.EphemeralCluster) (reconcile.Result, error) {
	removeFinalizer := func() (reconcile.Result, error) {
		finalizers, removed := cislices.Delete(ec.Finalizers, DependentProwJobFinalizer)
		if removed {
			ec.Finalizers = finalizers
			return reconcile.Result{}, r.updateEphemeralCluster(ctx, ec)
		}
		return reconcile.Result{RequeueAfter: r.polling()}, nil
	}

	pjId := ec.Status.ProwJobID
	if pjId == "" {
		log.Info("ProwJob ID is empty, removing the finalizer")
		return removeFinalizer()
	}

	pj := prowv1.ProwJob{}
	nn := types.NamespacedName{Namespace: r.prowConfigAgent.Config().ProwJobNamespace, Name: pjId}
	if err := r.masterClient.Get(ctx, nn, &pj); err != nil {
		if kerrors.IsNotFound(err) {
			log.Info("ProwJob not found, removing the finalizer")
			return removeFinalizer()
		} else {
			return reconcile.Result{}, fmt.Errorf("get prowjob: %w", err)
		}
	}

	log = log.WithField("prowjob_name", pj.Name)
	switch pj.Status.State {
	case prowv1.AbortedState, prowv1.ErrorState, prowv1.FailureState, prowv1.SuccessState:
		log.Info("ProwJob in a definitive state already, removing the finalizer")
		return removeFinalizer()
	}

	return r.abortProwJob(ctx, log, &pj, AbortProwJobDeleteEC)
}

func (r *reconciler) abortProwJob(ctx context.Context, log *logrus.Entry, pj *prowv1.ProwJob, reason string) (reconcile.Result, error) {
	if pj.Status.State == prowv1.AbortedState {
		log.Info("ProwJob aborted already, skipping")
		return reconcile.Result{}, nil
	}

	pj.Status.State = prowv1.AbortedState
	pj.Status.Description = reason
	pj.Status.CompletionTime = ptr.To(v1.NewTime(r.now()))

	if err := r.masterClient.Update(ctx, pj); err != nil {
		return reconcile.Result{}, fmt.Errorf("abort prowjob: %w", err)
	}
	log.Info("ProwJob aborted")

	return reconcile.Result{RequeueAfter: r.polling()}, nil
}

func (r *reconciler) notifyTestComplete(ctx context.Context, log *logrus.Entry, ec *ephemeralclusterv1.EphemeralClusterStatus, pj *prowv1.ProwJob) error {
	buildClient, err := r.buildClientFor(pj)
	if err != nil {
		log.WithField("cluster", pj.Spec.Cluster).WithError(err).Warn("Build client not found")
		upsertCondition(ec, ephemeralclusterv1.TestCompleted, ephemeralclusterv1.ConditionFalse, r.now(), ephemeralclusterv1.CreateTestCompletedSecretFailureReason, err.Error())
		ec.Phase = ephemeralclusterv1.EphemeralClusterFailed
		return reconcile.TerminalError(err)
	}

	ns, err := r.findCIOperatorTestNS(ctx, buildClient, pj)
	if err != nil {
		upsertCondition(ec, ephemeralclusterv1.TestCompleted, ephemeralclusterv1.ConditionFalse, r.now(), ephemeralclusterv1.CreateTestCompletedSecretFailureReason, ephemeralclusterv1.CIOperatorNSNotFoundMsg)
		ec.Phase = ephemeralclusterv1.EphemeralClusterFailed
		return nil
	}

	log = log.WithField("namespace", ns)
	log.Info("ci-operator namespace found")

	createSecret := false
	if err := buildClient.Get(ctx, types.NamespacedName{Name: api.EphemeralClusterTestDoneSignalSecretName, Namespace: ns}, &corev1.Secret{}); err != nil {
		if kerrors.IsNotFound(err) {
			createSecret = true
		} else {
			log.WithError(err).Warn("Failed to fetch the secret")
			return nil
		}
	}

	log = log.WithField("secret", api.EphemeralClusterTestDoneSignalSecretName)
	if createSecret {
		if err := buildClient.Create(ctx, &corev1.Secret{ObjectMeta: v1.ObjectMeta{
			Name:      api.EphemeralClusterTestDoneSignalSecretName,
			Namespace: ns,
		}}); err != nil {
			log.WithError(err).Warn("Failed to create the secret")
			upsertCondition(ec, ephemeralclusterv1.TestCompleted, ephemeralclusterv1.ConditionFalse, r.now(), ephemeralclusterv1.CreateTestCompletedSecretFailureReason, err.Error())
			return nil
		}
		log.Info("Secret created")
	}

	upsertCondition(ec, ephemeralclusterv1.TestCompleted, ephemeralclusterv1.ConditionTrue, r.now(), "", "")
	ec.Phase = ephemeralclusterv1.EphemeralClusterDeprovisioning
	return nil
}

func (r *reconciler) buildClientFor(pj *prowv1.ProwJob) (ctrlruntimeclient.Client, error) {
	buildClient, ok := r.buildClients[pj.Spec.Cluster]
	if !ok {
		return nil, fmt.Errorf("uknown cluster %s", pj.Spec.Cluster)
	}
	return buildClient, nil
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
		LastTransitionTime: v1.NewTime(now),
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

func conditionsEqual(a, b *ephemeralclusterv1.EphemeralClusterCondition) bool {
	return a.Message == b.Message && a.Reason == b.Reason &&
		a.Status == b.Status && a.Type == b.Type
}

func parsePRMeta(event, headers string) (PRMeta, error) {
	prMeta := PRMeta{}

	if event == "" || headers == "" {
		return prMeta, errors.New("malformed PR event payload")
	}

	if err := json.Unmarshal([]byte(event), &prMeta.Event); err != nil {
		return prMeta, fmt.Errorf("unmarshal event: %w", err)
	}

	if err := json.Unmarshal([]byte(headers), &prMeta.Headers); err != nil {
		return prMeta, fmt.Errorf("unmarshal headers: %w", err)
	}

	if _, ok := prMeta.Headers[GitHubGUID]; !ok {
		return prMeta, errors.New("unsupported PR event payload")
	}

	return prMeta, nil
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
