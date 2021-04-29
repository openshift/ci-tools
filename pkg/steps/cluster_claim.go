package steps

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/sirupsen/logrus"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/test-infra/prow/kube"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	hivev1 "github.com/openshift/hive/apis/hive/v1"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/results"
	"github.com/openshift/ci-tools/pkg/steps/loggingclient"
	"github.com/openshift/ci-tools/pkg/util"
)

type clusterClaimStep struct {
	as           string
	clusterClaim *api.ClusterClaim
	hiveClient   ctrlruntimeclient.Client
	client       loggingclient.LoggingClient
	jobSpec      *api.JobSpec
	wrapped      api.Step
}

func (s clusterClaimStep) Inputs() (api.InputDefinition, error) {
	return s.wrapped.Inputs()
}

var NoHiveClientErr = errors.New("step claims a cluster without providing a Hive client")

func (s *clusterClaimStep) Validate() error {
	if s.hiveClient == nil {
		return NoHiveClientErr
	}
	return nil
}

func (s *clusterClaimStep) Name() string                        { return s.wrapped.Name() }
func (s *clusterClaimStep) Description() string                 { return s.wrapped.Description() }
func (s *clusterClaimStep) Requires() []api.StepLink            { return s.wrapped.Requires() }
func (s *clusterClaimStep) Creates() []api.StepLink             { return s.wrapped.Creates() }
func (s *clusterClaimStep) Objects() []ctrlruntimeclient.Object { return s.wrapped.Objects() }
func (s *clusterClaimStep) Provides() api.ParameterMap          { return s.wrapped.Provides() }

func (s *clusterClaimStep) Run(ctx context.Context) error {
	return results.ForReason("utilizing_cluster_claim").ForError(s.run(ctx))
}

func (s *clusterClaimStep) run(ctx context.Context) error {
	if s.clusterClaim == nil {
		// should never happen
		return fmt.Errorf("cannot claim a nil cluster")
	}
	clusterClaim, err := acquireCluster(ctx, *s.clusterClaim, s.hiveClient, s.client, *s.jobSpec)
	if err != nil {
		return err
	}

	wrappedErr := results.ForReason("executing_test").ForError(s.wrapped.Run(ctx))
	logrus.Infof("Releasing cluster claims for test %s", s.Name())
	releaseErr := results.ForReason("releasing_cluster_claim").ForError(releaseCluster(ctx, s.hiveClient, clusterClaim))

	return aggregateWrappedErrorAndReleaseError(wrappedErr, releaseErr)
}

