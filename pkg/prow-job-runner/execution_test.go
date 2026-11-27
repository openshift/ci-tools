package prowjobrunner

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	prowapi "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	"sigs.k8s.io/prow/pkg/flagutil"
)

// testKubeconfig selects a Prow infrastructure context without requiring a reachable cluster.
const testKubeconfig = `apiVersion: v1
kind: Config
current-context: prow
contexts:
- name: prow
  context: {cluster: prow}
clusters:
- name: prow
  cluster: {server: "https://example.invalid"}
`

// statusRead represents one controller update or read error for a particular job.
type statusRead struct {
	state    prowapi.ProwJobState
	complete bool
	err      error
}

// jobScenario describes a create attempt and the subsequent observations for that job.
type jobScenario struct {
	name          string
	createErr     error
	commitOnError bool
	reads         []statusRead
}

// executionClient scripts the create response and status reads independently for each job.
type executionClient struct {
	client.Client
	t           *testing.T
	scenarios   map[string]*jobScenario
	attempted   sets.Set[string]
	cancelOnGet context.CancelFunc
}

// Create stores accepted jobs, including those for which the response was lost.
func (c *executionClient) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	c.t.Helper()
	s := c.scenarios[obj.GetName()]
	if s == nil || obj.GetNamespace() != "ci" || c.attempted.Has(s.name) {
		c.t.Fatalf("unexpected submission of %s", client.ObjectKeyFromObject(obj))
	}
	c.attempted.Insert(s.name)
	if s.createErr == nil || s.commitOnError {
		if err := c.Client.Create(ctx, obj, opts...); err != nil {
			c.t.Fatal(err)
		}
	}
	return s.createErr
}

// Get reads the next observation for key after all submissions have been attempted.
func (c *executionClient) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	c.t.Helper()
	if len(c.attempted) != len(c.scenarios) {
		c.t.Fatalf("started waiting after only %d of %d submissions", len(c.attempted), len(c.scenarios))
	}
	s := c.scenarios[key.Name]
	if s == nil || key.Namespace != "ci" || len(s.reads) == 0 {
		c.t.Fatalf("unexpected status read for %s", key)
	}
	read := s.reads[0]
	s.reads = s.reads[1:]
	if read.err != nil {
		return read.err
	}
	if err := c.Client.Get(ctx, key, obj, opts...); err != nil {
		return err
	}
	job := obj.(*prowapi.ProwJob)
	job.Status.State = read.state
	if read.complete {
		job.SetComplete()
	}
	if c.cancelOnGet != nil {
		c.cancelOnGet()
	}
	return nil
}

