package steps

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/sirupsen/logrus"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/test-infra/prow/kube"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	hivev1 "github.com/openshift/hive/apis/hive/v1"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/results"
	"github.com/openshift/ci-tools/pkg/steps/loggingclient"
	"github.com/openshift/ci-tools/pkg/steps/utils"
	"github.com/openshift/ci-tools/pkg/util"
)

type clusterClaimStep struct {
	as           string
	clusterClaim *api.ClusterClaim
	hiveClient   ctrlruntimeclient.WithWatch
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
	waitForClaim := func(client ctrlruntimeclient.WithWatch, ns, name string, claim *hivev1.ClusterClaim, timeout time.Duration) error {
		logrus.WithFields(logrus.Fields{
			"namespace": ns,
			"name":      name,
		}).Trace("Waiting for claim to be running.")

		evaluatorFunc := func(obj runtime.Object) (bool, error) {
			switch clusterClaim := obj.(type) {
			case *hivev1.ClusterClaim:
				// https://github.com/openshift/hive/blob/a535d29c4d2dc4f7f9bf3f30098c69d9334bee2e/apis/hive/v1/clusterclaim_types.go#L18-L22
				for _, condition := range clusterClaim.Status.Conditions {
					if condition.Type == hivev1.ClusterRunningCondition && condition.Status == corev1.ConditionTrue {
						return true, nil
					}
				}
			default:
				return false, fmt.Errorf("clusterClaim/%v ns/%v got an event that did not contain a clusterClaim: %v", name, ns, obj)
			}
			return false, nil
		}

		return waitForConditionOnObject(ctx, client, ctrlruntimeclient.ObjectKey{Namespace: ns, Name: name}, &hivev1.ClusterClaimList{}, claim, evaluatorFunc, timeout)
	}

	clusterClaim, err := s.acquireCluster(ctx, waitForClaim)
	if err != nil {
		acquireErr := results.ForReason("acquiring_cluster_claim").ForError(err)
		// always attempt to delete claim if one exists
		var releaseErr error
		if clusterClaim != nil {
			releaseErr = results.ForReason("releasing_cluster_claim").ForError(s.releaseCluster(ctx, clusterClaim))
		}
		return aggregateWrappedErrorAndReleaseError(acquireErr, releaseErr)
	}

	wrappedErr := results.ForReason("executing_test").ForError(s.wrapped.Run(ctx))
	releaseErr := results.ForReason("releasing_cluster_claim").ForError(s.releaseCluster(ctx, clusterClaim))

	return aggregateWrappedErrorAndReleaseError(wrappedErr, releaseErr)
}

