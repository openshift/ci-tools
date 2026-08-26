package jobrunaggregatorlib

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	pipelinev1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	prowv1 "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	prowjobclientset "sigs.k8s.io/prow/pkg/client/clientset/versioned"
)

const fakeMatchingLabel = "fakeMatchingLabel"

func fakeProwJobMatcherFunc(job *prowv1.ProwJob) bool {
	if match, ok := job.Labels[fakeMatchingLabel]; ok && match == "match" {
		return true
	}
	return false
}

func TestAllProwJobFinished(t *testing.T) {
	tests := []struct {
		name               string
		allItems           []*prowv1.ProwJob
		ProwJobMatcherFunc ProwJobMatcherFunc
		result             bool
	}{
		{
			name:               "Single matched job completed test",
			ProwJobMatcherFunc: fakeProwJobMatcherFunc,
			allItems: []*prowv1.ProwJob{
				{
					Status: prowv1.ProwJobStatus{
						CompletionTime: &metav1.Time{
							Time: time.Now(),
						},
					},
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{
							prowJobJobRunIDLabel: "Job1",
							fakeMatchingLabel:    "match",
						},
					},
				},
			},
			result: true,
		},
		{
			name:               "Single matched job uncompleted test",
			ProwJobMatcherFunc: fakeProwJobMatcherFunc,
			allItems: []*prowv1.ProwJob{
				{
					Status: prowv1.ProwJobStatus{},
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{
							prowJobJobRunIDLabel: "Job1",
							fakeMatchingLabel:    "match",
						},
					},
				},
			},
			result: false,
		},
		{
			name:               "Single unmatched job completed test",
			ProwJobMatcherFunc: fakeProwJobMatcherFunc,
			allItems: []*prowv1.ProwJob{
				{
					Status: prowv1.ProwJobStatus{
						CompletionTime: &metav1.Time{
							Time: time.Now(),
						},
					},
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{
							prowJobJobRunIDLabel: "Job1",
							fakeMatchingLabel:    "unmatched",
						},
					},
				},
			},
			result: true,
		},
		{
			name:               "Single unmatched job uncompleted test",
			ProwJobMatcherFunc: fakeProwJobMatcherFunc,
			allItems: []*prowv1.ProwJob{
				{
					Status: prowv1.ProwJobStatus{},
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{
							prowJobJobRunIDLabel: "Job1",
							fakeMatchingLabel:    "unmatched",
						},
					},
				},
			},
			result: true,
		},
		{
			name:               "Multiple matched jobs completed test",
			ProwJobMatcherFunc: fakeProwJobMatcherFunc,
			allItems: []*prowv1.ProwJob{
				{
					Status: prowv1.ProwJobStatus{
						CompletionTime: &metav1.Time{
							Time: time.Now(),
						},
					},
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{
							prowJobJobRunIDLabel: "Job1",
							fakeMatchingLabel:    "match",
						},
					},
				},
				{
					Status: prowv1.ProwJobStatus{
						CompletionTime: &metav1.Time{
							Time: time.Now(),
						},
					},
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{
							prowJobJobRunIDLabel: "Job2",
							fakeMatchingLabel:    "unmatched",
						},
					},
				},
				{
					Status: prowv1.ProwJobStatus{
						CompletionTime: &metav1.Time{
							Time: time.Now(),
						},
					},
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{
							prowJobJobRunIDLabel: "Job3",
							fakeMatchingLabel:    "match",
						},
					},
				},
			},
			result: true,
		},
		{
			name:               "Multiple matched jobs uncompleted test",
			ProwJobMatcherFunc: fakeProwJobMatcherFunc,
			allItems: []*prowv1.ProwJob{
				{
					Status: prowv1.ProwJobStatus{},
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{
							prowJobJobRunIDLabel: "Job1",
							fakeMatchingLabel:    "match",
						},
					},
				},
				{
					Status: prowv1.ProwJobStatus{
						CompletionTime: &metav1.Time{
							Time: time.Now(),
						},
					},
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{
							prowJobJobRunIDLabel: "Job2",
							fakeMatchingLabel:    "unmatched",
						},
					},
				},
				{
					Status: prowv1.ProwJobStatus{
						CompletionTime: &metav1.Time{
							Time: time.Now(),
						},
					},
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{
							prowJobJobRunIDLabel: "Job3",
							fakeMatchingLabel:    "match",
						},
					},
				},
			},
			result: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			waiter := ClusterJobRunWaiter{
				TimeToStopWaiting:  time.Now(),
				ProwJobMatcherFunc: tt.ProwJobMatcherFunc,
			}
			result, _ := waiter.allProwJobsFinished(tt.allItems)
			assert.Equal(t, tt.result, result, "Test %s expecting %v, got %v", tt.name, tt.result, result)
		})
	}
}

