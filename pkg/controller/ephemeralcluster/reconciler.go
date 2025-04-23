package ephemeralcluster

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"time"

	"github.com/ghodss/yaml"
	"github.com/sirupsen/logrus"

	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	ctrlbldr "sigs.k8s.io/controller-runtime/pkg/builder"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	prowv1 "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	prowconfig "sigs.k8s.io/prow/pkg/config"
	"sigs.k8s.io/prow/pkg/github"
	"sigs.k8s.io/prow/pkg/pjutil"

	"github.com/openshift/ci-tools/pkg/api"
	ephemeralclusterv1 "github.com/openshift/ci-tools/pkg/api/ephemeralcluster/v1"
	"github.com/openshift/ci-tools/pkg/prowgen"
	"github.com/openshift/ci-tools/pkg/steps"
	cislices "github.com/openshift/ci-tools/pkg/util/slices"
)

const (
	WaitTestStepName          = "wait-test-complete"
	ProwJobNamespace          = "ci"
	EphemeralClusterNameLabel = "ci.openshift.io/ephemeral-cluster-name"
	EphemeralClusterNamespace = "konflux-ephemeral-cluster"
	AbortProwJobDeleteEC      = "Ephemeral Cluster deleted"
	DependentProwJobFinalizer = "ephemeralcluster.ci.openshift.io/dependent-prowjob"
	TestDoneSecretName        = "test-done-keep-going"
	UnresolvedConfigVar       = "UNRESOLVED_CONFIG"
)

var (
	//go:embed wait-test-complete.sh
	waitKubeconfigSh string
)

type NewPresubmitFunc func(pr github.PullRequest, baseSHA string, job prowconfig.Presubmit, eventGUID string, additionalLabels map[string]string, modifiers ...pjutil.Modifier) prowv1.ProwJob

type reconciler struct {
	logger          *logrus.Entry
	masterClient    ctrlruntimeclient.Client
	buildClients    map[string]ctrlruntimeclient.Client
	newPresubmit    NewPresubmitFunc
	prowConfigAgent *prowconfig.Agent

	// Mock for testing
	now     func() time.Time
	polling func() time.Duration
}

