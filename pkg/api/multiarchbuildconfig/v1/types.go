package v1

import (
	"context"
	"fmt"

	"github.com/sirupsen/logrus"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	buildv1 "github.com/openshift/api/build/v1"
)

const (
	MultiArchBuildConfigNameLabel = "multiarchbuildconfigs.ci.openshift.io/name"
	MultiArchBuildConfigArchLabel = "multiarchbuildconfigs.ci.openshift.io/arch"
)

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:resource:shortName=mabc

type MultiArchBuildConfig struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`

	// +kubebuilder:validation:Required
	Spec   MultiArchBuildConfigSpec   `json:"spec"`
	Status MultiArchBuildConfigStatus `json:"status,omitempty"`
}

type MultiArchBuildConfigSpec struct {
	BuildSpec buildv1.BuildConfigSpec `json:"build_spec"`
	// output describes the container image the MultiArchBuildConfig should produce.
	//+kubebuilder:validation:Optional
	Output MultiArchBuildConfigOutput `json:"output,omitempty"`
}

type MultiArchBuildConfigOutput struct {
	// to defines a list of optional location to push the output of this build to.
	// A location must be a full registry path, ex.: myregistry.com/myimage:latest.
	//+kubebuilder:validation:MinItems=1
	//+kubebuilder:validation:MaxItems=5
	To []string `json:"to,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type MultiArchBuildConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`

	Items []MultiArchBuildConfig `json:"items"`
}

type MultiArchBuildConfigStatus struct {
	Conditions []metav1.Condition        `json:"conditions,omitempty"`
	State      MultiArchBuildConfigState `json:"state,omitempty"`
	Builds     map[string]*buildv1.Build `json:"builds,omitempty"`
}

type MultiArchBuildConfigState string

const (
	// SuccessState means all builds were completed without error (exit 0)
	SuccessState MultiArchBuildConfigState = "success"
	// FailureState means that all builds were completed with errors (exit non-zero)
	FailureState MultiArchBuildConfigState = "failure"
)

func UpdateMultiArchBuildConfig(ctx context.Context, logger *logrus.Entry, client ctrlruntimeclient.Client, namespacedName types.NamespacedName, mutateFn func(mabcToMutate *MultiArchBuildConfig)) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		multiArchBuildConfig := &MultiArchBuildConfig{}
		if err := client.Get(ctx, namespacedName, multiArchBuildConfig); err != nil {
			return fmt.Errorf("failed to get the MultiArchBuildConfig: %w", err)
		}

		mutateFn(multiArchBuildConfig)

		logger.WithField("namespace", namespacedName.Namespace).WithField("name", namespacedName.Name).Info("Updating MultiArchBuildConfig...")
		if err := client.Update(ctx, multiArchBuildConfig); err != nil {
			return fmt.Errorf("failed to update MultiArchBuildConfig %s: %w", multiArchBuildConfig.Name, err)
		}
		return nil
	})
}