func untrimmedProwJob(name string) *prowv1.ProwJob {
	return &prowv1.ProwJob{
		TypeMeta: metav1.TypeMeta{APIVersion: "prow.k8s.io/v1", Kind: "ProwJob"},
		ObjectMeta: metav1.ObjectMeta{
			Name:            name,
			Labels:          map[string]string{"keep": "label"},
			Annotations:     map[string]string{"keep": "annotation"},
			ManagedFields:   []metav1.ManagedFieldsEntry{{Manager: "manager"}},
			OwnerReferences: []metav1.OwnerReference{{Name: "owner"}},
		},
		Spec: prowv1.ProwJobSpec{
			Job:                   "keep-job",
			PodSpec:               &corev1.PodSpec{},
			PipelineRunSpec:       &pipelinev1.PipelineRunSpec{},
			TektonPipelineRunSpec: &prowv1.TektonPipelineRunSpec{},
			DecorationConfig:      &prowv1.DecorationConfig{},
			ExtraRefs:             []prowv1.Refs{{Org: "org", Repo: "repo"}},
		},
		Status: prowv1.ProwJobStatus{
			State:            prowv1.SuccessState,
			Description:      "large description",
			PrevReportStates: map[string]prowv1.ProwJobState{"reporter": prowv1.PendingState},
		},
	}
}

func requireTrimmedProwJob(t *testing.T, prowJob *prowv1.ProwJob) {
	t.Helper()
	require.Nil(t, prowJob.ManagedFields)
	require.Nil(t, prowJob.OwnerReferences)
	require.Nil(t, prowJob.Spec.PodSpec)
	require.Nil(t, prowJob.Spec.PipelineRunSpec)
	require.Nil(t, prowJob.Spec.TektonPipelineRunSpec)
	require.Nil(t, prowJob.Spec.DecorationConfig)
	require.Nil(t, prowJob.Spec.ExtraRefs)
	require.Empty(t, prowJob.Status.Description)
	require.Nil(t, prowJob.Status.PrevReportStates)

	assert.Equal(t, "keep-job", prowJob.Spec.Job)
	assert.Equal(t, map[string]string{"keep": "label"}, prowJob.Labels)
	assert.Equal(t, map[string]string{"keep": "annotation"}, prowJob.Annotations)
	assert.Equal(t, prowv1.SuccessState, prowJob.Status.State)
}

func TestTrimProwJobObject(t *testing.T) {
	t.Run("prowjob", func(t *testing.T) {
		prowJob := untrimmedProwJob("direct")
		got, err := trimProwJobObject(prowJob)
		require.NoError(t, err)
		require.Same(t, prowJob, got)
		requireTrimmedProwJob(t, prowJob)
	})

	t.Run("deleted final state", func(t *testing.T) {
		prowJob := untrimmedProwJob("deleted")
		tombstone := cache.DeletedFinalStateUnknown{Key: prowJob.Name, Obj: prowJob}
		got, err := trimProwJobObject(tombstone)
		require.NoError(t, err)
		assert.Equal(t, tombstone, got)
		requireTrimmedProwJob(t, prowJob)
	})

	t.Run("unrelated object", func(t *testing.T) {
		obj := &corev1.Pod{}
		got, err := trimProwJobObject(obj)
		require.NoError(t, err)
		assert.Same(t, obj, got)
	})
}