func (s *clusterClaimStep) acquireCluster(ctx context.Context, waitForClaim func(client ctrlruntimeclient.WithWatch, ns, name string, claim *hivev1.ClusterClaim, timeout time.Duration) error) (*hivev1.ClusterClaim, error) {
	clusterPools := &hivev1.ClusterPoolList{}
	listOption := ctrlruntimeclient.MatchingLabels{
		"product":      string(s.clusterClaim.Product),
		"version":      s.clusterClaim.Version,
		"architecture": string(s.clusterClaim.Architecture),
		"cloud":        string(s.clusterClaim.Cloud),
		"owner":        s.clusterClaim.Owner,
	}
	if err := s.hiveClient.List(ctx, clusterPools, listOption); err != nil {
		return nil, fmt.Errorf("failed to list cluster pools with list option %v: %w", listOption, err)
	}

	l := len(clusterPools.Items)
	if l == 0 {
		return nil, fmt.Errorf("failed to find a cluster pool providing the cluster: %v", listOption)
	} else if l > 1 {
		return nil, fmt.Errorf("find %d cluster pools providing the cluster (%v): should be only one", len(clusterPools.Items), listOption)
	}

	clusterPool := clusterPools.Items[0]
	claimName := s.jobSpec.ProwJobID
	claimNamespace := clusterPool.Namespace
	claim := &hivev1.ClusterClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      claimName,
			Namespace: claimNamespace,
			Labels: utils.SanitizeLabels(map[string]string{
				kube.ProwJobAnnotation: s.jobSpec.Job,
				kube.ProwBuildIDLabel:  s.jobSpec.BuildID,
			}),
		},
		Spec: hivev1.ClusterClaimSpec{
			ClusterPoolName: clusterPool.Name,
			Lifetime:        &metav1.Duration{Duration: 4 * time.Hour},
		},
	}
	if err := s.hiveClient.Create(ctx, claim); err != nil {
		return nil, fmt.Errorf("failed to created cluster claim %s in namespace %s: %w", claimName, claimNamespace, err)
	}
	logrus.Info("Waiting for the claimed cluster to be ready.")
	into := &hivev1.ClusterClaim{}
	if err := waitForClaim(s.hiveClient, claimNamespace, claimName, into, s.clusterClaim.Timeout.Duration); err != nil {
		return claim, fmt.Errorf("failed to wait for created cluster claim to become ready: %w", err)
	}
	claim = into
	logrus.Info("The claimed cluster is ready.")
	clusterDeployment := &hivev1.ClusterDeployment{}
	if err := s.hiveClient.Get(ctx, ctrlruntimeclient.ObjectKey{Name: claim.Spec.Namespace, Namespace: claim.Spec.Namespace}, clusterDeployment); err != nil {
		return claim, fmt.Errorf("failed to get cluster deployment %s in namespace %s: %w", claim.Spec.Namespace, claim.Spec.Namespace, err)
	}
	if clusterDeployment.Spec.ClusterMetadata == nil {
		return claim, fmt.Errorf("got nil cluster metadata from cluster deployment %s in namespace %s", claim.Spec.Namespace, claim.Spec.Namespace)
	}

	for src, dst := range map[string]string{clusterDeployment.Spec.ClusterMetadata.AdminKubeconfigSecretRef.Name: api.HiveAdminKubeconfigSecret, clusterDeployment.Spec.ClusterMetadata.AdminPasswordSecretRef.Name: api.HiveAdminPasswordSecret} {
		srcSecret := &corev1.Secret{}
		if err := s.hiveClient.Get(ctx, ctrlruntimeclient.ObjectKey{Name: src, Namespace: claim.Spec.Namespace}, srcSecret); err != nil {
			return claim, fmt.Errorf("failed to get secret %s in namespace %s: %w", clusterDeployment.Spec.ClusterMetadata.AdminKubeconfigSecretRef.Name, claim.Spec.Namespace, err)
		}
		dstNS := s.jobSpec.Namespace()
		dstSecret, err := getHiveSecret(srcSecret, dst, dstNS)
		if err != nil {
			return claim, fmt.Errorf("failed to mutate secret: %w", err)
		}
		if _, err := util.UpsertImmutableSecret(ctx, s.client, dstSecret); err != nil {
			return claim, fmt.Errorf("failed to upsert immutable secret %s in namespace %s: %w", dst, dstNS, err)
		}
	}
	return claim, nil
}

func getHiveSecret(src *corev1.Secret, name, namespace string) (*corev1.Secret, error) {
	var key string
	if name == api.HiveAdminKubeconfigSecret {
		key = api.HiveAdminKubeconfigSecretKey
	} else if name == api.HiveAdminPasswordSecret {
		key = api.HiveAdminPasswordSecretKey
	} else {
		return nil, fmt.Errorf("cannot mutate secret %s in namespace %s", src.Name, src.Namespace)
	}
	_, ok := src.Data[key]
	if !ok {
		return nil, fmt.Errorf("failed to find key %s in secret %s in namespace %s", key, src.Name, src.Namespace)
	}
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Data:       map[string][]byte{key: src.Data[key]},
		Type:       src.Type,
	}, nil
}

func (s *clusterClaimStep) releaseCluster(ctx context.Context, clusterClaim *hivev1.ClusterClaim) error {
	logrus.Infof("Releasing cluster claims for test %s", s.Name())
	logrus.WithField("clusterClaim.Namespace", clusterClaim.Namespace).WithField("clusterClaim.Name", clusterClaim.Name).Debug("Deleting cluster claim.")
	retry := 3
	for i := 0; i < retry; i++ {
		if err := s.hiveClient.Delete(ctx, clusterClaim); err != nil {
			logrus.WithField("clusterClaim.Name", clusterClaim.Name).WithField("i", i).WithField("clusterClaim.Namespace", clusterClaim.Namespace).Debug("Failed to delete cluster claim.")
			if i+1 < retry {
				continue
			}
			return fmt.Errorf("failed to delete cluster claim %s in namespace %s: %w", clusterClaim.Name, clusterClaim.Namespace, err)
		}
		break
	}
	return nil
}

func ClusterClaimStep(as string, clusterClaim *api.ClusterClaim, hiveClient ctrlruntimeclient.WithWatch, client loggingclient.LoggingClient, jobSpec *api.JobSpec, wrapped api.Step) api.Step {
	return &clusterClaimStep{
		as:           as,
		clusterClaim: clusterClaim,
		hiveClient:   hiveClient,
		client:       client,
		jobSpec:      jobSpec,
		wrapped:      wrapped,
	}
}