func acquireCluster(ctx context.Context, clusterClaim api.ClusterClaim, hiveClient ctrlruntimeclient.Client, client loggingclient.LoggingClient, jobSpec api.JobSpec) (*hivev1.ClusterClaim, error) {
	clusterPools := &hivev1.ClusterPoolList{}
	listOption := ctrlruntimeclient.MatchingLabels{
		"product":      string(clusterClaim.Product),
		"version":      clusterClaim.Version,
		"architecture": string(clusterClaim.Architecture),
		"cloud":        string(clusterClaim.Cloud),
		"owner":        clusterClaim.Owner,
	}
	if err := hiveClient.List(ctx, clusterPools, listOption); err != nil {
		return nil, fmt.Errorf("failed to list cluster pools with list option %v: %w", listOption, err)
	}

	l := len(clusterPools.Items)
	if l == 0 {
		return nil, fmt.Errorf("failed to find a cluster pool providing the cluster: %v", listOption)
	} else if l > 1 {
		return nil, fmt.Errorf("find %d cluster pools providing the cluster (%v): should be only one", len(clusterPools.Items), listOption)
	}

	clusterPool := clusterPools.Items[0]
	claimName := jobSpec.ProwJobID
	claimNamespace := clusterPool.Namespace
	claim := &hivev1.ClusterClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      claimName,
			Namespace: claimNamespace,
			Labels: trimLabels(map[string]string{
				kube.ProwJobAnnotation: jobSpec.Job,
				kube.ProwBuildIDLabel:  jobSpec.BuildID,
			}),
		},
		Spec: hivev1.ClusterClaimSpec{
			ClusterPoolName: clusterPool.Name,
			Lifetime:        &metav1.Duration{Duration: 4 * time.Hour},
		},
	}
	if err := hiveClient.Create(ctx, claim); err != nil {
		return nil, fmt.Errorf("failed to created cluster claim %s in namespace %s: %w", claim.Name, claim.Namespace, err)
	}
	logrus.Info("Waiting for the claimed cluster to be ready.")
	claim = &hivev1.ClusterClaim{}
	if err := wait.Poll(15*time.Second, clusterClaim.Timeout.Duration, func() (bool, error) {
		if err := hiveClient.Get(ctx, ctrlruntimeclient.ObjectKey{Name: claimName, Namespace: claimNamespace}, claim); err != nil {
			return false, fmt.Errorf("failed to get cluster claim %s in namespace %s: %w", claim.Name, claim.Namespace, err)
		}
		// https://github.com/openshift/hive/blob/a535d29c4d2dc4f7f9bf3f30098c69d9334bee2e/apis/hive/v1/clusterclaim_types.go#L18-L22
		for _, condition := range claim.Status.Conditions {
			logrus.WithField("claim.Name", claim.Name).WithField("claim.Namespace", claim.Namespace).
				WithField("condition.Type", condition.Type).WithField("condition.Status", condition.Status).
				Debug("Checking the claim's status.")
			if condition.Type == hivev1.ClusterRunningCondition && condition.Status == corev1.ConditionTrue {
				return true, nil
			}
		}
		return false, nil
	}); err != nil {
		return nil, fmt.Errorf("failed to wait for created cluster claim to become running: %w", err)
	}
	logrus.Info("The claimed cluster is ready.")
	clusterDeploymentName := claim.Spec.Namespace
	clusterDeploymentNamespace := claim.Spec.Namespace
	clusterDeployment := &hivev1.ClusterDeployment{}
	if err := hiveClient.Get(ctx, ctrlruntimeclient.ObjectKey{Name: clusterDeploymentName, Namespace: clusterDeploymentNamespace}, clusterDeployment); err != nil {
		return nil, fmt.Errorf("failed to get cluster deployment %s in namespace %s: %w", clusterDeploymentName, clusterDeploymentNamespace, err)
	}
	if clusterDeployment.Spec.ClusterMetadata == nil {
		return nil, fmt.Errorf("got nil cluster metadata from cluster deployment %s in namespace %s", clusterDeploymentName, clusterDeploymentNamespace)
	}
	kubeconfigSecretName := clusterDeployment.Spec.ClusterMetadata.AdminKubeconfigSecretRef.Name
	passwordSecretName := clusterDeployment.Spec.ClusterMetadata.AdminPasswordSecretRef.Name

	for src, dst := range map[string]string{kubeconfigSecretName: api.HiveAdminKubeconfigSecret, passwordSecretName: api.HiveAdminPasswordSecret} {
		srcSecret := &corev1.Secret{}
		if err := hiveClient.Get(ctx, ctrlruntimeclient.ObjectKey{Name: src, Namespace: clusterDeploymentNamespace}, srcSecret); err != nil {
			return nil, fmt.Errorf("failed to get secret %s in namespace %s: %w", kubeconfigSecretName, clusterDeploymentNamespace, err)
		}
		dstSecret, err := mutate(srcSecret, dst, jobSpec.Namespace())
		if err != nil {
			return nil, fmt.Errorf("failed to mutate secret: %w", err)
		}
		if _, err := util.UpsertImmutableSecret(ctx, client, dstSecret); err != nil {
			return nil, fmt.Errorf("failed to upsert immutable secret %s in namespace %s: %w", dstSecret.Name, dstSecret.Namespace, err)
		}
	}
	return claim, nil
}

func mutate(secret *corev1.Secret, name, namespace string) (*corev1.Secret, error) {
	var key string
	if name == api.HiveAdminKubeconfigSecret {
		key = api.HiveAdminKubeconfigSecretKey
	} else if name == api.HiveAdminPasswordSecret {
		key = api.HiveAdminPasswordSecretKey
	} else {
		return nil, fmt.Errorf("cannot mutate secret %s in namespace %s", secret.Name, secret.Namespace)
	}
	_, ok := secret.Data[key]
	if !ok {
		return nil, fmt.Errorf("failed to find key %s in secret %s in namespace %s", key, secret.Name, secret.Namespace)
	}
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Data:       map[string][]byte{key: secret.Data[key]},
		Type:       secret.Type,
	}, nil
}

func releaseCluster(ctx context.Context, hiveClient ctrlruntimeclient.Client, clusterClaim *hivev1.ClusterClaim) error {
	logrus.WithField("clusterClaim.Namespace", clusterClaim.Namespace).WithField("clusterClaim.Name", clusterClaim.Name).Debug("Deleting cluster claim.")
	if err := hiveClient.Delete(ctx, clusterClaim); err != nil {
		logrus.WithField("clusterClaim.Name", clusterClaim.Name).WithField("clusterClaim.Namespace", clusterClaim.Namespace).Debug("Failed to delete cluster claim.")
		return fmt.Errorf("failed to delete cluster claim %s in namespace %s: %w", clusterClaim.Name, clusterClaim.Namespace, err)
	}
	return nil
}

func ClusterClaimStep(as string, clusterClaim *api.ClusterClaim, hiveClient ctrlruntimeclient.Client, client loggingclient.LoggingClient, jobSpec *api.JobSpec, wrapped api.Step) api.Step {
	ret := clusterClaimStep{
		as:           as,
		clusterClaim: clusterClaim,
		hiveClient:   hiveClient,
		client:       client,
		jobSpec:      jobSpec,
		wrapped:      wrapped,
	}
	return &ret
}
