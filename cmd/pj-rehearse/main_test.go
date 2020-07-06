package main

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	imagev1 "github.com/openshift/api/image/v1"
	"github.com/sirupsen/logrus"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	utilpointer "k8s.io/utils/pointer"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	fakectrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"

	testimagestreamtagimportv1 "github.com/openshift/ci-tools/pkg/api/testimagestreamtagimport/v1"
)

func init() {
	if err := imagev1.AddToScheme(scheme.Scheme); err != nil {
		panic(fmt.Sprintf("failed to register imagev1 scheme: %v", err))
	}
}

func TestEnsureImageStreamTags(t *testing.T) {
	second = time.Millisecond
	defer func() { second = time.Second }()
	t.Parallel()
	imageStreamTagNamespace, imageStreamTagName := "namespace", "name:1"
	importNamespace := "imports"
	clusterName := "cluster"
	testCases := []struct {
		name string
		// Optional, defaults to the clusterName variable
		targetCluster        *string
		clusterClient        ctrlruntimeclient.Client
		istImportClient      ctrlruntimeclient.Client
		expectedErrorMessage string
		expectedImport       *testimagestreamtagimportv1.TestImageStreamTagImportSpec
	}{
		{
			name: "imagestreamtag already exists, nothing to do",
			clusterClient: fakectrlruntimeclient.NewFakeClient(
				&imagev1.ImageStreamTag{ObjectMeta: metav1.ObjectMeta{
					Namespace: imageStreamTagNamespace,
					Name:      imageStreamTagName,
				}},
			),
		},
		{
			name: "Import already exists error gets swallowed",
			istImportClient: fakectrlruntimeclient.NewFakeClient(
				&testimagestreamtagimportv1.TestImageStreamTagImport{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: importNamespace,
						Name:      fmt.Sprintf("%s-%s-%s", clusterName, imageStreamTagNamespace, strings.Replace(imageStreamTagName, ":", ".", 1)),
					},
					Spec: testimagestreamtagimportv1.TestImageStreamTagImportSpec{
						ClusterName: clusterName,
						Namespace:   imageStreamTagNamespace,
						Name:        imageStreamTagName,
					},
				},
			),
			expectedImport: &testimagestreamtagimportv1.TestImageStreamTagImportSpec{
				ClusterName: clusterName,
				Namespace:   imageStreamTagNamespace,
				Name:        imageStreamTagName,
			},
		},
		{
			name: "Import is created",
			expectedImport: &testimagestreamtagimportv1.TestImageStreamTagImportSpec{
				ClusterName: clusterName,
				Namespace:   imageStreamTagNamespace,
				Name:        imageStreamTagName,
			},
		},
		{
			name:          "Api.ci cluster imports are skipped",
			targetCluster: utilpointer.StringPtr("api.ci"),
		},
	}

	ctx := context.Background()
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {

			if tc.clusterClient == nil {
				tc.clusterClient = fakectrlruntimeclient.NewFakeClient()
			}
			if tc.istImportClient == nil {
				tc.istImportClient = fakectrlruntimeclient.NewFakeClient()
			}
			if tc.targetCluster == nil {
				tc.targetCluster = utilpointer.StringPtr(clusterName)
			}
			tc.istImportClient = &creatingClientWithCallBack{
				Client: tc.istImportClient,
				callback: func() {
					if err := tc.clusterClient.Create(ctx, &imagev1.ImageStreamTag{
						ObjectMeta: metav1.ObjectMeta{
							Namespace: imageStreamTagNamespace,
							Name:      imageStreamTagName,
						},
					}); err != nil {
						t.Fatalf("failed to create imagestreamtag in clusterClient: %v", err)
					}
					t.Log("Created imagestreamtag")
				},
			}
			m := map[string]types.NamespacedName{
				imageStreamTagNamespace + "/" + imageStreamTagName: {
					Namespace: imageStreamTagNamespace,
					Name:      imageStreamTagName,
				},
			}
			if err := ensureImageStreamTags(ctx, tc.clusterClient, m, *tc.targetCluster, importNamespace, tc.istImportClient, logrus.NewEntry(logrus.StandardLogger())); err != nil {
				t.Fatalf("ensureImageStreamTags errored: %v", err)
			}

			created := &testimagestreamtagimportv1.TestImageStreamTagImport{}
			name := types.NamespacedName{
				Namespace: importNamespace,
				Name:      fmt.Sprintf("%s-%s-%s", *tc.targetCluster, imageStreamTagNamespace, strings.Replace(imageStreamTagName, ":", ".", 1)),
			}
			if err := tc.istImportClient.Get(ctx, name, created); err != nil {
				if tc.expectedImport == nil {
					if !apierrors.IsNotFound(err) {
						t.Fatalf("expected not found error, got %v", err)
					}
					return
				}
				t.Fatalf("failed to get imagestreamtagimport %s: %v", name, err)
			}

			if diff := cmp.Diff(&created.Spec, tc.expectedImport); diff != "" {
				t.Errorf("Created import differs from expected: %s", diff)
			}
		})
	}
}

type creatingClientWithCallBack struct {
	callback func()
	ctrlruntimeclient.Client
}

func (c *creatingClientWithCallBack) Create(ctx context.Context, obj runtime.Object, opts ...ctrlruntimeclient.CreateOption) error {
	c.callback()
	return c.Client.Create(ctx, obj, opts...)
}