// TestExecute checks submission, waiting for every submitted job, API failures, and cancellation.
func TestExecute(t *testing.T) {
	success := statusRead{state: prowapi.SuccessState, complete: true}
	failure := statusRead{state: prowapi.FailureState, complete: true}
	pending := statusRead{state: prowapi.PendingState}
	resource := schema.GroupResource{Group: "prow.k8s.io", Resource: "prowjobs"}
	denied := apierrors.NewForbidden(resource, "b", errors.New("create denied"))
	for _, tc := range []struct {
		name         string
		jobs         []jobScenario
		cancelBefore bool
		cancelOnGet  bool
		wantErrors   []string
	}{
		{name: "empty execution"},
		{
			name: "submit all before waiting through scheduling and running states",
			jobs: []jobScenario{
				{name: "a", reads: []statusRead{{state: prowapi.SchedulingState}, pending, success}},
				{name: "b", reads: []statusRead{{state: prowapi.TriggeredState}, success}},
			},
		},
		{
			name: "incomplete aborted job is tracked to completion alongside a failed job",
			jobs: []jobScenario{
				{name: "a", reads: []statusRead{{state: prowapi.AbortedState}, {state: prowapi.AbortedState, complete: true}}},
				{name: "b", reads: []statusRead{failure}},
			},
			wantErrors: []string{"test-a (ci/a) finished with state aborted", "test-b (ci/b) finished with state failure"},
		},
		{
			name:       "rejected submission does not skip other jobs or their results",
			jobs:       []jobScenario{{name: "a", reads: []statusRead{success}}, {name: "b", createErr: denied}, {name: "c", reads: []statusRead{pending, failure}}},
			wantErrors: []string{"submitting test-b (ci/b): " + denied.Error(), "test-c (ci/c) finished with state failure"},
		},
		{
			name: "deleted job does not hide a later failure",
			jobs: []jobScenario{
				{name: "a", reads: []statusRead{{err: apierrors.NewNotFound(resource, "a")}}},
				{name: "b", reads: []statusRead{pending, failure}},
			},
			wantErrors: []string{`reading test-a (ci/a): prowjobs.prow.k8s.io "a" not found`, "test-b (ci/b) finished with state failure"},
		},
		{
			name: "transient status errors recover",
			jobs: []jobScenario{{name: "a", reads: []statusRead{{err: apierrors.NewTooManyRequests("busy", 0)}, {err: io.EOF}, success}}},
		},
		{
			name: "create response lost after commit and first confirmation times out",
			jobs: []jobScenario{{name: "a", createErr: io.ErrUnexpectedEOF, commitOnError: true, reads: []statusRead{{err: context.DeadlineExceeded}, pending, success}}},
		},
		{
			name:       "failure of a recovered submission counts",
			jobs:       []jobScenario{{name: "a", createErr: apierrors.NewTimeoutError("response lost", 0), commitOnError: true, reads: []statusRead{pending, failure}}},
			wantErrors: []string{"test-a (ci/a) finished with state failure"},
		},
		{
			name:       "unconfirmed creation is absent and another job fails later",
			jobs:       []jobScenario{{name: "a", createErr: io.ErrUnexpectedEOF, reads: []statusRead{{}}}, {name: "b", reads: []statusRead{pending, failure}}},
			wantErrors: []string{"submitting test-a (ci/a): unexpected EOF", `reading test-a (ci/a): prowjobs.prow.k8s.io "a" not found`, "test-b (ci/b) finished with state failure"},
		},
		{
			name: "cancellation before submission", jobs: []jobScenario{{name: "a"}}, cancelBefore: true,
			wantErrors: []string{"submitting ProwJobs: context canceled"},
		},
		{
			name: "cancellation while waiting leaves jobs intact", jobs: []jobScenario{{name: "a", reads: []statusRead{pending}}}, cancelOnGet: true,
			wantErrors: []string{"waiting for ProwJobs: context canceled"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			if err := prowapi.AddToScheme(scheme); err != nil {
				t.Fatal(err)
			}
			pjclient := &executionClient{
				Client: fake.NewClientBuilder().WithScheme(scheme).Build(), t: t,
				scenarios: map[string]*jobScenario{}, attempted: sets.New[string](),
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if tc.cancelBefore {
				cancel()
			}
			if tc.cancelOnGet {
				pjclient.cancelOnGet = cancel
			}
			var jobs, original []*prowapi.ProwJob
			var wantCreated []string
			for _, scenario := range tc.jobs {
				name := scenario.name
				pjclient.scenarios[name] = &scenario
				if !tc.cancelBefore && (scenario.createErr == nil || scenario.commitOnError) {
					wantCreated = append(wantCreated, name)
				}
				job := &prowapi.ProwJob{
					ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "ci", Labels: map[string]string{"source": "presubmit"}},
					Spec:       prowapi.ProwJobSpec{Job: "test-" + name, Cluster: "build02", Type: prowapi.PresubmitJob},
					Status:     prowapi.ProwJobStatus{State: prowapi.SchedulingState},
				}
				jobs = append(jobs, job)
				original = append(original, job.DeepCopy())
			}
			// A zero polling interval runs scripted transitions without real timers.
			err := execute(ctx, pjclient, jobs, 0)
			if len(tc.wantErrors) == 0 && err != nil {
				t.Fatal(err)
			}
			for _, message := range tc.wantErrors {
				if err == nil || !strings.Contains(err.Error(), message) {
					t.Errorf("want error containing %q, got %v", message, err)
				}
			}
			for _, s := range pjclient.scenarios {
				if pjclient.attempted.Has(s.name) == tc.cancelBefore || len(s.reads) != 0 {
					t.Errorf("job %s: submission attempted=%t, %d unobserved results", s.name, pjclient.attempted.Has(s.name), len(s.reads))
				}
			}
			if diff := cmp.Diff(original, jobs); diff != "" {
				t.Errorf("input jobs changed (-want +got):\n%s", diff)
			}
			var stored prowapi.ProwJobList
			if err := pjclient.Client.List(t.Context(), &stored, client.InNamespace("ci")); err != nil {
				t.Fatal(err)
			}
			var created []string
			for _, job := range stored.Items {
				created = append(created, job.Name)
				if job.Status.State != prowapi.SchedulingState || job.Spec.Cluster != "build02" || job.Labels["source"] != "presubmit" {
					t.Errorf("submitted job lost its scheduling, cluster, or labels: %+v", job)
				}
			}
			slices.Sort(created)
			slices.Sort(wantCreated)
			if diff := cmp.Diff(wantCreated, created); diff != "" {
				t.Errorf("unexpected stored jobs (-want +got):\n%s", diff)
			}
		})
	}
}

