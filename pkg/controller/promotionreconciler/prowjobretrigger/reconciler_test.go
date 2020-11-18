package prowjobretrigger

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/sirupsen/logrus"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	prowjobsv1 "k8s.io/test-infra/prow/apis/prowjobs/v1"
	"k8s.io/test-infra/prow/github"
	fakectrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	imagev1 "github.com/openshift/api/image/v1"

	"github.com/openshift/ci-tools/pkg/controller/promotionreconciler/prowjobreconciler"
	controllerutil "github.com/openshift/ci-tools/pkg/controller/util"
)

func init() {
	if err := imagev1.AddToScheme(scheme.Scheme); err != nil {
		panic(fmt.Sprintf("failed to register imagev1 scheme: %v", err))
	}
}

type fakeGithubClient struct {
	getGef func(string, string, string) (string, error)
}

func (fghc fakeGithubClient) GetRef(org, repo, ref string) (string, error) {
	return fghc.getGef(org, repo, ref)
}

func TestReconcile(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name              string
		githubClient      func(owner, repo, ref string) (string, error)
		promotionDisabled bool
		verify            func(error, *prowjobreconciler.OrgRepoBranchCommit) error
	}{
		{
			name:         "404 getting commit for branch returns terminal error",
			githubClient: func(_, _, _ string) (string, error) { return "", fmt.Errorf("wrapped: %w", github.NewNotFound()) },
			verify: func(e error, _ *prowjobreconciler.OrgRepoBranchCommit) error {
				if !controllerutil.IsTerminal(e) {
					return fmt.Errorf("expected to get terminal error, got %v", e)
				}
				return nil
			},
		},
		{
			name: "ErrTooManyRefs getting commit for branch returns terminal error",
			githubClient: func(_, _, _ string) (string, error) {
				return "", fmt.Errorf("wrapped: %w", github.GetRefTooManyResultsError{})
			},
			verify: func(e error, _ *prowjobreconciler.OrgRepoBranchCommit) error {
				if !controllerutil.IsTerminal(e) {
					return fmt.Errorf("expected to get terminal error, got %v", e)
				}
				return nil
			},
		},
		{
			name:         "outdated job failed, nothing to do",
			githubClient: func(_, _, _ string) (string, error) { return "other", nil },
			verify: func(e error, req *prowjobreconciler.OrgRepoBranchCommit) error {
				if e != nil {
					return fmt.Errorf("expected error to be nil, was %w", e)
				}
				if req != nil {
					return fmt.Errorf("expected to not get a prowjob creation request, got %v", req)
				}
				return nil
			},
		},
		{
			name:         "current commit job failed, prowjob created",
			githubClient: func(_, _, _ string) (string, error) { return "commit", nil },
			verify: func(e error, req *prowjobreconciler.OrgRepoBranchCommit) error {
				if e != nil {
					return fmt.Errorf("expected error to be nil, was %w", e)
				}
				if req == nil {
					return errors.New("expected to get request, was nil")
				}
				expected := &prowjobreconciler.OrgRepoBranchCommit{
					Org:    "org",
					Repo:   "repo",
					Branch: "branch",
					Commit: "commit",
				}
				if diff := cmp.Diff(req, expected); diff != "" {
					return fmt.Errorf("req differs from expected: %s", diff)
				}
				return nil
			},
		},
	}

	logger := logrus.New()
	logger.SetLevel(logrus.TraceLevel)

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			prowJob := &prowjobsv1.ProwJob{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "namespace",
					Name:      "name",
				},
				Spec: prowjobsv1.ProwJobSpec{
					Job: "branch-ci-org-repo-branch-images",
					Refs: &prowjobsv1.Refs{
						Org:     "org",
						Repo:    "repo",
						BaseRef: "branch",
						BaseSHA: "commit",
					},
				},
			}

			var req *prowjobreconciler.OrgRepoBranchCommit

			r := &reconciler{
				log:          logger.WithField("test", tc.name),
				client:       fakectrlruntimeclient.NewFakeClient(prowJob),
				gitHubClient: fakeGithubClient{getGef: tc.githubClient},
				enqueueJob:   func(orbc prowjobreconciler.OrgRepoBranchCommit) { req = &orbc },
			}

			err := r.reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{
				Namespace: "namespace",
				Name:      "name",
			}}, r.log)

			if err := tc.verify(err, req); err != nil {
				t.Fatal(err)
			}
		})
	}
}
