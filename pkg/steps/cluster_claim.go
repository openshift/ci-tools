package steps

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/sirupsen/logrus"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation"
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
	clusterClaim *api.ClusterClaim
	hiveClient   ctrlruntimeclient.Client
	client       loggingclient.LoggingClient
	jobSpec      *api.JobSpec
}

func (s clusterClaimStep) Inputs() (api.InputDefinition, error) {
	return nil, nil
}

var NoHiveClientErr = errors.New("step claims a cluster without providing a Hive client")

func (s *clusterClaimStep) Validate() error {
	if s.hiveClient == nil {
		return NoHiveClientErr
	}
	return nil
}

func (s *clusterClaimStep) Run(ctx context.Context) error {
	return results.ForReason("cluster_claim").ForError(s.run(ctx))
}

func (s *clusterClaimStep) Name() string {
	return fmt.Sprintf("cluster_claim:%s_%s_%s_%s_%s", s.clusterClaim.Product, s.clusterClaim.Version, s.clusterClaim.Architecture, s.clusterClaim.Cloud, s.clusterClaim.Owner)
}

func (s *clusterClaimStep) Description() string {
	return fmt.Sprintf("Claim a(n) %s cluster of version %s and architecture %s on cloud %s with %s's account", s.clusterClaim.Product, s.clusterClaim.Version, s.clusterClaim.Architecture, s.clusterClaim.Cloud, s.clusterClaim.Owner)
}

func (s *clusterClaimStep) Requires() []api.StepLink { return nil }

func (s *clusterClaimStep) Creates() []api.StepLink {
	return []api.StepLink{api.ClusterClaimLink(s.Name())}
}

func (s *clusterClaimStep) Provides() api.ParameterMap { return nil }

func (s *clusterClaimStep) Objects() []ctrlruntimeclient.Object { return s.client.Objects() }

