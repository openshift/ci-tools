package steps

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/sirupsen/logrus"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/test-infra/prow/kube"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	hivev1 "github.com/openshift/hive/apis/hive/v1"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/results"
	"github.com/openshift/ci-tools/pkg/secrets"
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
	censor       *secrets.DynamicCensor
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
	// Make sure we release the claim no matter what. This is a very brute force solution,
	// that works even if wrapped() blocks and doesn't correctly end when ctx is cancelled.
	if clusterClaim != nil {
		go func() {
			<-ctx.Done()
			if err := s.releaseCluster(cleanupCtx, clusterClaim, false); err != nil {
				logrus.WithError(err).Error("failed to release cluster claim")
			}
		}()
	}
	if err != nil {
		acquireErr := results.ForReason("acquiring_cluster_claim").ForError(err)
		// always attempt to delete claim if one exists
		var releaseErr error
		if clusterClaim != nil {
			releaseErr = results.ForReason("releasing_cluster_claim").ForError(s.releaseCluster(cleanupCtx, clusterClaim, true))
		}
		return aggregateWrappedErrorAndReleaseError(acquireErr, releaseErr)
	}

	wrappedErr := results.ForReason("executing_test").ForError(s.wrapped.Run(ctx))
	releaseErr := results.ForReason("releasing_cluster_claim").ForError(s.releaseCluster(cleanupCtx, clusterClaim, false))

	return aggregateWrappedErrorAndReleaseError(wrappedErr, releaseErr)
}

func (s *clusterClaimStep) acquireCluster(ctx context.Context, waitForClaim func(client ctrlruntimeclient.WithWatch, ns, name string, claim *hivev1.ClusterClaim, timeout time.Duration) error) (*hivev1.ClusterClaim, error) {
	clusterPool, err := utils.ClusterPoolFromClaim(ctx, s.clusterClaim, s.hiveClient)
	if err != nil {
		return nil, err
	}
	logrus.Infof("Claiming cluster from pool %s/%s owned by %s", clusterPool.Namespace, clusterPool.Name, clusterPool.Labels["owner"])

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
	logrus.Infof("Waiting for cluster claim %s/%s to be fulfilled.", claimNamespace, claimName)
	claimStart := time.Now()
	into := &hivev1.ClusterClaim{}
	if err := waitForClaim(s.hiveClient, claimNamespace, claimName, into, s.clusterClaim.Timeout.Duration); err != nil {
		return claim, fmt.Errorf("failed to wait for the created cluster claim to become ready: %w", err)
	}
	claim = into
	logrus.Infof("The claimed cluster %s is ready after %s.", claim.Spec.Namespace, time.Since(claimStart).Truncate(time.Second))
	clusterDeployment := &hivev1.ClusterDeployment{}
	if err := s.hiveClient.Get(ctx, ctrlruntimeclient.ObjectKey{Name: claim.Spec.Namespace, Namespace: claim.Spec.Namespace}, clusterDeployment); err != nil {
		return claim, fmt.Errorf("failed to get cluster deployment %s in namespace %s: %w", claim.Spec.Namespace, claim.Spec.Namespace, err)
	}
	if clusterDeployment.Spec.ClusterMetadata == nil {
		return claim, fmt.Errorf("got nil cluster metadata from cluster deployment %s in namespace %s", claim.Spec.Namespace, claim.Spec.Namespace)
	}
	if clusterDeployment.Spec.ClusterMetadata.AdminPasswordSecretRef == nil {
		return claim, fmt.Errorf("got nil admin password secret reference from cluster deployment %s in namespace %s", claim.Spec.Namespace, claim.Spec.Namespace)
	}

	for src, dst := range map[string]string{clusterDeployment.Spec.ClusterMetadata.AdminKubeconfigSecretRef.Name: api.HiveAdminKubeconfigSecret, clusterDeployment.Spec.ClusterMetadata.AdminPasswordSecretRef.Name: api.HiveAdminPasswordSecret} {
		srcSecret := &corev1.Secret{}
		if err := s.hiveClient.Get(ctx, ctrlruntimeclient.ObjectKey{Name: src, Namespace: claim.Spec.Namespace}, srcSecret); err != nil {
			return claim, fmt.Errorf("failed to get secret %s in namespace %s: %w", clusterDeployment.Spec.ClusterMetadata.AdminKubeconfigSecretRef.Name, claim.Spec.Namespace, err)
		}
		dstNS := s.jobSpec.Namespace()
		dstSecret, err := getHiveSecret(srcSecret, dst, dstNS, s.as)
		if err != nil {
			return claim, fmt.Errorf("failed to mutate secret: %w", err)
		}
		if _, err := util.UpsertImmutableSecret(ctx, s.client, dstSecret); err != nil {
			return claim, fmt.Errorf("failed to upsert immutable secret %s in namespace %s: %w", dst, dstNS, err)
		}
	}
	return claim, nil
}

func namePerTest(name, testName string) string {
	return strings.ReplaceAll(utils.Trim63(fmt.Sprintf("%s-%s", testName, name)), ".", "-")
}

func getHiveSecret(src *corev1.Secret, name, namespace, testName string) (*corev1.Secret, error) {
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
		ObjectMeta: metav1.ObjectMeta{Name: namePerTest(name, testName), Namespace: namespace},
		Data:       map[string][]byte{key: src.Data[key]},
		Type:       src.Type,
	}, nil
}