func TestNewProwJobInformer(t *testing.T) {
	var mu sync.Mutex
	var listOptions []metav1.ListOptions
	watchOptions := make(chan metav1.ListOptions, 1)
	watchEvents := make(chan watch.Event, 1)
	serverErrors := make(chan error, 1)
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/apis/prow.k8s.io/v1/namespaces/ci/prowjobs" {
			http.NotFound(w, r)
			return
		}

		var limit int64
		if value := r.URL.Query().Get("limit"); value != "" {
			var err error
			limit, err = strconv.ParseInt(value, 10, 64)
			if err != nil {
				select {
				case serverErrors <- err:
				default:
				}
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
		}
		options := metav1.ListOptions{
			Continue:             r.URL.Query().Get("continue"),
			Limit:                limit,
			ResourceVersion:      r.URL.Query().Get("resourceVersion"),
			ResourceVersionMatch: metav1.ResourceVersionMatch(r.URL.Query().Get("resourceVersionMatch")),
		}
		if r.URL.Query().Get("watch") == "true" {
			watchOptions <- options
			w.Header().Set("Content-Type", "application/json")
			flusher := w.(http.Flusher)
			flusher.Flush()
			for {
				select {
				case event := <-watchEvents:
					if err := json.NewEncoder(w).Encode(map[string]interface{}{"type": event.Type, "object": event.Object}); err != nil {
						return
					}
					flusher.Flush()
				case <-r.Context().Done():
					return
				}
			}
		}

		mu.Lock()
		listOptions = append(listOptions, options)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		var page *prowv1.ProwJobList
		switch options.Continue {
		case "":
			page = &prowv1.ProwJobList{
				TypeMeta: metav1.TypeMeta{APIVersion: "prow.k8s.io/v1", Kind: "ProwJobList"},
				ListMeta: metav1.ListMeta{ResourceVersion: "first-page-rv", Continue: "next-page"},
				Items:    []prowv1.ProwJob{*untrimmedProwJob("first")},
			}
		case "next-page":
			page = &prowv1.ProwJobList{
				TypeMeta: metav1.TypeMeta{APIVersion: "prow.k8s.io/v1", Kind: "ProwJobList"},
				ListMeta: metav1.ListMeta{ResourceVersion: "second-page-rv"},
				Items:    []prowv1.ProwJob{*untrimmedProwJob("second")},
			}
		default:
			http.Error(w, "unexpected continue token", http.StatusBadRequest)
			return
		}
		if err := json.NewEncoder(w).Encode(page); err != nil {
			select {
			case serverErrors <- err:
			default:
			}
		}
	}))
	t.Cleanup(apiServer.Close)

	client, err := prowjobclientset.NewForConfig(&rest.Config{Host: apiServer.URL})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	waiter := &ClusterJobRunWaiter{ProwJobClient: client}
	informer, err := waiter.newProwJobInformer(ctx)
	require.NoError(t, err)

	runDone := make(chan struct{})
	go func() {
		defer close(runDone)
		informer.Run(ctx.Done())
	}()
	require.True(t, cache.WaitForCacheSync(ctx.Done(), informer.HasSynced))

	mu.Lock()
	gotListOptions := append([]metav1.ListOptions(nil), listOptions...)
	mu.Unlock()
	require.Len(t, gotListOptions, 2)
	assert.Equal(t, int64(prowJobListPageSize), gotListOptions[0].Limit)
	assert.Empty(t, gotListOptions[0].ResourceVersion)
	assert.Empty(t, gotListOptions[0].ResourceVersionMatch)
	assert.Empty(t, gotListOptions[0].Continue)
	assert.Equal(t, "next-page", gotListOptions[1].Continue)
	assert.Equal(t, int64(prowJobListPageSize), gotListOptions[1].Limit)

	select {
	case options := <-watchOptions:
		assert.Equal(t, "first-page-rv", options.ResourceVersion, "the watch must resume from the first page snapshot")
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for watch to start")
	}

	for _, name := range []string{"first", "second"} {
		obj, exists, err := informer.GetStore().GetByKey(name)
		require.NoError(t, err)
		require.True(t, exists)
		requireTrimmedProwJob(t, obj.(*prowv1.ProwJob))
	}

	updated := untrimmedProwJob("first")
	updated.Spec.Job = "updated-job"
	watchEvents <- watch.Event{Type: watch.Modified, Object: updated}
	require.Eventually(t, func() bool {
		obj, exists, err := informer.GetStore().GetByKey("first")
		if err != nil || !exists {
			return false
		}
		prowJob := obj.(*prowv1.ProwJob)
		return prowJob.Spec.Job == "updated-job" && prowJob.Spec.PodSpec == nil && prowJob.Status.Description == ""
	}, 5*time.Second, 10*time.Millisecond, "watch updates should be cached after transformation")

	cancel()
	select {
	case <-runDone:
		assert.True(t, informer.IsStopped())
	case <-time.After(5 * time.Second):
		t.Fatal("informer did not stop after context cancellation")
	}
	select {
	case err := <-serverErrors:
		t.Fatalf("fake API server failed: %v", err)
	default:
	}
}
