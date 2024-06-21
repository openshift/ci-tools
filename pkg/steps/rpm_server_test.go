package steps

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	fakectrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"
	prowapi "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	"sigs.k8s.io/prow/pkg/pod-utils/downwardapi"

	routev1 "github.com/openshift/api/route/v1"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/steps/loggingclient"
	"github.com/openshift/ci-tools/pkg/testhelper"
	"github.com/openshift/ci-tools/pkg/util"
)

func TestRPMServerStepProvides(t *testing.T) {
	ns := "ns"
	if err := routev1.AddToScheme(scheme.Scheme); err != nil {
		t.Error(err)
	}
	for _, tc := range []struct {
		name     string
		jobSpec  api.JobSpec
		expected [][2]string
	}{{
		name: "no refs",
	}, {
		name: "ref",
		jobSpec: api.JobSpec{
			JobSpec: downwardapi.JobSpec{
				Refs: &prowapi.Refs{Org: "org", Repo: "repo"},
			},
		},
		expected: [][2]string{{"RPM_REPO_ORG_REPO", "http://host"}},
	}, {
		name: "extra refs",
		jobSpec: api.JobSpec{
			JobSpec: downwardapi.JobSpec{
				ExtraRefs: []prowapi.Refs{
					{Org: "org0", Repo: "repo0"},
					{Org: "org1", Repo: "repo1"},
				},
			},
		},
		expected: [][2]string{
			{"RPM_REPO_ORG0_REPO0", "http://host"},
			{"RPM_REPO_ORG1_REPO1", "http://host"},
		},
	}, {
		name: "refs + extra refs",
		jobSpec: api.JobSpec{
			JobSpec: downwardapi.JobSpec{
				Refs: &prowapi.Refs{Org: "org", Repo: "repo"},
				ExtraRefs: []prowapi.Refs{
					{Org: "org0", Repo: "repo0"},
					{Org: "org1", Repo: "repo1"},
				},
			},
		},
		expected: [][2]string{
			{"RPM_REPO_ORG0_REPO0", "http://host"},
			{"RPM_REPO_ORG1_REPO1", "http://host"},
			{"RPM_REPO_ORG_REPO", "http://host"},
		},
	}} {
		t.Run(tc.name, func(t *testing.T) {
			client := loggingclient.New(fakectrlruntimeclient.NewClientBuilder().WithRuntimeObjects(
				&routev1.Route{
					ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "rpm-repo"},
					Status: routev1.RouteStatus{
						Ingress: []routev1.RouteIngress{{
							Host: "host",
							Conditions: []routev1.RouteIngressCondition{{
								Type:   routev1.RouteAdmitted,
								Status: corev1.ConditionTrue,
							}},
						}},
					},
				},
			).Build())
			tc.jobSpec.SetNamespace(ns)
			step := RPMServerStep(api.RPMServeStepConfiguration{}, client, &tc.jobSpec)
			providesMap := step.Provides()
			var provides [][2]string
			for _, k := range util.SortedKeys(providesMap) {
				s, err := providesMap[k]()
				if err != nil {
					t.Fatal(err)
				}
				provides = append(provides, [2]string{k, s})
			}
			testhelper.Diff(t, "parameter map", provides, tc.expected)
		})
	}
}
