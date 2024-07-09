package main

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/pkg/errors"

	"k8s.io/apimachinery/pkg/util/diff"
	v1 "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	prowconfig "sigs.k8s.io/prow/pkg/config"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/diffs"
)

type refId struct {
	org, repo, ref string
}

type fakeGetter struct {
	data map[refId]string
	errs map[refId]error
}

func (g *fakeGetter) GetRef(org, repo, ref string) (string, error) {
	id := refId{org: org, repo: repo, ref: ref}
	if ref, exists := g.data[id]; exists {
		return ref, nil
	}
	if err, exists := g.errs[id]; exists {
		return "", err
	}
	return "", fmt.Errorf("no response configured for id %v", id)
}

func TestJobsFor(t *testing.T) {
	var testCases = []struct {
		name       string
		getter     fakeGetter
		changed    []diffs.PostsubmitInContext
		prowConfig prowconfig.Config
		expected   []struct {
			Spec  v1.ProwJobSpec
			State v1.ProwJobState
		} // the other fields are dynamic and not interesting
		expectedErr bool
	}{
		{
			name: "no changes, no output",
		},
		{
			name: "error getting refs means no job and errors",
			getter: fakeGetter{
				data: map[refId]string{},
				errs: map[refId]error{{org: "org", repo: "repo", ref: "heads/master"}: errors.New("oops")},
			},
			changed:     []diffs.PostsubmitInContext{{Metadata: api.Metadata{Org: "org", Repo: "repo", Branch: "master"}}},
			expectedErr: true,
		},
		{
			name: "happy case creates a job",
			getter: fakeGetter{
				data: map[refId]string{{org: "org", repo: "repo", ref: "heads/master"}: "sha123"},
				errs: map[refId]error{},
			},
			changed: []diffs.PostsubmitInContext{{
				Metadata: api.Metadata{Org: "org", Repo: "repo", Branch: "master"},
				Job: prowconfig.Postsubmit{
					JobBase: prowconfig.JobBase{
						Name:  "my-images",
						Agent: "kubernetes",
					},
				},
			}},
			expected: []struct {
				Spec  v1.ProwJobSpec
				State v1.ProwJobState
			}{{
				v1.ProwJobSpec{
					Type:   v1.PostsubmitJob,
					Agent:  v1.KubernetesAgent,
					Job:    "my-images",
					Refs:   &v1.Refs{Org: "org", Repo: "repo", BaseRef: "master", BaseSHA: "sha123"},
					Report: true,
				},
				v1.TriggeredState,
			}},
		},
		{
			name: "happy case creates a job in scheduling state",
			getter: fakeGetter{
				data: map[refId]string{{org: "org", repo: "repo", ref: "heads/master"}: "sha123"},
				errs: map[refId]error{},
			},
			changed: []diffs.PostsubmitInContext{{
				Metadata: api.Metadata{Org: "org", Repo: "repo", Branch: "master"},
				Job: prowconfig.Postsubmit{
					JobBase: prowconfig.JobBase{
						Name:  "my-images",
						Agent: "kubernetes",
					},
				},
			}},
			prowConfig: prowconfig.Config{ProwConfig: prowconfig.ProwConfig{Scheduler: prowconfig.Scheduler{Enabled: true}}},
			expected: []struct {
				Spec  v1.ProwJobSpec
				State v1.ProwJobState
			}{{
				v1.ProwJobSpec{
					Type:   v1.PostsubmitJob,
					Agent:  v1.KubernetesAgent,
					Job:    "my-images",
					Refs:   &v1.Refs{Org: "org", Repo: "repo", BaseRef: "master", BaseSHA: "sha123"},
					Report: true,
				},
				v1.SchedulingState,
			}},
		},
		{
			name: "error getting some refs means some jobs and errors",
			getter: fakeGetter{
				data: map[refId]string{{org: "org", repo: "repo", ref: "heads/master"}: "sha123"},
				errs: map[refId]error{{org: "org", repo: "repo", ref: "heads/release-1.13"}: errors.New("oops")},
			},
			changed: []diffs.PostsubmitInContext{{
				Metadata: api.Metadata{Org: "org", Repo: "repo", Branch: "master"},
				Job: prowconfig.Postsubmit{
					JobBase: prowconfig.JobBase{
						Name:  "my-images",
						Agent: "kubernetes",
					},
				},
			}, {
				Metadata: api.Metadata{Org: "org", Repo: "repo", Branch: "release-1.13"},
			}},
			expected: []struct {
				Spec  v1.ProwJobSpec
				State v1.ProwJobState
			}{{
				v1.ProwJobSpec{
					Type:   v1.PostsubmitJob,
					Agent:  v1.KubernetesAgent,
					Job:    "my-images",
					Refs:   &v1.Refs{Org: "org", Repo: "repo", BaseRef: "master", BaseSHA: "sha123"},
					Report: true,
				},
				v1.TriggeredState,
			}},
			expectedErr: true,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			actualJobs, actualErr := jobsFor(testCase.changed, &testCase.getter, &testCase.prowConfig)
			if testCase.expectedErr && len(actualErr) == 0 {
				t.Errorf("%s: expected errors but got none", testCase.name)
			}
			if !testCase.expectedErr && len(actualErr) != 0 {
				t.Errorf("%s: expected no errors but got some: %v", testCase.name, actualErr)
			}

			var actualSpecsNStates []struct {
				Spec  v1.ProwJobSpec
				State v1.ProwJobState
			}
			for _, job := range actualJobs {
				actualSpecsNStates = append(actualSpecsNStates, struct {
					Spec  v1.ProwJobSpec
					State v1.ProwJobState
				}{job.Spec, job.Status.State})
			}

			if !reflect.DeepEqual(actualSpecsNStates, testCase.expected) {
				t.Errorf("%s: did not get correct job specs: %v", testCase.name, diff.ObjectReflectDiff(actualSpecsNStates, testCase.expected))
			}
		})
	}
}