func AddToManager(logger *logrus.Entry, mgr manager.Manager, allManagers map[string]manager.Manager,
	prowConfigAgent *prowconfig.Agent) error {
	buildClients := make(map[string]ctrlruntimeclient.Client)
	for clusterName, clusterManager := range allManagers {
		buildClients[clusterName] = clusterManager.GetClient()
	}

	r := reconciler{
		logger:          logger,
		masterClient:    mgr.GetClient(),
		buildClients:    buildClients,
		prowConfigAgent: prowConfigAgent,
	}

	if err := ctrlbldr.ControllerManagedBy(mgr).
		WithOptions(controller.Options{MaxConcurrentReconciles: 1}).
		For(&ephemeralclusterv1.EphemeralCluster{}).
		Complete(&r); err != nil {
		return fmt.Errorf("build controller: %w", err)
	}

	if err := addPJReconcilerToManager(logger, mgr); err != nil {
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

	ec = ec.DeepCopy()

	if !ec.DeletionTimestamp.IsZero() {
		return r.deleteEphemeralCluster(ctx, log, ec)
	}

	pjId, pj := ec.Status.ProwJobID, prowv1.ProwJob{}
	if pjId != "" {
		nn := types.NamespacedName{Namespace: ProwJobNamespace, Name: pjId}
		if err := r.masterClient.Get(ctx, nn, &pj); err != nil {
			return r.handleGetProwJobError(ctx, ec, err)
		}
	} else {
		log.Info("ProwJob not found, creating")
		r.createProwJob(ctx, log, ec)
		return reconcile.Result{RequeueAfter: r.polling()}, r.updateEphemeralCluster(ctx, ec)
	}

	if updated := r.reportProwJobStatus(&pj, ec); updated {
		return reconcile.Result{RequeueAfter: r.polling()}, r.updateEphemeralCluster(ctx, ec)
	}

	if ec.Spec.TearDownCluster {
		log.Info("Notifying test is completed")
		res, ecUpdated, err := r.notifyTestComplete(ctx, log, ec, &pj)
		if ecUpdated {
			if err := r.updateEphemeralCluster(ctx, ec); err != nil {
				return reconcile.Result{RequeueAfter: r.polling()}, err
			}
		}
		return res, err
	}

	if ec.Status.Kubeconfig == "" {
		log.Info("Fetching the kubeconfig")
		res, ecUpdated, err := r.fetchKubeconfig(ctx, log, ec, &pj)
		if ecUpdated {
			if err := r.updateEphemeralCluster(ctx, ec); err != nil {
				return reconcile.Result{RequeueAfter: r.polling()}, err
			}
		}
		return res, err
	}

	return reconcile.Result{}, nil
}

func (r *reconciler) handleGetProwJobError(ctx context.Context, ec *ephemeralclusterv1.EphemeralCluster, err error) (reconcile.Result, error) {
	if kerrors.IsNotFound(err) {
		finalizers, removed := cislices.Delete(ec.Finalizers, DependentProwJobFinalizer)
		if removed {
			ec.Finalizers = finalizers
			return reconcile.Result{}, r.updateEphemeralCluster(ctx, ec)
		}
		return reconcile.Result{}, nil
	} else {
		return reconcile.Result{}, fmt.Errorf("get prowjob: %w", err)
	}
}

func (r *reconciler) createProwJob(ctx context.Context, log *logrus.Entry, ec *ephemeralclusterv1.EphemeralCluster) {
	ciOperatorConfig := &api.ReleaseBuildConfiguration{
		InputConfiguration: api.InputConfiguration{
			Releases: map[string]api.UnresolvedRelease{
				"initial": {Integration: &api.Integration{Name: "4.17", Namespace: "ocp"}},
				"latest":  {Integration: &api.Integration{Name: "4.17", Namespace: "ocp"}},
			},
		},
		Resources: api.ResourceConfiguration{
			"*": api.ResourceRequirements{
				Requests: api.ResourceList{"cpu": "200m"},
				Limits:   api.ResourceList{"memory": "400Mi"},
			},
		},
		Tests: []api.TestStepConfiguration{{
			As: "cluster-provisioning",
			MultiStageTestConfiguration: &api.MultiStageTestConfiguration{
				Test: []api.TestStep{{
					LiteralTestStep: &api.LiteralTestStep{
						As:       WaitTestStepName,
						From:     "cli",
						Commands: waitKubeconfigSh,
						Resources: api.ResourceRequirements{
							Requests: api.ResourceList{"cpu": "200m"},
							Limits:   api.ResourceList{"memory": "400Mi"},
						},
					},
				}},
			},
		}},
		Metadata: api.Metadata{Org: "org", Repo: "repo", Branch: "branch"},
	}

	upsertProvisioningCond := func(status ephemeralclusterv1.ConditionStatus, reason, msg string) {
		upsertCondition(ec, ephemeralclusterv1.ClusterProvisioning, status, r.now(), reason, msg)
	}

	pj, err := r.makeProwJob(ciOperatorConfig)
	if err != nil {
		upsertProvisioningCond(ephemeralclusterv1.ConditionFalse, ephemeralclusterv1.CIOperatorJobsGenerateFailureReason, err.Error())
		return
	}

	log = log.WithField("prowjob", pj.Name)

	if err := r.masterClient.Create(ctx, pj); err != nil {
		log.WithError(err).Error("create prowjob")
		err = fmt.Errorf("create prowjob: %w", err)
		upsertProvisioningCond(ephemeralclusterv1.ConditionFalse, ephemeralclusterv1.CIOperatorJobsGenerateFailureReason, err.Error())
		return
	}

	ec.Status.ProwJobID = pj.Name
	ec.Finalizers, _ = cislices.UniqueAdd(ec.Finalizers, DependentProwJobFinalizer)
	upsertProvisioningCond(ephemeralclusterv1.ConditionTrue, "", "")
}

func (r *reconciler) makeProwJob(ciOperatorConfig *api.ReleaseBuildConfiguration) (*prowv1.ProwJob, error) {
	jobConfig, err := prowgen.GenerateJobs(ciOperatorConfig, &prowgen.ProwgenInfo{
		Metadata: api.Metadata{
			Org:    "org",
			Repo:   "repo",
			Branch: "branch",
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

	prowYAML := prowconfig.ProwYAML{Presubmits: presubs}
	// This is a workaround to apply some defaults to the prowjob
	if err := prowconfig.DefaultAndValidateProwYAML(r.prowConfigAgent.Config(), &prowYAML, ""); err != nil {
		return nil, fmt.Errorf("validate and default presubmit: %w", err)
	}

	presubmit := &prowYAML.Presubmits[0]
	labels := map[string]string{EphemeralClusterNameLabel: ""}
	// TODO: enable scheduling only when the ci-operator config will stored into the openshift/release repository. Until then
	// the scheduler won't be able to assign a cluster properly.
	pj := r.newPresubmit(github.PullRequest{}, "fake", *presubmit, "no-event-guid", labels, pjutil.RequireScheduling(false))
	// TODO: temporary workaround: we should leverage the scheduler instead, check the comment above.
	pj.Spec.Cluster = string(api.ClusterBuild01)
	pj.Namespace = ProwJobNamespace
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

func (r *reconciler) fetchKubeconfig(ctx context.Context, log *logrus.Entry, ec *ephemeralclusterv1.EphemeralCluster, pj *prowv1.ProwJob) (reconcile.Result, bool, error) {
	buildClient, err := r.buildClientFor(pj)
	if err != nil {
		ecUpdated := upsertCondition(ec, ephemeralclusterv1.ClusterReady, ephemeralclusterv1.ConditionFalse, r.now(), ephemeralclusterv1.KubeconfigFetchFailureReason, err.Error())
		return reconcile.Result{}, ecUpdated, reconcile.TerminalError(err)
	}

	ns, err := r.findCIOperatorTestNS(ctx, buildClient, pj)
	if err != nil {
		ecUpdated := upsertCondition(ec, ephemeralclusterv1.ClusterReady, ephemeralclusterv1.ConditionFalse, r.now(), ephemeralclusterv1.KubeconfigFetchFailureReason, ephemeralclusterv1.CIOperatorNSNotFoundMsg)
		return reconcile.Result{RequeueAfter: r.polling()}, ecUpdated, nil
	}
	log.WithField("namespace", ns).Info("ci-operator namespace found")

	kubeconfigSecret := corev1.Secret{}
	// The secret is named after the test step name.
	if err := buildClient.Get(ctx, types.NamespacedName{Name: WaitTestStepName, Namespace: ns}, &kubeconfigSecret); err != nil {
		ecUpdated := upsertCondition(ec, ephemeralclusterv1.ClusterReady, ephemeralclusterv1.ConditionFalse, r.now(), ephemeralclusterv1.KubeconfigFetchFailureReason, err.Error())
		return reconcile.Result{RequeueAfter: r.polling()}, ecUpdated, nil
	}

	kubeconfig, ok := kubeconfigSecret.Data["kubeconfig"]
	if !ok {
		// The kubeconfig takes time before being stored into the secret, requeuing.
		ecUpdated := upsertCondition(ec, ephemeralclusterv1.ClusterReady, ephemeralclusterv1.ConditionFalse, r.now(), ephemeralclusterv1.KubeconfigFetchFailureReason, ephemeralclusterv1.KubeconfigNotReadMsg)
		return reconcile.Result{RequeueAfter: r.polling()}, ecUpdated, nil
	}

	ec.Status.Kubeconfig = string(kubeconfig)
	log.Info("kubeconfig fetched")
	ecUpdated := upsertCondition(ec, ephemeralclusterv1.ClusterReady, ephemeralclusterv1.ConditionTrue, r.now(), "", "")

	return reconcile.Result{}, ecUpdated, nil
}

func (r *reconciler) updateEphemeralCluster(ctx context.Context, ec *ephemeralclusterv1.EphemeralCluster) error {
	if err := r.masterClient.Update(ctx, ec); err != nil {
		return fmt.Errorf("update ephemereal cluster: %w", err)
	}
	return nil
}

// reportProwJobStatus reports the state of pj as a condition into ec. Returns true if the condition
// has been inserted.
func (r *reconciler) reportProwJobStatus(pj *prowv1.ProwJob, ec *ephemeralclusterv1.EphemeralCluster) bool {
	addCondition := func(status ephemeralclusterv1.ConditionStatus, reason, msg string) bool {
		return uniqueAddCondition(ec, ephemeralclusterv1.EphemeralClusterCondition{
			Type:               ephemeralclusterv1.ProwJobCompleted,
			Status:             status,
			LastTransitionTime: v1.NewTime(r.now()),
			Reason:             reason,
			Message:            msg,
		})
	}

	switch pj.Status.State {
	case prowv1.AbortedState, prowv1.ErrorState, prowv1.FailureState:
		msg := "prowjob state: " + string(pj.Status.State)
		return addCondition(ephemeralclusterv1.ConditionTrue, ephemeralclusterv1.ProwJobFailureReason, msg)
	case prowv1.SuccessState:
		msg := "prowjob state: " + string(pj.Status.State)
		return addCondition(ephemeralclusterv1.ConditionTrue, ephemeralclusterv1.ProwJobCompletedReason, msg)
	default:
		return false
	}
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
	nn := types.NamespacedName{Namespace: ProwJobNamespace, Name: pjId}
	if err := r.masterClient.Get(ctx, nn, &pj); err != nil {
		if kerrors.IsNotFound(err) {
			log.Info("ProwJob not found, removing the finalizer")
			return removeFinalizer()
		} else {
			return reconcile.Result{}, fmt.Errorf("get prowjob: %w", err)
		}
	}

	log = log.WithField("pj", pj.Name)
	switch pj.Status.State {
	case prowv1.AbortedState, prowv1.ErrorState, prowv1.FailureState, prowv1.SuccessState:
		log.Info("ProwJob in a definitive state already, skip abortion")
		return reconcile.Result{RequeueAfter: r.polling()}, nil
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

func (r *reconciler) notifyTestComplete(ctx context.Context, log *logrus.Entry, ec *ephemeralclusterv1.EphemeralCluster, pj *prowv1.ProwJob) (res reconcile.Result, ecUpdated bool, retErr error) {
	buildClient, err := r.buildClientFor(pj)
	if err != nil {
		log.WithField("cluster", pj.Spec.Cluster).Warn("Client not found")
		ecUpdated = upsertCondition(ec, ephemeralclusterv1.TestCompleted, ephemeralclusterv1.ConditionFalse, r.now(), ephemeralclusterv1.CreateTestCompletedFailureSecretReason, err.Error())
		res, retErr = reconcile.Result{}, reconcile.TerminalError(err)
		return
	}

	ns, err := r.findCIOperatorTestNS(ctx, buildClient, pj)
	if err != nil {
		ecUpdated = upsertCondition(ec, ephemeralclusterv1.TestCompleted, ephemeralclusterv1.ConditionFalse, r.now(), ephemeralclusterv1.CreateTestCompletedFailureSecretReason, ephemeralclusterv1.CIOperatorNSNotFoundMsg)
		res, retErr = reconcile.Result{}, nil
		return
	}

	log = log.WithField("namespace", ns)
	log.Info("ci-operator namespace found")

	createSecret := false
	if err := buildClient.Get(ctx, types.NamespacedName{Name: TestDoneSecretName, Namespace: ns}, &corev1.Secret{}); err != nil {
		if kerrors.IsNotFound(err) {
			createSecret = true
		} else {
			log.Warn("Failed to fetch the secret")
			res, retErr = reconcile.Result{RequeueAfter: r.polling()}, nil
			return
		}
	}

	log = log.WithField("secret", TestDoneSecretName)
	if createSecret {
		if err := buildClient.Create(ctx, &corev1.Secret{ObjectMeta: v1.ObjectMeta{
			Name:      TestDoneSecretName,
			Namespace: ns,
		}}); err != nil {
			log.Warn("Failed to create the secret")
			ecUpdated = upsertCondition(ec, ephemeralclusterv1.TestCompleted, ephemeralclusterv1.ConditionFalse, r.now(), ephemeralclusterv1.CreateTestCompletedFailureSecretReason, err.Error())
			res, retErr = reconcile.Result{}, err
			return
		}
	}
	log.Info("Secret created")

	ecUpdated = upsertCondition(ec, ephemeralclusterv1.TestCompleted, ephemeralclusterv1.ConditionTrue, r.now(), "", "")
	res, retErr = reconcile.Result{}, nil
	return
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

func upsertCondition(ec *ephemeralclusterv1.EphemeralCluster, t ephemeralclusterv1.EphemeralClusterConditionType, status ephemeralclusterv1.ConditionStatus, now time.Time, reason, msg string) bool {
	newCond := ephemeralclusterv1.EphemeralClusterCondition{
		Type:               t,
		Status:             status,
		LastTransitionTime: v1.NewTime(now),
		Reason:             reason,
		Message:            msg,
	}

	for i := range ec.Status.Conditions {
		cond := &ec.Status.Conditions[i]
		if cond.Type == newCond.Type {
			if conditionsEqual(cond, &newCond) {
				return false
			}
			ec.Status.Conditions[i] = newCond
			return true
		}
	}

	ec.Status.Conditions = append(ec.Status.Conditions, newCond)
	return true
}

func uniqueAddCondition(ec *ephemeralclusterv1.EphemeralCluster, c ephemeralclusterv1.EphemeralClusterCondition) bool {
	if conditions, inserted := cislices.UniqueAddFunc(ec.Status.Conditions, c, func(e ephemeralclusterv1.EphemeralClusterCondition) bool {
		return e.Type == c.Type
	}); inserted {
		ec.Status.Conditions = conditions
		return true
	}
	return false
}

func conditionsEqual(a, b *ephemeralclusterv1.EphemeralClusterCondition) bool {
	return a.Message == b.Message && a.Reason == b.Reason &&
		a.Status == b.Status && a.Type == b.Type
}
