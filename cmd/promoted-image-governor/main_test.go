package main

import (
	"context"
	"fmt"
	"regexp"
	"testing"

	"github.com/google/go-cmp/cmp"

	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/kubernetes/scheme"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	fakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"

	imagev1 "github.com/openshift/api/image/v1"

	"github.com/openshift/ci-tools/pkg/api"
	releaseconfig "github.com/openshift/ci-tools/pkg/release/config"
	"github.com/openshift/ci-tools/pkg/testhelper"
)

func init() {
	if err := imagev1.AddToScheme(scheme.Scheme); err != nil {
		panic(fmt.Sprintf("failed to register imagev1 scheme: %v", err))
	}
}

func TestTagsToDelete(t *testing.T) {
	ocp48Stream := &imagev1.ImageStream{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "4.8",
			Namespace: "ocp",
		},
		Status: imagev1.ImageStreamStatus{
			Tags: []imagev1.NamedTagEventList{
				{Tag: "some-component"},
				{Tag: "machine-os-content"},
				{Tag: "bar"},
			},
		},
	}

	origin48Stream := &imagev1.ImageStream{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "4.8",
			Namespace: "origin",
		},
		Status: imagev1.ImageStreamStatus{
			Tags: []imagev1.NamedTagEventList{
				{Tag: "some-component"},
				{Tag: "bar"},
				{Tag: "machine-os-content"},
				{Tag: "not-mirror-from-ocp"},
			},
		},
	}

	ciSomeStream := &imagev1.ImageStream{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "some-tool",
			Namespace: "ci",
		},
		Status: imagev1.ImageStreamStatus{
			Tags: []imagev1.NamedTagEventList{
				{Tag: "latest"},
				{Tag: "test"},
			},
		},
	}

	testCases := []struct {
		name                                 string
		client                               ctrlruntimeclient.Client
		promotedTags                         []api.ImageStreamTagReference
		toIgnore                             []*regexp.Regexp
		imageStreamRefs                      []releaseconfig.ImageStreamRef
		expectedTagsToDelete                 map[api.ImageStreamTagReference]interface{}
		expectedImageStreamsWithPromotedTags map[ctrlruntimeclient.ObjectKey]interface{}
		expectedError                        error
	}{
		{
			name:   "basic case",
			client: fakeclient.NewClientBuilder().WithRuntimeObjects(ocp48Stream.DeepCopy(), ciSomeStream.DeepCopy(), origin48Stream.DeepCopy()).Build(),
			promotedTags: []api.ImageStreamTagReference{
				{
					Namespace: "ci",
					Name:      "some-tool",
					Tag:       "latest",
				},
				{
					Namespace: "ocp",
					Name:      "4.8",
					Tag:       "some-component",
				},
				{
					Namespace: "origin",
					Name:      "4.8",
					Tag:       "not-mirror-from-ocp",
				},
			},
			toIgnore: []*regexp.Regexp{
				regexp.MustCompile(`^ocp/\S+:machine-os-content$`),
			},
			imageStreamRefs: []releaseconfig.ImageStreamRef{
				{
					Namespace:   "origin",
					Name:        "4.8",
					ExcludeTags: []string{"machine-os-content"},
				},
			},
			expectedTagsToDelete: map[api.ImageStreamTagReference]interface{}{
				{
					Namespace: "ci",
					Name:      "some-tool",
					Tag:       "test",
				}: nil,
				{
					Namespace: "ocp",
					Name:      "4.8",
					Tag:       "bar",
				}: nil,
				{
					Namespace: "origin",
					Name:      "4.8",
					Tag:       "machine-os-content",
				}: nil,
			},
			expectedImageStreamsWithPromotedTags: map[ctrlruntimeclient.ObjectKey]interface{}{
				{Namespace: "ci", Name: "some-tool"}: nil,
				{Namespace: "ocp", Name: "4.8"}:      nil,
				{Namespace: "origin", Name: "4.8"}:   nil,
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actualTagsToDelete, actualImageStreamsWithPromotedTags, actualError := tagsToDelete(context.TODO(), tc.client, tc.promotedTags, tc.toIgnore, tc.imageStreamRefs)
			if diff := cmp.Diff(tc.expectedTagsToDelete, actualTagsToDelete); diff != "" {
				t.Errorf("%s: actual does not match expected, diff: %s", tc.name, diff)
			}
			if diff := cmp.Diff(tc.expectedImageStreamsWithPromotedTags, actualImageStreamsWithPromotedTags); diff != "" {
				t.Errorf("%s: actual does not match expected, diff: %s", tc.name, diff)
			}
			if diff := cmp.Diff(tc.expectedError, actualError, testhelper.EquateErrorMessage); diff != "" {
				t.Errorf("%s: actual does not match expected, diff: %s", tc.name, diff)
			}
		})
	}
}

