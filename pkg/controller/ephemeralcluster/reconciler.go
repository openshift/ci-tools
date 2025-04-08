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
)

const (
	WaitTestStepName = "wait-test-complete"
	ProwJobNamespace = "ci"
)

var (
	//go:embed wait-test-complete.sh
	waitKubeconfigSh string
)

type NewPresubmitFunc func(pr github.PullRequest, baseSHA string, job prowconfig.Presubmit, eventGUID string, additionalLabels map[string]string, modifiers ...pjutil.Modifier) prowv1.ProwJob
type ConfigSpecUploader interface {
	UploadConfigSpec(ctx context.Context, location, ciOpConfigContent string) (string, error)
}

type reconciler struct {
	logger         *logrus.Entry
	masterClient   ctrlruntimeclient.Client
	buildClients   map[string]ctrlruntimeclient.Client
	newPresubmit   NewPresubmitFunc
	configUploader ConfigSpecUploader

	// Mock for testing
	now     func() time.Time
	polling func() time.Duration
}

func AddToManager(logger *logrus.Entry, mgr manager.Manager, allManagers map[string]manager.Manager) error {
	buildClients := make(map[string]ctrlruntimeclient.Client)
	for clusterName, clusterManager := range allManagers {
		buildClients[clusterName] = clusterManager.GetClient()
	}

	r := reconciler{
		logger:       logger,
		masterClient: mgr.GetClient(),
		buildClients: buildClients,
	}

	if err := ctrlbldr.ControllerManagedBy(mgr).
		WithOptions(controller.Options{MaxConcurrentReconciles: 1}).
		For(&ephemeralclusterv1.EphemeralCluster{}).
		Complete(&r); err != nil {
		return fmt.Errorf("build controller: %w", err)
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
		// TODO
		return reconcile.Result{}, reconcile.TerminalError(errors.New("unimplemented"))
	}

	pjId, pj := ec.Status.ProwJobID, prowv1.ProwJob{}
	if pjId != "" {
		nn := types.NamespacedName{Namespace: ProwJobNamespace, Name: pjId}
		if err := r.masterClient.Get(ctx, nn, &pj); err != nil {
			return r.handleGetProwJobError(err)
		}
	} else {
		log.Info("ProwJob not found, creating")
		r.createProwJob(ctx, log, ec)
		return reconcile.Result{RequeueAfter: r.polling()}, r.updateEphemeralCluster(ctx, ec)
	}

	if ec.Status.Kubeconfig == "" {
		log.Info("Fetching the kubeconfig")
		res, err := r.fetchKubeconfig(ctx, log, ec, &pj)
		if err := r.updateEphemeralCluster(ctx, ec); err != nil {
			return reconcile.Result{RequeueAfter: r.polling()}, err
		}
		return res, err
	}

	return reconcile.Result{}, nil
}

func (r *reconciler) handleGetProwJobError(err error) (reconcile.Result, error) {
	if kerrors.IsNotFound(err) {
		// TODO
		return reconcile.Result{}, reconcile.TerminalError(errors.New("unimplemented"))
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
			MultiStageTestConfigurationLiteral: &api.MultiStageTestConfigurationLiteral{
				Test: []api.LiteralTestStep{{
					As:       WaitTestStepName,
					From:     "cli",
					Commands: waitKubeconfigSh,
					Resources: api.ResourceRequirements{
						Requests: api.ResourceList{"cpu": "200m"},
						Limits:   api.ResourceList{"memory": "400Mi"},
					},
				}},
			},
		}},
		Metadata: api.Metadata{Org: "org", Repo: "repo", Branch: "branch"},
	}

	upsertProvisioningCond := func(status ephemeralclusterv1.ConditionStatus, reason, msg string) {
		r.upsertCondition(ec, ephemeralclusterv1.ClusterProvisioning, status, reason, msg)
	}

	pj, err := r.makeProwJob(ciOperatorConfig)
	if err != nil {
		upsertProvisioningCond(ephemeralclusterv1.ConditionFalse, ephemeralclusterv1.CIOperatorJobsGenerateFailureReason, err.Error())
		return
	}

	location, err := r.uploadCIOperatorConfig(ctx, ciOperatorConfig)
	if err != nil {
		err := fmt.Errorf("upload ci config: %w", err)
		upsertProvisioningCond(ephemeralclusterv1.ConditionFalse, ephemeralclusterv1.CIOperatorJobsGenerateFailureReason, err.Error())
		return
	}
	log.WithField("Path", location).Info("Config uploaded to GCS")

	if len(pj.Spec.PodSpec.Containers) != 1 {
		upsertProvisioningCond(ephemeralclusterv1.ConditionFalse, ephemeralclusterv1.CIOperatorJobsGenerateFailureReason, "too many presubmit containers")
		return
	}
	container := &pj.Spec.PodSpec.Containers[0]
	container.Env = append(container.Env, corev1.EnvVar{Name: "CONFIG_SPEC_GCS_URL", Value: location})

	if err := r.masterClient.Create(ctx, pj); err != nil {
		err = fmt.Errorf("create prowjob: %w", err)
		upsertProvisioningCond(ephemeralclusterv1.ConditionFalse, ephemeralclusterv1.CIOperatorJobsGenerateFailureReason, err.Error())
		return
	}

	ec.Status.ProwJobID = pj.Name
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

	var presubmit *prowconfig.Presubmit
	for i := range presubs {
		p := &presubs[i]
		if p.Name == "pull-ci-org-repo-branch-cluster-provisioning" {
			presubmit = p
			break
		}
	}
	if presubmit == nil {
		return nil, errors.New("presubmit job not found")
	}

	pj := r.newPresubmit(github.PullRequest{}, "fake", *presubmit, "no-event-guid", nil, pjutil.RequireScheduling(true))
	pj.Spec.Refs = nil
	pj.Spec.Report = false

	return &pj, nil
}

