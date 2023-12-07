package main

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	v1 "k8s.io/test-infra/prow/apis/prowjobs/v1"
	"k8s.io/test-infra/prow/github"
	"k8s.io/test-infra/prow/kube"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	fakectrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"
)

type fakeGhClient struct {
	closed sets.Int
}

func (c fakeGhClient) GetPullRequest(org, repo string, number int) (*github.PullRequest, error) {
	if c.closed.Has(number) {
		return &github.PullRequest{State: github.PullRequestStateClosed}, nil
	}
	return &github.PullRequest{State: github.PullRequestStateOpen}, nil

}

func (c fakeGhClient) CreateComment(owner, repo string, number int, comment string) error {
	return nil
}

func (c fakeGhClient) GetPullRequestChanges(org string, repo string, number int) ([]github.PullRequestChange, error) {
	return []github.PullRequestChange{}, nil
}

type FakeReader struct {
	pjs v1.ProwJobList
}

func (tr FakeReader) Get(ctx context.Context, n ctrlruntimeclient.ObjectKey, o ctrlruntimeclient.Object, opts ...ctrlruntimeclient.GetOption) error {
	return nil
}

func (tr FakeReader) List(ctx context.Context, list ctrlruntimeclient.ObjectList, opts ...ctrlruntimeclient.ListOption) error {
	pjList, ok := list.(*v1.ProwJobList)
	if !ok {
		return errors.New("conversion to pj list error")
	}
	pjList.Items = tr.pjs.Items
	return nil
}

func composePresubmit(name string, state v1.ProwJobState, sha string) v1.ProwJob {
	timeNow := time.Now().Truncate(time.Hour)
	pj := v1.ProwJob{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{
				kube.ProwJobTypeLabel: "presubmit",
				kube.OrgLabel:         "org",
				kube.RepoLabel:        "repo",
				kube.PullLabel:        "123",
			},
			CreationTimestamp: metav1.Time{
				Time: timeNow.Add(-3 * time.Hour),
			},
			ResourceVersion: "999",
		},
		Status: v1.ProwJobStatus{
			State: state,
		},
		Spec: v1.ProwJobSpec{
			Type: v1.PresubmitJob,
			Refs: &v1.Refs{
				BaseRef: "master",
				Repo:    "repo",
				Pulls: []v1.Pull{
					{
						Number: 123,
						SHA:    sha,
					},
				},
			},
			Job:    name,
			Report: true,
		},
	}
	if state == v1.SuccessState || state == v1.FailureState || state == v1.AbortedState {
		pj.Status.CompletionTime = &metav1.Time{Time: timeNow.Add(-2 * time.Hour)}
	}
	return pj
}

