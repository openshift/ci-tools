package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/go-cmp/cmp"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	fakectrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"

	routev1 "github.com/openshift/api/route/v1"
	hivev1 "github.com/openshift/hive/apis/hive/v1"
)

func init() {
	if err := hivev1.AddToScheme(scheme.Scheme); err != nil {
		panic(fmt.Sprintf("failed to add hivev1 to scheme: %v", err))
	}
	if err := routev1.Install(scheme.Scheme); err != nil {
		panic(fmt.Errorf("failed to add routev1 to scheme: %w", err))
	}
}

func aClusterPool(version string) *hivev1.ClusterPool {
	return &hivev1.ClusterPool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("ci-ocp-%s-amd64-aws-us-east-1", version),
			Namespace: "ci-cluster-pool",
			Labels: map[string]string{
				"product":      "ocp",
				"version":      version,
				"architecture": "amd64",
				"cloud":        "aws",
				"owner":        "dpp",
				"region":       "us-east-1",
			},
		},
		Spec: hivev1.ClusterPoolSpec{
			ImageSetRef: hivev1.ClusterImageSetReference{
				Name: fmt.Sprintf("ocp-%s-amd64", version),
			},
		},
	}
}

func aClusterImageSet(version string) *hivev1.ClusterImageSet {
	return &hivev1.ClusterImageSet{
		ObjectMeta: metav1.ObjectMeta{
			Name: fmt.Sprintf("ocp-%s-amd64", version),
		},
		Spec: hivev1.ClusterImageSetSpec{
			ReleaseImage: fmt.Sprintf("quay.io/openshift-release-dev/ocp-release:%s-x86_64", version),
		},
	}
}

func TestGetRouter(t *testing.T) {
	testCases := []struct {
		name                string
		url                 string
		hiveClient          ctrlruntimeclient.Client
		clients             map[string]ctrlruntimeclient.Client
		disabledClusters    []string
		expectedCode        int
		expectedBody        string
		expectedContentType string
	}{
		{
			name:                "server is healthy",
			url:                 "/api/health",
			expectedCode:        200,
			expectedBody:        "{\"ok\":true}\n",
			expectedContentType: "application/json",
		},
		{
			name:         "there are cluster pools",
			url:          "/api/v1/clusterpools",
			hiveClient:   fakectrlruntimeclient.NewClientBuilder().WithRuntimeObjects(aClusterPool("4.7.0"), aClusterPool("4.6.0"), aClusterImageSet("4.7.0"), aClusterImageSet("4.6.0")).Build(),
			expectedCode: 200,
			expectedBody: `{"data":[{"imageSet":"ocp-4.6.0-amd64","labels":"architecture=amd64,cloud=aws,owner=dpp,product=ocp,region=us-east-1,version=4.6.0","maxSize":"nil","name":"ci-ocp-4.6.0-amd64-aws-us-east-1","namespace":"ci-cluster-pool","owner":"dpp","ready":"0","releaseImage":"quay.io/openshift-release-dev/ocp-release:4.6.0-x86_64","size":"0","standby":"0"},{"imageSet":"ocp-4.7.0-amd64","labels":"architecture=amd64,cloud=aws,owner=dpp,product=ocp,region=us-east-1,version=4.7.0","maxSize":"nil","name":"ci-ocp-4.7.0-amd64-aws-us-east-1","namespace":"ci-cluster-pool","owner":"dpp","ready":"0","releaseImage":"quay.io/openshift-release-dev/ocp-release:4.7.0-x86_64","size":"0","standby":"0"}]}
`,
			expectedContentType: "application/json",
		},
		{
			name:                "there are no cluster pools",
			url:                 "/api/v1/clusterpools",
			hiveClient:          fakectrlruntimeclient.NewClientBuilder().Build(),
			expectedCode:        200,
			expectedBody:        "{\"data\":[]}\n",
			expectedContentType: "application/json",
		},
		{
			name:                "there are cluster pools with callback",
			url:                 "/api/v1/clusterpools?callback=jQuery35103321760038853385_1623880606193&_=1623880606194",
			hiveClient:          fakectrlruntimeclient.NewClientBuilder().WithRuntimeObjects(aClusterPool("4.7.0"), aClusterPool("4.6.0"), aClusterImageSet("4.7.0"), aClusterImageSet("4.6.0")).Build(),
			expectedCode:        200,
			expectedBody:        `jQuery35103321760038853385_1623880606193({"data":[{"imageSet":"ocp-4.6.0-amd64","labels":"architecture=amd64,cloud=aws,owner=dpp,product=ocp,region=us-east-1,version=4.6.0","maxSize":"nil","name":"ci-ocp-4.6.0-amd64-aws-us-east-1","namespace":"ci-cluster-pool","owner":"dpp","ready":"0","releaseImage":"quay.io/openshift-release-dev/ocp-release:4.6.0-x86_64","size":"0","standby":"0"},{"imageSet":"ocp-4.7.0-amd64","labels":"architecture=amd64,cloud=aws,owner=dpp,product=ocp,region=us-east-1,version=4.7.0","maxSize":"nil","name":"ci-ocp-4.7.0-amd64-aws-us-east-1","namespace":"ci-cluster-pool","owner":"dpp","ready":"0","releaseImage":"quay.io/openshift-release-dev/ocp-release:4.7.0-x86_64","size":"0","standby":"0"}]});`,
			expectedContentType: "application/javascript",
		},
		{
			name:                "there are no cluster pools with callback",
			url:                 "/api/v1/clusterpools?callback=jQuery35103321760038853385_1623880606193&_=1623880606194",
			hiveClient:          fakectrlruntimeclient.NewClientBuilder().WithRuntimeObjects().Build(),
			expectedCode:        200,
			expectedBody:        `jQuery35103321760038853385_1623880606193({"data":[]});`,
			expectedContentType: "application/javascript",
		},
		{
			name:       "no disabled clusters",
			url:        "/api/v1/clusters",
			hiveClient: fakectrlruntimeclient.NewClientBuilder().WithRuntimeObjects().Build(),
			clients: map[string]ctrlruntimeclient.Client{
				"a": fakectrlruntimeclient.NewClientBuilder().WithRuntimeObjects().Build(),
			},
			expectedCode: 200,
			expectedBody: `{"data":[{"cluster":"a","error":"an error occurred while retrieving cluster information"},{"cluster":"hive","error":"an error occurred while retrieving cluster information"}]}
`,
			expectedContentType: "application/json",
		},
		{
			name:       "disabled clusters",
			url:        "/api/v1/clusters",
			hiveClient: fakectrlruntimeclient.NewClientBuilder().WithRuntimeObjects().Build(),
			clients: map[string]ctrlruntimeclient.Client{
				"a": fakectrlruntimeclient.NewClientBuilder().WithRuntimeObjects().Build(),
			},
			disabledClusters: []string{"a"},
			expectedCode:     200,
			expectedBody: `{"data":[{"cluster":"hive","error":"an error occurred while retrieving cluster information"},{"cluster":"a","error":"disabled cluster in Prow"}]}
`,
			expectedContentType: "application/json",
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodGet, tc.url, nil)
			if err != nil {
				t.Errorf("failed to create http request :%v", err)
			}

			rr := httptest.NewRecorder()
			router := getRouter(context.TODO(), tc.hiveClient, tc.clients, tc.disabledClusters)
			router.ServeHTTP(rr, req)

			if diff := cmp.Diff(tc.expectedCode, rr.Code); diff != "" {
				t.Errorf("%s differs from expected:\n%s", tc.name, diff)
			}
			if diff := cmp.Diff(tc.expectedContentType, rr.Header().Get("Content-Type")); diff != "" {
				t.Errorf("%s differs from expected:\n%s", tc.name, diff)
			}
			if diff := cmp.Diff(tc.expectedBody, rr.Body.String()); diff != "" {
				t.Errorf("%s differs from expected:\n%s", tc.name, diff)
			}
		})
	}
}