func (s *clusterClaimStep) releaseCluster(ctx context.Context, clusterClaim *hivev1.ClusterClaim, printConditions bool) error {
	logger := logrus.WithField("clusterClaim.Namespace", clusterClaim.Namespace).WithField("clusterClaim.Name", clusterClaim.Name)
	if err := s.saveArtifacts(ctx, clusterClaim.Namespace, clusterClaim.Name, printConditions); err != nil {
		// logging the error without failing the test
		logger.WithError(err).Error("Failed to save artifacts before releasing the claimed cluster")
	}
	logrus.Infof("Releasing cluster claims for test %s", s.Name())
	logger.Debug("Deleting cluster claim.")
	retry := 3
	for i := 0; i < retry; i++ {
		if err := s.hiveClient.Delete(ctx, clusterClaim); err != nil && !apierrors.IsNotFound(err) {
			logger.WithField("i", i).Debug("Failed to delete cluster claim.")
			if i+1 < retry {
				continue
			}
			return fmt.Errorf("failed to delete cluster claim %s in namespace %s: %w", clusterClaim.Name, clusterClaim.Namespace, err)
		}
		break
	}
	return nil
}

func (s *clusterClaimStep) saveArtifacts(ctx context.Context, namespace, name string, printConditions bool) error {
	logrus.WithField("clusterClaim.Namespace", namespace).WithField("clusterClaim.Name", name).Debug("Saving artifacts.")
	var errs []error
	namespaceDir := api.NamespaceDir
	claim := hivev1.ClusterClaim{}
	if err := s.saveObjectAsArtifact(
		ctx,
		ctrlruntimeclient.ObjectKey{Namespace: namespace, Name: name},
		&claim,
		"cluster claim",
		filepath.Join(namespaceDir, "clusterClaim.json"),
	); err != nil {
		errs = append(errs, err)
	}
	if printConditions {
		var relevantConditions []hivev1.ClusterClaimCondition
		for _, condition := range claim.Status.Conditions {
			if condition.Status == corev1.ConditionUnknown || condition.Reason == "Initialized" {
				continue
			}
			relevantConditions = append(relevantConditions, condition)
		}
		builder := &strings.Builder{}
		_, _ = builder.WriteString(fmt.Sprintf("Found %d conditions for ClusterClaim:\n", len(relevantConditions)))
		sort.Slice(relevantConditions, func(i, j int) bool {
			return relevantConditions[i].LastTransitionTime.Before(&relevantConditions[j].LastTransitionTime)
		})
		for _, condition := range relevantConditions {
			_, _ = builder.WriteString(fmt.Sprintf("\n  *[%s]%s: %s", condition.LastTransitionTime.Format(time.RFC3339), condition.Reason, condition.Message))
		}
		_, _ = builder.WriteString("\n")
		logrus.Warn(builder.String())
	}

	if claim.Spec.Namespace != "" {
		clusterDeployment := hivev1.ClusterDeployment{}
		if err := s.saveObjectAsArtifact(
			ctx,
			ctrlruntimeclient.ObjectKey{Namespace: claim.Spec.Namespace, Name: claim.Spec.Namespace},
			&clusterDeployment,
			"cluster deployment",
			filepath.Join(namespaceDir, "clusterDeployment.json"),
		); err != nil {
			errs = append(errs, err)
		}
		if printConditions {
			builder := &strings.Builder{}
			_, _ = builder.WriteString(fmt.Sprintf("Found %d conditions for ClusterDeployment:", len(clusterDeployment.Status.Conditions)))
			sort.Slice(clusterDeployment.Status.Conditions, func(i, j int) bool {
				return clusterDeployment.Status.Conditions[i].LastTransitionTime.Before(&clusterDeployment.Status.Conditions[j].LastTransitionTime)
			})
			for _, condition := range clusterDeployment.Status.Conditions {
				if condition.Status == corev1.ConditionUnknown || condition.Reason == "Initialized" {
					continue
				}
				_, _ = builder.WriteString(fmt.Sprintf("\n[%s] %s: %s", condition.LastTransitionTime.Format(time.RFC3339), condition.Reason, condition.Message))
			}
			logrus.Warn(builder.String())
		}
	}

	return utilerrors.NewAggregate(errs)
}

func (s *clusterClaimStep) saveObjectAsArtifact(ctx context.Context, key ctrlruntimeclient.ObjectKey, obj ctrlruntimeclient.Object, kind, path string) error {
	if err := s.hiveClient.Get(ctx, key, obj); err != nil {
		return fmt.Errorf("failed to get %s %s in namespace %s: %w", kind, key.Name, key.Namespace, err)
	}
	data, err := json.MarshalIndent(obj, "", "  ")
	if err != nil {
		return err
	}
	return api.SaveArtifact(s.censor, path, data)
}

func ClusterClaimStep(as string, clusterClaim *api.ClusterClaim, hiveClient ctrlruntimeclient.WithWatch, client loggingclient.LoggingClient, jobSpec *api.JobSpec, wrapped api.Step, censor *secrets.DynamicCensor) api.Step {
	return &clusterClaimStep{
		as:           as,
		clusterClaim: clusterClaim,
		hiveClient:   hiveClient,
		client:       client,
		jobSpec:      jobSpec,
		wrapped:      wrapped,
		censor:       censor,
	}
}