func Test_reconciler_reportSuccessOnPR(t *testing.T) {
	var objs []runtime.Object
	fakeClient := fakectrlruntimeclient.NewClientBuilder().WithRuntimeObjects(objs...).Build()
	baseSha := "sha"
	dummyPJ := composePresubmit("org-repo-master-ps1", v1.SuccessState, baseSha)
	defaultGhClient := fakeGhClient{closed: sets.NewInt()}

	type fields struct {
		lister FakeReader
		ghc    minimalGhClient
	}
	type args struct {
		ctx        context.Context
		presubmits presubmitTests
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		want    bool
		wantErr bool
	}{
		{
			name: "all tests are required and passed successfully, trigger protected",
			fields: fields{
				lister: FakeReader{pjs: v1.ProwJobList{Items: []v1.ProwJob{
					composePresubmit("org-repo-master-ps2", v1.SuccessState, baseSha),
					composePresubmit("org-repo-master-ps3", v1.SuccessState, baseSha),
				}}},
				ghc: defaultGhClient,
			},
			args: args{
				ctx: context.Background(),
				presubmits: presubmitTests{
					protected:             []string{"org-repo-master-ps1"},
					alwaysRequired:        []string{"org-repo-master-ps2", "org-repo-other-branch-ps2"},
					conditionallyRequired: []string{"org-repo-master-ps3"},
				},
			},
			want:    true,
			wantErr: false,
		},
		{
			name: "all tests are required and passed successfully, do not trigger protected as PR is closed",
			fields: fields{
				lister: FakeReader{pjs: v1.ProwJobList{Items: []v1.ProwJob{
					composePresubmit("org-repo-master-ps2", v1.SuccessState, baseSha),
					composePresubmit("org-repo-master-ps3", v1.SuccessState, baseSha),
				}}},
				ghc: fakeGhClient{closed: sets.NewInt([]int{123}...)},
			},
			args: args{
				ctx: context.Background(),
				presubmits: presubmitTests{
					protected:             []string{"org-repo-master-ps1"},
					alwaysRequired:        []string{"org-repo-master-ps2"},
					conditionallyRequired: []string{"org-repo-master-ps3"},
				},
			},
			want:    false,
			wantErr: false,
		},
		{
			name: "all required complete but conditionally required failed",
			fields: fields{
				lister: FakeReader{pjs: v1.ProwJobList{Items: []v1.ProwJob{
					composePresubmit("org-repo-master-ps2", v1.SuccessState, baseSha),
					composePresubmit("org-repo-master-ps3", v1.FailureState, baseSha),
				}}},
				ghc: defaultGhClient,
			},
			args: args{
				ctx: context.Background(),
				presubmits: presubmitTests{
					protected:             []string{"org-repo-master-ps1"},
					alwaysRequired:        []string{"org-repo-master-ps2"},
					conditionallyRequired: []string{"org-repo-master-ps3"},
				},
			},
			want:    false,
			wantErr: false,
		},
		{
			name: "all required complete only some of cond required executed",
			fields: fields{
				lister: FakeReader{pjs: v1.ProwJobList{Items: []v1.ProwJob{
					composePresubmit("org-repo-master-ps2", v1.SuccessState, baseSha),
					composePresubmit("org-repo-master-ps3", v1.SuccessState, baseSha),
				}}},
				ghc: defaultGhClient,
			},
			args: args{
				ctx: context.Background(),
				presubmits: presubmitTests{
					protected:             []string{"org-repo-master-ps1"},
					alwaysRequired:        []string{"org-repo-master-ps2"},
					conditionallyRequired: []string{"org-repo-master-ps3", "org-repo-master-ps4", "org-repo-master-ps5"},
				},
			},
			want:    true,
			wantErr: false,
		},
		{
			name: "all required complete but always required is aborted",
			fields: fields{
				lister: FakeReader{pjs: v1.ProwJobList{Items: []v1.ProwJob{
					composePresubmit("org-repo-master-ps2", v1.AbortedState, baseSha),
					composePresubmit("org-repo-master-ps3", v1.SuccessState, baseSha),
				}}},
				ghc: defaultGhClient,
			},
			args: args{
				ctx: context.Background(),
				presubmits: presubmitTests{
					protected:             []string{"org-repo-master-ps1"},
					alwaysRequired:        []string{"org-repo-master-ps2"},
					conditionallyRequired: []string{"org-repo-master-ps3"},
				},
			},
			want:    false,
			wantErr: false,
		},
		{
			name: "do not trigger as user is manually triggering",
			fields: fields{
				lister: FakeReader{pjs: v1.ProwJobList{Items: []v1.ProwJob{
					composePresubmit("org-repo-master-ps1", v1.SuccessState, baseSha),
					composePresubmit("org-repo-master-ps2", v1.SuccessState, baseSha),
					composePresubmit("org-repo-master-ps3", v1.SuccessState, baseSha),
				}}},
				ghc: defaultGhClient,
			},
			args: args{
				ctx: context.Background(),
				presubmits: presubmitTests{
					protected:             []string{"org-repo-master-ps1", "org-repo-master-ps4"},
					alwaysRequired:        []string{"org-repo-master-ps2"},
					conditionallyRequired: []string{"org-repo-master-ps3"},
				},
			},
			want:    false,
			wantErr: false,
		},
		{
			name: "do not trigger as required are not complete",
			fields: fields{
				lister: FakeReader{pjs: v1.ProwJobList{Items: []v1.ProwJob{
					composePresubmit("org-repo-master-ps2", v1.PendingState, baseSha),
					composePresubmit("org-repo-master-ps3", v1.TriggeredState, baseSha),
				}}},
				ghc: defaultGhClient,
			},
			args: args{
				ctx: context.Background(),
				presubmits: presubmitTests{
					protected:             []string{"org-repo-master-ps1"},
					alwaysRequired:        []string{"org-repo-master-ps2"},
					conditionallyRequired: []string{"org-repo-master-ps3"},
				},
			},
			want:    false,
			wantErr: false,
		},
		{
			name: "only protected tests exist",
			fields: fields{
				lister: FakeReader{pjs: v1.ProwJobList{Items: []v1.ProwJob{}}},
				ghc:    defaultGhClient,
			},
			args: args{
				ctx: context.Background(),
				presubmits: presubmitTests{
					protected:             []string{"org-repo-master-ps2"},
					alwaysRequired:        []string{},
					conditionallyRequired: []string{},
				},
			},
			want:    true,
			wantErr: false,
		},
		{
			name: "batch with one sha is analyzed but different sha has already passed",
			fields: fields{
				lister: FakeReader{pjs: v1.ProwJobList{Items: []v1.ProwJob{
					composePresubmit("org-repo-master-ps2", v1.SuccessState, "other-sha"),
					composePresubmit("org-repo-master-ps3", v1.SuccessState, "other-sha"),
				}}},
				ghc: defaultGhClient,
			},
			args: args{
				ctx: context.Background(),
				presubmits: presubmitTests{
					protected:             []string{"org-repo-master-ps1"},
					alwaysRequired:        []string{"org-repo-master-ps2"},
					conditionallyRequired: []string{"org-repo-master-ps3"},
				},
			},
			want:    false,
			wantErr: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := &reconciler{
				pjclientset:        fakeClient,
				lister:             tc.fields.lister,
				configDataProvider: &ConfigDataProvider{},
				ghc:                tc.fields.ghc,
				ids:                sync.Map{},
				closedPRsCache:     closedPRsCache{prs: map[string]pullRequest{}, m: sync.Mutex{}, ghc: tc.fields.ghc, clearTime: time.Now()},
			}
			got, err := r.reportSuccessOnPR(tc.args.ctx, &dummyPJ, tc.args.presubmits)
			if (err != nil) != tc.wantErr {
				t.Errorf("reconciler.reportSuccessOnPR() error = %v, wantErr %v", err, tc.wantErr)
				return
			}
			if got != tc.want {
				t.Errorf("reconciler.reportSuccessOnPR() = %v, want %v", got, tc.want)
			}
		})
	}
}