func (s *clusterClaimStep) run(ctx context.Context) error {
	if s.clusterClaim == nil {
		// should never happen
		return fmt.Errorf("cannot claim a nil cluster")
	}
	clusterPools := &hivev1.ClusterPoolList{}
	listOption := ctrlruntimeclient.MatchingLabels{
		"product":      string(s.clusterClaim.Product),
		"version":      s.clusterClaim.Version,
		"architecture": string(s.clusterClaim.Architecture),
		"cloud":        string(s.clusterClaim.Cloud),
		"owner":        s.clusterClaim.Owner,
	}
	if err := s.hiveClient.List(ctx, clusterPools, listOption); err != nil {
		return fmt.Errorf("failed to list cluster pools with list option %v: %w", listOption, err)
	}

	l := len(clusterPools.Items)
	if l == 0 {
		return fmt.Errorf("failed to find a cluster pool providing the cluster: %v", listOption)
	} else if l > 1 {
		return fmt.Errorf("find %d cluster pools providing the cluster (%v): should be only one", len(clusterPools.Items), listOption)
	}

	clusterPool := clusterPools.Items[0]
	claimName := s.jobSpec.ProwJobID
	claimNamespace := clusterPool.Namespace
	// https://github.com/kubernetes/test-infra/blob/d14589b797fae426bb70cea4843fa46be50c6739/prow/pod-utils/decorate/podspec.go#L99-L101
	jobNameForLabel := s.jobSpec.Job
	if len(jobNameForLabel) > validation.LabelValueMaxLength {
		jobNameForLabel = strings.TrimRight(s.jobSpec.Job[:validation.LabelValueMaxLength], ".-")
	}
	claim := &hivev1.ClusterClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      claimName,
			Namespace: claimNamespace,
			Labels: map[string]string{
				kube.ProwJobAnnotation: jobNameForLabel,
				kube.ProwBuildIDLabel:  s.jobSpec.BuildID,
			},
		},
		Spec: hivev1.ClusterClaimSpec{
			ClusterPoolName: clusterPool.Name,
			Lifetime:        &metav1.Duration{Duration: 4 * time.Hour},
		},
	}
	if err := s.hiveClient.Create(ctx, claim); err != nil {
		return fmt.Errorf("failed to created cluster claim %s in namespace %s: %w", claim.Name, claim.Namespace, err)
	}
	logrus.WithField("claim.Name", claim.Name).WithField("claim.Namespace", claim.Namespace).Info("waiting for the claimed cluster to be ready ...")
	claim = &hivev1.ClusterClaim{}
	if err := wait.Poll(15*time.Second, s.clusterClaim.Timeout.Duration, func() (bool, error) {
		if err := s.hiveClient.Get(ctx, ctrlruntimeclient.ObjectKey{Name: claimName, Namespace: claimNamespace}, claim); err != nil {
			return false, fmt.Errorf("failed to get cluster claim %s in namespace %s: %w", claim.Name, claim.Namespace, err)
		}
		// https://github.com/openshift/hive/blob/a535d29c4d2dc4f7f9bf3f30098c69d9334bee2e/apis/hive/v1/clusterclaim_types.go#L18-L22
		for _, condition := range claim.Status.Conditions {
			if condition.Type == hivev1.ClusterRunningCondition && condition.Status == corev1.ConditionTrue {
				return true, nil
			}
		}
		return false, nil
	}); err != nil {
		return fmt.Errorf("failed to wait for created cluster claim to become running: %w", err)
	}
	clusterDeploymentName := claim.Spec.Namespace
	clusterDeploymentNamespace := claim.Spec.Namespace
	clusterDeployment := &hivev1.ClusterDeployment{}
	if err := s.hiveClient.Get(ctx, ctrlruntimeclient.ObjectKey{Name: clusterDeploymentName, Namespace: clusterDeploymentNamespace}, clusterDeployment); err != nil {
		return fmt.Errorf("failed to get cluster deployment %s in namespace %s: %w", clusterDeploymentName, clusterDeploymentNamespace, err)
	}
	if clusterDeployment.Spec.ClusterMetadata == nil {
		return fmt.Errorf("got nil cluster metadata from cluster deployment %s in namespace %s", clusterDeploymentName, clusterDeploymentNamespace)
	}
	kubeconfigSecretName := clusterDeployment.Spec.ClusterMetadata.AdminKubeconfigSecretRef.Name
	passwordSecretName := clusterDeployment.Spec.ClusterMetadata.AdminPasswordSecretRef.Name

	for src, dst := range map[string]string{kubeconfigSecretName: api.HiveAdminKubeconfigSecret, passwordSecretName: api.HiveAdminPasswordSecret} {
		srcSecret := &corev1.Secret{}
		if err := s.hiveClient.Get(ctx, ctrlruntimeclient.ObjectKey{Name: src, Namespace: clusterDeploymentNamespace}, srcSecret); err != nil {
			return fmt.Errorf("failed to get secret %s in namespace %s: %w", kubeconfigSecretName, clusterDeploymentNamespace, err)
		}
		dstSecret, err := mutate(srcSecret, dst, s.jobSpec.Namespace())
		if err != nil {
			return fmt.Errorf("failed to mutate secret: %w", err)
		}
		if _, err := util.UpsertImmutableSecret(ctx, s.client, dstSecret); err != nil {
			return fmt.Errorf("failed to upsert immutable secret %s in namespace %s: %w", dstSecret.Name, dstSecret.Namespace, err)
		}
	}
	return nil
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

func ClusterClaimStep(clusterClaim *api.ClusterClaim, hiveClient ctrlruntimeclient.Client, client loggingclient.LoggingClient, jobSpec *api.JobSpec) api.Step {
	ret := clusterClaimStep{
		clusterClaim: clusterClaim,
		hiveClient:   hiveClient,
		client:       client,
		jobSpec:      jobSpec,
	}
	return &ret
}