func TestGenerateMappings(t *testing.T) {
	testCases := []struct {
		name            string
		promotedTags    []api.ImageStreamTagReference
		mappingConfig   *OpenshiftMappingConfig
		imageStreamRefs []releaseconfig.ImageStreamRef
		expected        map[string]map[string]sets.Set[string]
		expectedErr     error
	}{
		{
			name: "basic case",
			promotedTags: []api.ImageStreamTagReference{
				{
					Namespace: "ci",
					Name:      "some-tool",
					Tag:       "latest",
				},
				{
					Namespace: "origin",
					Name:      "4.8",
					Tag:       "bar",
				},
				{
					Namespace: "origin",
					Name:      "4.8",
					Tag:       "foo",
				},
				{
					Namespace: "origin",
					Name:      "4.9",
					Tag:       "bar",
				},
				{
					Namespace: "origin",
					Name:      "4.9",
					Tag:       "some",
				},
				{
					Namespace: "ocp",
					Name:      "4.8",
					Tag:       "ocp-some",
				},
				{
					Namespace: "ocp",
					Name:      "4.8",
					Tag:       "ironic-ipa-downloader",
				},
			},
			mappingConfig: &OpenshiftMappingConfig{
				SourceNamespace: "origin",
				TargetNamespace: "openshift",
				SourceRegistry:  "registry.ci.openshift.org",
				TargetRegistry:  "quay.io",
				Images: map[string][]string{
					"4.8": {"4.8", "4.8.0"},
					"4.9": {"4.9", "4.9.0", "latest"},
				},
			},
			imageStreamRefs: []releaseconfig.ImageStreamRef{
				{
					Namespace:   "origin",
					Name:        "4.8",
					ExcludeTags: []string{"ironic-ipa-downloader"},
				},
			},
			expected: map[string]map[string]sets.Set[string]{
				"mapping_origin_4_8": {
					"registry.ci.openshift.org/origin/4.8:bar": sets.New[string]("quay.io/openshift/origin-bar:4.8", "quay.io/openshift/origin-bar:4.8.0"),
					"registry.ci.openshift.org/origin/4.8:foo": sets.New[string]("quay.io/openshift/origin-foo:4.8", "quay.io/openshift/origin-foo:4.8.0"),
					"registry.ci.openshift.org/origin/4.8:ocp-some": sets.New[string](
						"quay.io/openshift/origin-ocp-some:4.8",
						"quay.io/openshift/origin-ocp-some:4.8.0",
					),
				},
				"mapping_origin_4_9": {
					"registry.ci.openshift.org/origin/4.9:bar": sets.New[string](
						"quay.io/openshift/origin-bar:4.9",
						"quay.io/openshift/origin-bar:4.9.0",
						"quay.io/openshift/origin-bar:latest",
					),
					"registry.ci.openshift.org/origin/4.9:some": sets.New[string](
						"quay.io/openshift/origin-some:4.9",
						"quay.io/openshift/origin-some:4.9.0",
						"quay.io/openshift/origin-some:latest",
					),
				},
			},
		},
		{
			name: "same destination more than once",
			promotedTags: []api.ImageStreamTagReference{
				{
					Namespace: "origin",
					Name:      "4.6",
					Tag:       "ovirt-installer",
				},
				{
					Namespace: "ocp",
					Name:      "4.6",
					Tag:       "ovirt-installer",
				},
			},
			mappingConfig: &OpenshiftMappingConfig{
				SourceNamespace: "origin",
				TargetNamespace: "openshift",
				SourceRegistry:  "registry.ci.openshift.org",
				TargetRegistry:  "quay.io",
				Images: map[string][]string{
					"4.6": {"4.6", "4.6.0"},
				},
			},
			imageStreamRefs: []releaseconfig.ImageStreamRef{
				{
					Namespace: "origin",
					Name:      "4.6",
				},
			},
			expectedErr: utilerrors.NewAggregate([]error{
				fmt.Errorf("cannot define the same mirroring destination quay.io/openshift/origin-ovirt-installer:4.6 more than once for the source registry.ci.openshift.org/origin/4.6:ovirt-installer in filename mapping_origin_4_6"),
				fmt.Errorf("cannot define the same mirroring destination quay.io/openshift/origin-ovirt-installer:4.6.0 more than once for the source registry.ci.openshift.org/origin/4.6:ovirt-installer in filename mapping_origin_4_6"),
			}),
		},
		{
			name: "same destination more than once: resolved by excluded tags",
			promotedTags: []api.ImageStreamTagReference{
				{
					Namespace: "origin",
					Name:      "4.6",
					Tag:       "ovirt-installer",
				},
				{
					Namespace: "ocp",
					Name:      "4.6",
					Tag:       "ovirt-installer",
				},
			},
			mappingConfig: &OpenshiftMappingConfig{
				SourceNamespace: "origin",
				TargetNamespace: "openshift",
				SourceRegistry:  "registry.ci.openshift.org",
				TargetRegistry:  "quay.io",
				Images: map[string][]string{
					"4.6": {"4.6", "4.6.0"},
				},
			},
			imageStreamRefs: []releaseconfig.ImageStreamRef{
				{
					Namespace:   "origin",
					Name:        "4.6",
					ExcludeTags: []string{"ovirt-installer"},
				},
			},
			expected: map[string]map[string]sets.Set[string]{
				"mapping_origin_4_6": {
					"registry.ci.openshift.org/origin/4.6:ovirt-installer": sets.New[string]("quay.io/openshift/origin-ovirt-installer:4.6", "quay.io/openshift/origin-ovirt-installer:4.6.0"),
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual, acutalErr := generateMappings(tc.promotedTags, tc.mappingConfig, tc.imageStreamRefs)
			if diff := cmp.Diff(tc.expected, actual); diff != "" {
				t.Errorf("%s: actual does not match expected, diff: %s", tc.name, diff)
			}
			if diff := cmp.Diff(tc.expectedErr, acutalErr, testhelper.EquateErrorMessage); diff != "" {
				t.Errorf("%s: actual does not match expected, diff: %s", tc.name, diff)
			}
		})
	}
}

func init() {
	if err := imagev1.AddToScheme(scheme.Scheme); err != nil {
		panic(fmt.Sprintf("failed to register imagev1 scheme: %v", err))
	}
}

func TestDeleteTagsOnBuildFarm(t *testing.T) {
	ocp41ImageStreamAppCI := &imagev1.ImageStream{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "ocp",
			Name:      "4.1",
		},
		Status: imagev1.ImageStreamStatus{
			Tags: []imagev1.NamedTagEventList{
				{
					Tag: "etcd",
				},
			},
		},
	}

	ocp41ImageStreamBuild01 := &imagev1.ImageStream{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "ocp",
			Name:      "4.1",
		},
		Status: imagev1.ImageStreamStatus{
			Tags: []imagev1.NamedTagEventList{
				{
					Tag: "test",
				},
				{
					Tag: "etcd",
				},
			},
		},
	}

	ocp41TestImageStreamTag := &imagev1.ImageStreamTag{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "ocp",
			Name:      "4.1:test",
		},
	}
	ocp41ETCDImageStreamTag := &imagev1.ImageStreamTag{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "ocp",
			Name:      "4.1:etcd",
		},
	}

	testCases := []struct {
		name                         string
		appCIClient                  ctrlruntimeclient.Client
		buildClusterClients          map[string]ctrlruntimeclient.Client
		imageStreamsWithPromotedTags map[ctrlruntimeclient.ObjectKey]interface{}
		dryRun                       bool
		expected                     error
		verify                       func(context.Context, ctrlruntimeclient.Client, map[string]ctrlruntimeclient.Client) error
	}{
		{
			name:                         "delete image stream tags on build farm",
			appCIClient:                  fakeclient.NewClientBuilder().WithObjects(ocp41ImageStreamAppCI.DeepCopy(), ocp41ETCDImageStreamTag.DeepCopy()).Build(),
			imageStreamsWithPromotedTags: map[ctrlruntimeclient.ObjectKey]interface{}{{Namespace: "ocp", Name: "4.1"}: nil},
			buildClusterClients: map[string]ctrlruntimeclient.Client{
				"build01": fakeclient.NewClientBuilder().WithObjects(ocp41ImageStreamBuild01.DeepCopy(), ocp41TestImageStreamTag.DeepCopy(),
					ocp41ETCDImageStreamTag.DeepCopy()).Build(),
			},
			verify: func(ctx context.Context, client ctrlruntimeclient.Client, m map[string]ctrlruntimeclient.Client) error {
				ist := &imagev1.ImageStreamTag{}
				build01Client := m["build01"]
				if err := build01Client.Get(ctx, ctrlruntimeclient.ObjectKey{Namespace: "ocp", Name: "4.1:test"}, ist); !kerrors.IsNotFound(err) {
					return fmt.Errorf("expected Not found error, but got %w", err)
				}
				ist = &imagev1.ImageStreamTag{}
				if err := build01Client.Get(ctx, ctrlruntimeclient.ObjectKey{Namespace: "ocp", Name: "4.1:etcd"}, ist); err != nil {
					return fmt.Errorf("unexpected error occurred %w", err)
				}
				return nil
			},
		},
		{
			name:                         "delete image stream tags on build farm: dry-run mode",
			appCIClient:                  fakeclient.NewClientBuilder().WithObjects(ocp41ImageStreamAppCI.DeepCopy(), ocp41ETCDImageStreamTag.DeepCopy()).Build(),
			imageStreamsWithPromotedTags: map[ctrlruntimeclient.ObjectKey]interface{}{{Namespace: "ocp", Name: "4.1"}: nil},
			buildClusterClients: map[string]ctrlruntimeclient.Client{
				"build01": fakeclient.NewClientBuilder().WithObjects(ocp41ImageStreamBuild01.DeepCopy(), ocp41TestImageStreamTag.DeepCopy(),
					ocp41ETCDImageStreamTag.DeepCopy()).Build(),
			},
			dryRun: true,
			verify: func(ctx context.Context, client ctrlruntimeclient.Client, m map[string]ctrlruntimeclient.Client) error {
				ist := &imagev1.ImageStreamTag{}
				build01Client := m["build01"]
				if err := build01Client.Get(ctx, ctrlruntimeclient.ObjectKey{Namespace: "ocp", Name: "4.1:test"}, ist); err != nil {
					return fmt.Errorf("unexpected error occurred %w", err)
				}
				ist = &imagev1.ImageStreamTag{}
				if err := build01Client.Get(ctx, ctrlruntimeclient.ObjectKey{Namespace: "ocp", Name: "4.1:etcd"}, ist); err != nil {
					return fmt.Errorf("unexpected error occurred %w", err)
				}
				return nil
			},
		},
		{
			name:                         "delete image stream on build farm",
			appCIClient:                  fakeclient.NewClientBuilder().WithObjects().Build(),
			imageStreamsWithPromotedTags: map[ctrlruntimeclient.ObjectKey]interface{}{{Namespace: "ocp", Name: "4.1"}: nil},
			buildClusterClients: map[string]ctrlruntimeclient.Client{
				"build01": fakeclient.NewClientBuilder().WithObjects(ocp41ImageStreamBuild01.DeepCopy()).Build(),
			},
			verify: func(ctx context.Context, client ctrlruntimeclient.Client, m map[string]ctrlruntimeclient.Client) error {
				is := &imagev1.ImageStream{}
				build01Client := m["build01"]
				if err := build01Client.Get(ctx, ctrlruntimeclient.ObjectKey{Namespace: "ocp", Name: "4.1"}, is); !kerrors.IsNotFound(err) {
					return fmt.Errorf("expected Not found error, but got %w", err)
				}
				return nil
			},
		},
		{
			name:                         "delete image stream on build farm: dry-run mode",
			appCIClient:                  fakeclient.NewClientBuilder().WithObjects().Build(),
			imageStreamsWithPromotedTags: map[ctrlruntimeclient.ObjectKey]interface{}{{Namespace: "ocp", Name: "4.1"}: nil},
			buildClusterClients: map[string]ctrlruntimeclient.Client{
				"build01": fakeclient.NewClientBuilder().WithObjects(ocp41ImageStreamBuild01.DeepCopy()).Build(),
			},
			dryRun: true,
			verify: func(ctx context.Context, client ctrlruntimeclient.Client, m map[string]ctrlruntimeclient.Client) error {
				is := &imagev1.ImageStream{}
				build01Client := m["build01"]
				if err := build01Client.Get(ctx, ctrlruntimeclient.ObjectKey{Namespace: "ocp", Name: "4.1"}, is); err != nil {
					return fmt.Errorf("unexpected error occurred %w", err)
				}
				return nil
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.TODO()
			actual := deleteTagsOnBuildFarm(ctx, tc.appCIClient, tc.buildClusterClients, tc.imageStreamsWithPromotedTags, tc.dryRun)
			if diff := cmp.Diff(tc.expected, actual, testhelper.EquateErrorMessage); diff != "" {
				t.Errorf("%s: actual does not match expected, diff: %s", tc.name, diff)
			}
			if tc.verify != nil {
				if err := tc.verify(ctx, tc.appCIClient, tc.buildClusterClients); err != nil {
					t.Errorf("unexpected error occurred: %v", err)
				}
			}
		})
	}
}