func (r *reconciler) uploadCIOperatorConfig(ctx context.Context, config *api.ReleaseBuildConfiguration) (string, error) {
	gcsPath := "ephemeral-cluster/configs"
	configBytes, err := yaml.Marshal(config)
	if err != nil {
		return "", fmt.Errorf("marshal config: %w", err)
	}
	return r.configUploader.UploadConfigSpec(ctx, gcsPath, string(configBytes))
}

func (r *reconciler) fetchKubeconfig(ctx context.Context, log *logrus.Entry, ec *ephemeralclusterv1.EphemeralCluster, pj *prowv1.ProwJob) (reconcile.Result, error) {
	buildClient, ok := r.buildClients[pj.Spec.Cluster]
	if !ok {
		err := fmt.Errorf("uknown cluster %s", pj.Spec.Cluster)
		r.upsertCondition(ec, ephemeralclusterv1.ClusterReady, ephemeralclusterv1.ConditionFalse, ephemeralclusterv1.KubeconfigFetchFailureReason, err.Error())
		return reconcile.Result{}, reconcile.TerminalError(err)
	}

	nss := corev1.NamespaceList{}
	if err := buildClient.List(ctx, &nss, ctrlruntimeclient.MatchingLabels{steps.LabelJobID: pj.Name}); err != nil {
		err := fmt.Errorf("get namespace for %s: %w", pj.Name, err)
		r.upsertCondition(ec, ephemeralclusterv1.ClusterReady, ephemeralclusterv1.ConditionFalse, ephemeralclusterv1.KubeconfigFetchFailureReason, err.Error())
		return reconcile.Result{RequeueAfter: r.polling()}, nil
	}

	// The NS hasn't been created yet, requeuing.
	if len(nss.Items) == 0 {
		r.upsertCondition(ec, ephemeralclusterv1.ClusterReady, ephemeralclusterv1.ConditionFalse, ephemeralclusterv1.KubeconfigFetchFailureReason, ephemeralclusterv1.CIOperatorNSNotFoundMsg)
		return reconcile.Result{RequeueAfter: r.polling()}, nil
	}

	ns := nss.Items[0].Name
	kubeconfigSecret := corev1.Secret{}
	// The secret is named after the test step name.
	if err := buildClient.Get(ctx, types.NamespacedName{Name: WaitTestStepName, Namespace: ns}, &kubeconfigSecret); err != nil {
		r.upsertCondition(ec, ephemeralclusterv1.ClusterReady, ephemeralclusterv1.ConditionFalse, ephemeralclusterv1.KubeconfigFetchFailureReason, err.Error())
		return reconcile.Result{RequeueAfter: r.polling()}, nil
	}

	kubeconfig, ok := kubeconfigSecret.Data["kubeconfig"]
	if !ok {
		// The kubeconfig takes time before being stored into the secret, requeuing.
		r.upsertCondition(ec, ephemeralclusterv1.ClusterReady, ephemeralclusterv1.ConditionFalse, ephemeralclusterv1.KubeconfigFetchFailureReason, ephemeralclusterv1.KubeconfigNotReadMsg)
		return reconcile.Result{RequeueAfter: r.polling()}, nil
	}

	ec.Status.Kubeconfig = string(kubeconfig)
	log.Info("kubeconfig fetched")
	r.upsertCondition(ec, ephemeralclusterv1.ClusterReady, ephemeralclusterv1.ConditionTrue, "", "")

	return reconcile.Result{}, nil
}

func (r *reconciler) updateEphemeralCluster(ctx context.Context, ec *ephemeralclusterv1.EphemeralCluster) error {
	if err := r.masterClient.Update(ctx, ec); err != nil {
		return fmt.Errorf("update ephemereal cluster: %w", err)
	}
	return nil
}

func (r *reconciler) upsertCondition(ec *ephemeralclusterv1.EphemeralCluster, t ephemeralclusterv1.EphemeralClusterConditionType, status ephemeralclusterv1.ConditionStatus, reason, msg string) {
	newCond := ephemeralclusterv1.EphemeralClusterCondition{
		Type:               t,
		Status:             status,
		LastTransitionTime: v1.NewTime(r.now()),
		Reason:             reason,
		Message:            msg,
	}

	for i := range ec.Status.Conditions {
		cond := ec.Status.Conditions[i]
		if cond.Type == newCond.Type {
			ec.Status.Conditions[i] = newCond
			return
		}
	}

	ec.Status.Conditions = append(ec.Status.Conditions, newCond)
}