// TestPoll checks active and terminal states, missing jobs, and isolation from other ProwJobs.
func TestPoll(t *testing.T) {
	for _, tc := range []struct {
		name     string
		state    prowapi.ProwJobState
		complete bool
		missing  bool
		done     bool
		failed   bool
		wantErr  string
	}{
		{name: "scheduling", state: prowapi.SchedulingState},
		{name: "triggered", state: prowapi.TriggeredState},
		{name: "pending", state: prowapi.PendingState},
		{name: "success", state: prowapi.SuccessState, complete: true, done: true},
		{name: "failure", state: prowapi.FailureState, complete: true, done: true, failed: true},
		{name: "error", state: prowapi.ErrorState, complete: true, done: true, failed: true},
		{name: "aborted", state: prowapi.AbortedState, complete: true, done: true, failed: true},
		{name: "aborted workload still stopping", state: prowapi.AbortedState},
		{name: "unknown state remains outstanding", state: "unknown"},
		{name: "unexpected completed state fails closed", state: "unknown", complete: true, done: true, failed: true},
		{name: "deleted job", missing: true, done: true, wantErr: `reading test (ci/id): prowjobs.prow.k8s.io "id" not found`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			if err := prowapi.AddToScheme(scheme); err != nil {
				t.Fatal(err)
			}
			job := &prowapi.ProwJob{ObjectMeta: metav1.ObjectMeta{Name: "id", Namespace: "ci"}, Spec: prowapi.ProwJobSpec{Job: "test"}}
			stored := job.DeepCopy()
			stored.Status.State = tc.state
			if tc.complete {
				stored.SetComplete()
			}
			builder := fake.NewClientBuilder().WithScheme(scheme)
			if !tc.missing {
				builder.WithObjects(stored)
			}
			unrelated := stored.DeepCopy()
			unrelated.Namespace = "other"
			unrelated.Status.State = prowapi.FailureState
			pjclient := builder.WithObjects(unrelated).Build()
			execution := jobExecution{client: pjclient, pending: []pendingJob{{job: job}}}
			done, err := execution.poll(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(tc.done, done); diff != "" {
				t.Errorf("unexpected completion (-want +got):\n%s", diff)
			}
			var wantFailures, failures []string
			if tc.wantErr != "" {
				wantFailures = []string{tc.wantErr}
			}
			if tc.failed {
				wantFailures = []string{"test (ci/id) finished with state " + string(tc.state)}
			}
			for _, failure := range execution.errors {
				failures = append(failures, failure.Error())
			}
			if diff := cmp.Diff(wantFailures, failures); diff != "" {
				t.Errorf("unexpected job failures (-want +got):\n%s", diff)
			}
			if !done && (len(execution.pending) != 1 || execution.pending[0].job.Status.State != tc.state) {
				t.Errorf("expected latest pending state %s, got %+v", tc.state, execution.pending)
			}
		})
	}
}

// TestRetryableError checks transport errors and Kubernetes responses that should be retried.
func TestRetryableError(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{name: "wrapped lost response", err: fmt.Errorf("reading response: %w", io.ErrUnexpectedEOF), want: true},
		{name: "request deadline", err: context.DeadlineExceeded, want: true},
		{name: "API busy", err: apierrors.NewTooManyRequests("busy", 0), want: true},
		{name: "API unavailable", err: apierrors.NewServiceUnavailable("offline"), want: true},
		{name: "unauthorized", err: apierrors.NewUnauthorized("not authorized")},
		{name: "malformed request", err: apierrors.NewBadRequest("bad request")},
		{name: "client configuration error", err: errors.New("missing kind")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if diff := cmp.Diff(tc.want, retryableError(tc.err)); diff != "" {
				t.Errorf("unexpected retry decision (-want +got):\n%s", diff)
			}
		})
	}
}

// TestNewProwJobClient checks local credential loading without contacting a cluster.
func TestNewProwJobClient(t *testing.T) {
	t.Setenv("KUBERNETES_SERVICE_HOST", "")
	for _, tc := range []struct {
		name       string
		kubeconfig string
		env        string
		args       []string
		wantErr    string
	}{
		{name: "environment kubeconfig", kubeconfig: testKubeconfig, env: "prow.config"},
		{name: "explicit kubeconfig overrides environment", kubeconfig: testKubeconfig, env: "invalid.yaml", args: []string{"--kubeconfig=prow.config"}},
		{name: "directory filters out invalid files", kubeconfig: testKubeconfig, env: "invalid.yaml", args: []string{"--kubeconfig-dir=.", "--kubeconfig-suffix=.config"}},
		{name: "invalid kubeconfig", kubeconfig: "[", env: "prow.config", wantErr: "loading Prow cluster credentials"},
		{name: "empty kubeconfig", env: "prow.config", wantErr: "loading Prow cluster credentials"},
		{name: "no infrastructure context", kubeconfig: testKubeconfig, env: "prow.config", args: []string{"--no-in-cluster-config"}, wantErr: "no Prow infrastructure cluster configured"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			t.Setenv("KUBECONFIG", tc.env)
			if err := os.WriteFile("prow.config", []byte(tc.kubeconfig), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile("invalid.yaml", []byte("["), 0600); err != nil {
				t.Fatal(err)
			}
			var opts flagutil.KubernetesOptions
			fs := flag.NewFlagSet(tc.name, flag.ContinueOnError)
			opts.AddFlags(fs)
			if err := fs.Parse(tc.args); err != nil {
				t.Fatal(err)
			}
			pjclient, err := newProwJobClient(&opts)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("want error containing %q, got %v", tc.wantErr, err)
				}
				return
			}
			if err != nil || pjclient == nil {
				t.Fatalf("want a ProwJob client, got %v, %v", pjclient, err)
			}
		})
	}
}