type fakeClusterGetter struct{}

func (g *fakeClusterGetter) GetClusterDetails(ctx context.Context, cluster string, client ctrlruntimeclient.Client) (map[string]string, error) {
	if cluster == "badCluster" {
		return map[string]string{
			"cluster": cluster,
			"error":   "an error occurred while retrieving cluster information",
		}, fmt.Errorf("an error occurred")
	}
	return map[string]string{
		"cluster": cluster,
		"error":   "",
	}, nil
}

func TestGetCluster(t *testing.T) {
	testCases := []struct {
		name     string
		clients  map[string]ctrlruntimeclient.Client
		getter   ClusterInfoGetter
		expected []map[string]string
	}{
		{
			name: "Clients without errors",
			clients: map[string]ctrlruntimeclient.Client{
				"cluster1": fakectrlruntimeclient.NewClientBuilder().Build(),
				"cluster2": fakectrlruntimeclient.NewClientBuilder().Build(),
			},
			expected: []map[string]string{
				{
					"cluster": "cluster1",
					"error":   "",
				},
				{
					"cluster": "cluster2",
					"error":   "",
				},
			},
			getter: &fakeClusterGetter{},
		},
		{
			name: "Client with error",
			clients: map[string]ctrlruntimeclient.Client{
				"badCluster": fakectrlruntimeclient.NewClientBuilder().Build(),
			},
			expected: []map[string]string{
				{
					"cluster": "badCluster",
					"error":   "an error occurred while retrieving cluster information",
				},
			},
			getter: &clusterInfoGetter{},
		},
		{
			name: "One client with error",
			clients: map[string]ctrlruntimeclient.Client{
				"okCluster":  fakectrlruntimeclient.NewClientBuilder().Build(),
				"badCluster": fakectrlruntimeclient.NewClientBuilder().Build(),
			},
			expected: []map[string]string{
				{
					"cluster": "badCluster",
					"error":   "an error occurred while retrieving cluster information",
				},
				{
					"cluster": "okCluster",
					"error":   "",
				},
			},
			getter: &fakeClusterGetter{},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			data := getCluster(context.TODO(), tc.clients, tc.getter)
			if diff := cmp.Diff(data, tc.expected); diff != "" {
				t.Errorf("result differs from expected output, diff:\n%s", diff)
			}
		})
	}
}
