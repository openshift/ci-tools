package main

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	v1 "k8s.io/test-infra/prow/apis/prowjobs/v1"
	"k8s.io/test-infra/prow/github"
	"k8s.io/test-infra/prow/kube"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	fakectrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"
)

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
				Repo: "repo",
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
	dummyPJ := composePresubmit("ps1", v1.SuccessState, baseSha)

	type fields struct {
		lister FakeReader
		ghc    github.Client
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
					composePresubmit("ps2", v1.SuccessState, baseSha),
					composePresubmit("ps3", v1.SuccessState, baseSha),
				}}},
				ghc: github.NewFakeClient(),
			},
			args: args{
				ctx: context.Background(),
				presubmits: presubmitTests{
					protected:             []string{"ps1"},
					alwaysRequired:        []string{"ps2"},
					conditionallyRequired: []string{"ps3"},
				},
			},
			want:    true,
			wantErr: false,
		},
		{
			name: "all required complete but conditionally required failed",
			fields: fields{
				lister: FakeReader{pjs: v1.ProwJobList{Items: []v1.ProwJob{
					composePresubmit("ps2", v1.SuccessState, baseSha),
					composePresubmit("ps3", v1.FailureState, baseSha),
				}}},
				ghc: github.NewFakeClient(),
			},
			args: args{
				ctx: context.Background(),
				presubmits: presubmitTests{
					protected:             []string{"ps1"},
					alwaysRequired:        []string{"ps2"},
					conditionallyRequired: []string{"ps3"},
				},
			},
			want:    false,
			wantErr: false,
		},
		{
			name: "all required complete but always required is aborted",
			fields: fields{
				lister: FakeReader{pjs: v1.ProwJobList{Items: []v1.ProwJob{
					composePresubmit("ps2", v1.AbortedState, baseSha),
					composePresubmit("ps3", v1.SuccessState, baseSha),
				}}},
				ghc: github.NewFakeClient(),
			},
			args: args{
				ctx: context.Background(),
				presubmits: presubmitTests{
					protected:             []string{"ps1"},
					alwaysRequired:        []string{"ps2"},
					conditionallyRequired: []string{"ps3"},
				},
			},
			want:    false,
			wantErr: false,
		},
		{
			name: "do not trigger as user is manually triggering",
			fields: fields{
				lister: FakeReader{pjs: v1.ProwJobList{Items: []v1.ProwJob{
					composePresubmit("ps1", v1.SuccessState, baseSha),
					composePresubmit("ps2", v1.SuccessState, baseSha),
					composePresubmit("ps3", v1.SuccessState, baseSha),
				}}},
				ghc: github.NewFakeClient(),
			},
			args: args{
				ctx: context.Background(),
				presubmits: presubmitTests{
					protected:             []string{"ps1", "ps4"},
					alwaysRequired:        []string{"ps2"},
					conditionallyRequired: []string{"ps3"},
				},
			},
			want:    false,
			wantErr: false,
		},
		{
			name: "do not trigger as required are not complete",
			fields: fields{
				lister: FakeReader{pjs: v1.ProwJobList{Items: []v1.ProwJob{
					composePresubmit("ps2", v1.PendingState, baseSha),
					composePresubmit("ps3", v1.TriggeredState, baseSha),
				}}},
				ghc: github.NewFakeClient(),
			},
			args: args{
				ctx: context.Background(),
				presubmits: presubmitTests{
					protected:             []string{"ps1"},
					alwaysRequired:        []string{"ps2"},
					conditionallyRequired: []string{"ps3"},
				},
			},
			want:    false,
			wantErr: false,
		},
		{
			name: "only protected tests exist",
			fields: fields{
				lister: FakeReader{pjs: v1.ProwJobList{Items: []v1.ProwJob{}}},
				ghc:    github.NewFakeClient(),
			},
			args: args{
				ctx: context.Background(),
				presubmits: presubmitTests{
					protected:             []string{"ps2"},
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
					composePresubmit("ps2", v1.SuccessState, "other-sha"),
					composePresubmit("ps3", v1.SuccessState, "other-sha"),
				}}},
				ghc: github.NewFakeClient(),
			},
			args: args{
				ctx: context.Background(),
				presubmits: presubmitTests{
					protected:             []string{"ps1"},
					alwaysRequired:        []string{"ps2"},
					conditionallyRequired: []string{"ps3"},
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
				shasCache:          shasCache{shas: map[string]sha{}, m: sync.Mutex{}, clearTime: time.Now()},
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

type fakeGithubClient struct {
}

func (c fakeGithubClient) GetPullRequest(org, repo string, number int) (*github.PullRequest, error) {
	orgRepo := fmt.Sprintf("%s/%s", org, repo)
	return c.prs[orgRepo], nil
}

func Test_closedPRsCache_isPRClosed(t *testing.T) {
	type fields struct {
		prs       map[string]pullRequest
		m         sync.Mutex
		ghc       fakeGithubClient
		clearTime time.Time
	}
	type args struct {
		refs *v1.Refs
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		want    bool
		wantErr bool
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &closedPRsCache{
				prs:       tt.fields.prs,
				m:         tt.fields.m,
				ghc:       tt.fields.ghc,
				clearTime: tt.fields.clearTime,
			}
			got, err := c.isPRClosed(tt.args.refs)
			if (err != nil) != tt.wantErr {
				t.Errorf("closedPRsCache.isPRClosed() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("closedPRsCache.isPRClosed() = %v, want %v", got, tt.want)
			}
		})
	}
}
