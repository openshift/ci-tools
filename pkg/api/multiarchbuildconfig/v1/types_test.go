package v1

import (
	"context"
	"fmt"
	"testing"

	"github.com/sirupsen/logrus"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestUpdateMultiArchBuildConfig(t *testing.T) {
	ctx := context.Background()

	testCases := []struct {
		name                 string
		multiArchBuildConfig *MultiArchBuildConfig
		mutateFn             func(*MultiArchBuildConfig)
		verifyFn             func(*MultiArchBuildConfig) error
	}{
		{
			name: "WithMutation",
			multiArchBuildConfig: &MultiArchBuildConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-mabc",
					Namespace: "test-ns",
				},
			},
			mutateFn: func(mabcToMutate *MultiArchBuildConfig) {
				mabcToMutate.Status.State = SuccessState
			},
			verifyFn: func(updatedMABC *MultiArchBuildConfig) error {
				if updatedMABC.Status.State != SuccessState {
					return fmt.Errorf("expected mutated status.state, got: %s", updatedMABC.Status.State)
				}
				return nil
			},
		},
		{
			name: "WithoutMutation",
			multiArchBuildConfig: &MultiArchBuildConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-mabc",
					Namespace: "test-ns",
				},
			},
			mutateFn: func(mabcToMutate *MultiArchBuildConfig) {},
			verifyFn: func(updatedMABC *MultiArchBuildConfig) error {
				if updatedMABC == nil {
					return fmt.Errorf("object not found")
				}
				return nil
			},
		},
	}

	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			fakeClient := fake.NewClientBuilder().WithObjects(tt.multiArchBuildConfig).Build()

			err := UpdateMultiArchBuildConfig(ctx, logrus.WithField("test-case", tt.name), fakeClient, types.NamespacedName{Name: tt.multiArchBuildConfig.Name, Namespace: tt.multiArchBuildConfig.Namespace}, tt.mutateFn)
			if err != nil {
				t.Fatalf("update failed: %v", err)
			}

			updatedMABC := &MultiArchBuildConfig{}
			if err = fakeClient.Get(ctx, types.NamespacedName{Name: tt.multiArchBuildConfig.Name, Namespace: tt.multiArchBuildConfig.Namespace}, updatedMABC); err != nil {
				t.Fatalf("failed to get updated object: %v", err)
			}

			if err := tt.verifyFn(updatedMABC); err != nil {
				t.Error(err)
			}
		})
	}
}
