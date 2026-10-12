package ephemeralcluster

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	ephemeralclusterv1 "github.com/openshift/ci-tools/pkg/api/ephemeralcluster/v1"
)

func TestComputePhaseTransition(t *testing.T) {
	t.Parallel()

	creationTime := time.Date(2025, time.April, 2, 12, 0, 0, 0, time.UTC)
	condition := func(conditionType string, status metav1.ConditionStatus, reason string, offset time.Duration) metav1.Condition {
		return metav1.Condition{
			Type:               conditionType,
			Status:             status,
			Reason:             reason,
			LastTransitionTime: metav1.NewTime(creationTime.Add(offset)),
		}
	}

	pjCreated := condition(ephemeralclusterv1.ProwJobCreated, metav1.ConditionTrue, ephemeralclusterv1.ProwJobProperlyCreatedReason, time.Minute)
	clusterReady := condition(ephemeralclusterv1.ClusterReady, metav1.ConditionTrue, ephemeralclusterv1.CredentialsReadyReason, 10*time.Minute)
	testCompleted := condition(ephemeralclusterv1.TestCompleted, metav1.ConditionTrue, ephemeralclusterv1.TestDoneReason, 20*time.Minute)
	pjSucceeded := condition(ephemeralclusterv1.ProwJobCompleted, metav1.ConditionTrue, ephemeralclusterv1.ProwJobCompletedReason, 25*time.Minute)
	pjFailed := condition(ephemeralclusterv1.ProwJobCompleted, metav1.ConditionTrue, ephemeralclusterv1.ProwJobFailureReason, 25*time.Minute)
	validationFailed := condition(ephemeralclusterv1.ProwJobCreated, metav1.ConditionFalse, ephemeralclusterv1.EphemeralClusterValidationFailureReason, time.Minute)

	type testCase struct {
		name         string
		currentPhase ephemeralclusterv1.EphemeralClusterPhase
		conditions   []metav1.Condition
		want         phaseTransition
	}
	testCases := []testCase{
		{
			name: "initialize without conditions",
			want: phaseTransition{
				phase:                        ephemeralclusterv1.EphemeralClusterInitializing,
				updatePhaseTransitionCounter: true,
			},
		},
		{
			name:       "ignore unrelated conditions",
			conditions: []metav1.Condition{condition("Unrelated", metav1.ConditionTrue, "UnrelatedReason", time.Minute)},
			want: phaseTransition{
				phase:                        ephemeralclusterv1.EphemeralClusterInitializing,
				updatePhaseTransitionCounter: true,
			},
		},
		{
			name: "false and unknown milestones do not advance the phase",
			conditions: []metav1.Condition{
				condition(ephemeralclusterv1.ProwJobCreated, metav1.ConditionUnknown, ephemeralclusterv1.CouldNotPersistProwJobReason, time.Minute),
				condition(ephemeralclusterv1.ClusterReady, metav1.ConditionFalse, ephemeralclusterv1.SecretsFetchFailureReason, 10*time.Minute),
				condition(ephemeralclusterv1.TestCompleted, metav1.ConditionUnknown, ephemeralclusterv1.CreateTestCompletedSecretFailureReason, 20*time.Minute),
				condition(ephemeralclusterv1.ProwJobCompleted, metav1.ConditionFalse, ephemeralclusterv1.ProwJobFailureReason, 25*time.Minute),
			},
			want: phaseTransition{
				phase:                        ephemeralclusterv1.EphemeralClusterInitializing,
				updatePhaseTransitionCounter: true,
			},
		},
		{
			name:         "validation failure is terminal",
			currentPhase: ephemeralclusterv1.EphemeralClusterInitializing,
			conditions:   []metav1.Condition{validationFailed},
			want: phaseTransition{
				phase:                        ephemeralclusterv1.EphemeralClusterInitializationFailed,
				updatePhaseTransitionCounter: true,
				updateProvisionDuration:      true,
			},
		},
		{
			name:         "job generation failure is terminal",
			currentPhase: ephemeralclusterv1.EphemeralClusterInitializing,
			conditions: []metav1.Condition{
				condition(ephemeralclusterv1.ProwJobCreated, metav1.ConditionFalse, ephemeralclusterv1.CIOperatorJobsGenerateFailureReason, time.Minute),
			},
			want: phaseTransition{
				phase:                        ephemeralclusterv1.EphemeralClusterInitializationFailed,
				updatePhaseTransitionCounter: true,
				updateProvisionDuration:      true,
			},
		},
		{
			name:         "multiple bound jobs are terminal even with unknown status",
			currentPhase: ephemeralclusterv1.EphemeralClusterInitializing,
			conditions: []metav1.Condition{
				condition(ephemeralclusterv1.ProwJobCreated, metav1.ConditionUnknown, ephemeralclusterv1.TooManyProwJobsBoundReason, time.Minute),
			},
			want: phaseTransition{
				phase:                        ephemeralclusterv1.EphemeralClusterInitializationFailed,
				updatePhaseTransitionCounter: true,
				updateProvisionDuration:      true,
			},
		},
		{
			name: "job persistence failure allows another attempt",
			conditions: []metav1.Condition{
				condition(ephemeralclusterv1.ProwJobCreated, metav1.ConditionFalse, ephemeralclusterv1.CouldNotPersistProwJobReason, time.Minute),
			},
			want: phaseTransition{
				phase:                        ephemeralclusterv1.EphemeralClusterInitializing,
				updatePhaseTransitionCounter: true,
			},
		},
		{
			name:         "created job starts provisioning",
			currentPhase: ephemeralclusterv1.EphemeralClusterInitializing,
			conditions:   []metav1.Condition{pjCreated},
			want: phaseTransition{
				phase:                        ephemeralclusterv1.EphemeralClusterProvisioning,
				duration:                     ptr.To(time.Minute),
				updatePhaseTransitionCounter: true,
			},
		},
		{
			name:         "ready cluster completes provisioning",
			currentPhase: ephemeralclusterv1.EphemeralClusterProvisioning,
			conditions:   []metav1.Condition{pjCreated, clusterReady},
			want: phaseTransition{
				phase:                        ephemeralclusterv1.EphemeralClusterReady,
				duration:                     ptr.To(10 * time.Minute),
				updatePhaseTransitionCounter: true,
				updateProvisionDuration:      true,
			},
		},
		{
			name:         "completed test starts deprovisioning",
			currentPhase: ephemeralclusterv1.EphemeralClusterReady,
			conditions:   []metav1.Condition{pjCreated, clusterReady, testCompleted},
			want: phaseTransition{
				phase:                        ephemeralclusterv1.EphemeralClusterDeprovisioning,
				updatePhaseTransitionCounter: true,
			},
		},
		{
			name:         "job failure before readiness fails provisioning",
			currentPhase: ephemeralclusterv1.EphemeralClusterProvisioning,
			conditions:   []metav1.Condition{pjCreated, pjFailed},
			want: phaseTransition{
				phase:                        ephemeralclusterv1.EphemeralClusterProvisioningFailed,
				duration:                     ptr.To(25 * time.Minute),
				updatePhaseTransitionCounter: true,
				updateProvisionDuration:      true,
			},
		},
		{
			name:         "job success before readiness is deprovisioned",
			currentPhase: ephemeralclusterv1.EphemeralClusterProvisioning,
			conditions:   []metav1.Condition{pjCreated, pjSucceeded},
			want: phaseTransition{
				phase:                        ephemeralclusterv1.EphemeralClusterDeprovisioned,
				duration:                     ptr.To(25 * time.Minute),
				updatePhaseTransitionCounter: true,
				updateDeprovisionDuration:    true,
			},
		},
		{
			name:         "job failure after readiness fails deprovisioning",
			currentPhase: ephemeralclusterv1.EphemeralClusterReady,
			conditions:   []metav1.Condition{pjCreated, clusterReady, pjFailed},
			want: phaseTransition{
				phase:                        ephemeralclusterv1.EphemeralClusterDeprovisioningFailure,
				duration:                     ptr.To(15 * time.Minute),
				updatePhaseTransitionCounter: true,
				updateDeprovisionDuration:    true,
			},
		},
		{
			name:         "job success after readiness is deprovisioned",
			currentPhase: ephemeralclusterv1.EphemeralClusterReady,
			conditions:   []metav1.Condition{pjCreated, clusterReady, pjSucceeded},
			want: phaseTransition{
				phase:                        ephemeralclusterv1.EphemeralClusterDeprovisioned,
				duration:                     ptr.To(15 * time.Minute),
				updatePhaseTransitionCounter: true,
				updateDeprovisionDuration:    true,
			},
		},
		{
			name:         "job failure after the test fails deprovisioning",
			currentPhase: ephemeralclusterv1.EphemeralClusterDeprovisioning,
			conditions:   []metav1.Condition{pjCreated, clusterReady, testCompleted, pjFailed},
			want: phaseTransition{
				phase:                        ephemeralclusterv1.EphemeralClusterDeprovisioningFailure,
				duration:                     ptr.To(5 * time.Minute),
				updatePhaseTransitionCounter: true,
				updateDeprovisionDuration:    true,
			},
		},
		{
			name:         "job success after the test completes deprovisioning",
			currentPhase: ephemeralclusterv1.EphemeralClusterDeprovisioning,
			conditions:   []metav1.Condition{pjCreated, clusterReady, testCompleted, pjSucceeded},
			want: phaseTransition{
				phase:                        ephemeralclusterv1.EphemeralClusterDeprovisioned,
				duration:                     ptr.To(5 * time.Minute),
				updatePhaseTransitionCounter: true,
				updateDeprovisionDuration:    true,
			},
		},
		{
			name:         "condition ordering does not affect the transition",
			currentPhase: ephemeralclusterv1.EphemeralClusterDeprovisioning,
			conditions:   []metav1.Condition{pjSucceeded, testCompleted, clusterReady, pjCreated},
			want: phaseTransition{
				phase:                        ephemeralclusterv1.EphemeralClusterDeprovisioned,
				duration:                     ptr.To(5 * time.Minute),
				updatePhaseTransitionCounter: true,
				updateDeprovisionDuration:    true,
			},
		},
		{
			name:         "false readiness and test completion leave provisioning active",
			currentPhase: ephemeralclusterv1.EphemeralClusterInitializing,
			conditions: []metav1.Condition{
				pjCreated,
				condition(ephemeralclusterv1.ClusterReady, metav1.ConditionFalse, ephemeralclusterv1.SecretsFetchFailureReason, 10*time.Minute),
				condition(ephemeralclusterv1.TestCompleted, metav1.ConditionFalse, ephemeralclusterv1.CreateTestCompletedSecretFailureReason, 20*time.Minute),
				condition(ephemeralclusterv1.ProwJobCompleted, metav1.ConditionFalse, ephemeralclusterv1.ProwJobFailureReason, 25*time.Minute),
			},
			want: phaseTransition{
				phase:                        ephemeralclusterv1.EphemeralClusterProvisioning,
				duration:                     ptr.To(time.Minute),
				updatePhaseTransitionCounter: true,
			},
		},
		{
			name:         "unknown job completion leaves a ready cluster active",
			currentPhase: ephemeralclusterv1.EphemeralClusterProvisioning,
			conditions: []metav1.Condition{
				pjCreated, clusterReady,
				condition(ephemeralclusterv1.ProwJobCompleted, metav1.ConditionUnknown, ephemeralclusterv1.ProwJobCompletedReason, 25*time.Minute),
			},
			want: phaseTransition{
				phase:                        ephemeralclusterv1.EphemeralClusterReady,
				duration:                     ptr.To(10 * time.Minute),
				updatePhaseTransitionCounter: true,
				updateProvisionDuration:      true,
			},
		},
		{
			name: "zero provisioning duration remains valid",
			conditions: []metav1.Condition{
				condition(ephemeralclusterv1.ClusterReady, metav1.ConditionTrue, ephemeralclusterv1.CredentialsReadyReason, 0),
			},
			want: phaseTransition{
				phase:                        ephemeralclusterv1.EphemeralClusterReady,
				duration:                     ptr.To(time.Duration(0)),
				updatePhaseTransitionCounter: true,
				updateProvisionDuration:      true,
			},
		},
		{
			name:         "zero deprovisioning duration remains valid",
			currentPhase: ephemeralclusterv1.EphemeralClusterDeprovisioning,
			conditions: []metav1.Condition{
				condition(ephemeralclusterv1.TestCompleted, metav1.ConditionTrue, ephemeralclusterv1.TestDoneReason, 25*time.Minute),
				pjSucceeded,
			},
			want: phaseTransition{
				phase:                        ephemeralclusterv1.EphemeralClusterDeprovisioned,
				duration:                     ptr.To(time.Duration(0)),
				updatePhaseTransitionCounter: true,
				updateDeprovisionDuration:    true,
			},
		},
		{
			name: "job creation before cluster creation has no duration",
			conditions: []metav1.Condition{
				condition(ephemeralclusterv1.ProwJobCreated, metav1.ConditionTrue, ephemeralclusterv1.ProwJobProperlyCreatedReason, -time.Minute),
			},
			want: phaseTransition{
				phase:                        ephemeralclusterv1.EphemeralClusterProvisioning,
				updatePhaseTransitionCounter: true,
			},
		},
		{
			name: "readiness before cluster creation still counts the transition",
			conditions: []metav1.Condition{
				condition(ephemeralclusterv1.ClusterReady, metav1.ConditionTrue, ephemeralclusterv1.CredentialsReadyReason, -time.Minute),
			},
			want: phaseTransition{
				phase:                        ephemeralclusterv1.EphemeralClusterReady,
				updatePhaseTransitionCounter: true,
				updateProvisionDuration:      true,
			},
		},
		{
			name:         "failed job before cluster creation still counts the transition",
			currentPhase: ephemeralclusterv1.EphemeralClusterProvisioning,
			conditions: []metav1.Condition{
				pjCreated,
				condition(ephemeralclusterv1.ProwJobCompleted, metav1.ConditionTrue, ephemeralclusterv1.ProwJobFailureReason, -time.Minute),
			},
			want: phaseTransition{
				phase:                        ephemeralclusterv1.EphemeralClusterProvisioningFailed,
				updatePhaseTransitionCounter: true,
				updateProvisionDuration:      true,
			},
		},
		{
			name:         "job completion before readiness still counts the transition",
			currentPhase: ephemeralclusterv1.EphemeralClusterReady,
			conditions: []metav1.Condition{
				pjCreated, clusterReady,
				condition(ephemeralclusterv1.ProwJobCompleted, metav1.ConditionTrue, ephemeralclusterv1.ProwJobCompletedReason, 9*time.Minute),
			},
			want: phaseTransition{
				phase:                        ephemeralclusterv1.EphemeralClusterDeprovisioned,
				updatePhaseTransitionCounter: true,
				updateDeprovisionDuration:    true,
			},
		},
		{
			name:         "job completion before the test still counts the transition",
			currentPhase: ephemeralclusterv1.EphemeralClusterDeprovisioning,
			conditions: []metav1.Condition{
				pjCreated, clusterReady, testCompleted,
				condition(ephemeralclusterv1.ProwJobCompleted, metav1.ConditionTrue, ephemeralclusterv1.ProwJobFailureReason, 19*time.Minute),
			},
			want: phaseTransition{
				phase:                        ephemeralclusterv1.EphemeralClusterDeprovisioningFailure,
				updatePhaseTransitionCounter: true,
				updateDeprovisionDuration:    true,
			},
		},
		{
			name:         "ready cluster cannot regress to provisioning",
			currentPhase: ephemeralclusterv1.EphemeralClusterReady,
			conditions:   []metav1.Condition{pjCreated},
			want:         phaseTransition{duration: ptr.To(time.Minute)},
		},
		{
			name:         "deprovisioning cluster cannot regress to ready",
			currentPhase: ephemeralclusterv1.EphemeralClusterDeprovisioning,
			conditions:   []metav1.Condition{pjCreated, clusterReady},
			want:         phaseTransition{duration: ptr.To(10 * time.Minute)},
		},
		{
			name:         "provisioning cluster cannot regress to initializing",
			currentPhase: ephemeralclusterv1.EphemeralClusterProvisioning,
			want:         phaseTransition{},
		},
	}

	terminalPhases := []ephemeralclusterv1.EphemeralClusterPhase{
		ephemeralclusterv1.EphemeralClusterInitializationFailed,
		ephemeralclusterv1.EphemeralClusterProvisioningFailed,
		ephemeralclusterv1.EphemeralClusterDeprovisioningFailure,
		ephemeralclusterv1.EphemeralClusterDeprovisioned,
	}
	for _, currentPhase := range terminalPhases {
		for _, candidate := range []struct {
			phase      ephemeralclusterv1.EphemeralClusterPhase
			conditions []metav1.Condition
			duration   *time.Duration
		}{
			{phase: ephemeralclusterv1.EphemeralClusterInitializing},
			{phase: ephemeralclusterv1.EphemeralClusterInitializationFailed, conditions: []metav1.Condition{validationFailed}},
			{phase: ephemeralclusterv1.EphemeralClusterProvisioning, conditions: []metav1.Condition{pjCreated}, duration: ptr.To(time.Minute)},
			{phase: ephemeralclusterv1.EphemeralClusterProvisioningFailed, conditions: []metav1.Condition{pjCreated, pjFailed}, duration: ptr.To(25 * time.Minute)},
			{phase: ephemeralclusterv1.EphemeralClusterReady, conditions: []metav1.Condition{pjCreated, clusterReady}, duration: ptr.To(10 * time.Minute)},
			{phase: ephemeralclusterv1.EphemeralClusterDeprovisioning, conditions: []metav1.Condition{pjCreated, clusterReady, testCompleted}},
			{phase: ephemeralclusterv1.EphemeralClusterDeprovisioningFailure, conditions: []metav1.Condition{pjCreated, clusterReady, testCompleted, pjFailed}, duration: ptr.To(5 * time.Minute)},
			{phase: ephemeralclusterv1.EphemeralClusterDeprovisioned, conditions: []metav1.Condition{pjCreated, clusterReady, testCompleted, pjSucceeded}, duration: ptr.To(5 * time.Minute)},
		} {
			testCases = append(testCases, testCase{
				name:         string(currentPhase) + " cannot transition to " + string(candidate.phase),
				currentPhase: currentPhase,
				conditions:   candidate.conditions,
				want:         phaseTransition{duration: candidate.duration},
			})
		}
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ec := &ephemeralclusterv1.EphemeralCluster{
				ObjectMeta: metav1.ObjectMeta{CreationTimestamp: metav1.NewTime(creationTime)},
			}
			observedStatus := &ephemeralclusterv1.EphemeralClusterStatus{
				Phase:      tc.currentPhase,
				Conditions: tc.conditions,
			}
			originalEC, originalStatus := ec.DeepCopy(), observedStatus.DeepCopy()

			got := computePhaseTransition(ec, observedStatus)
			if diff := cmp.Diff(tc.want, got, cmp.AllowUnexported(phaseTransition{})); diff != "" {
				t.Errorf("unexpected phase transition (-want +got): %s", diff)
			}
			if diff := cmp.Diff(originalEC, ec); diff != "" {
				t.Errorf("ephemeral cluster was mutated: %s", diff)
			}
			if diff := cmp.Diff(originalStatus, observedStatus); diff != "" {
				t.Errorf("observed status was mutated: %s", diff)
			}
		})
	}
}
