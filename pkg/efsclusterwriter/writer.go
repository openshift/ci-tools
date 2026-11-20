// Package efsclusterwriter materializes dispatcher placements into the cluster
// field of Prow job files stored on EFS. It never creates, deletes, or renames a
// job and never changes any field other than cluster.
package efsclusterwriter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/sirupsen/logrus"

	prowconfig "sigs.k8s.io/prow/pkg/config"
	"sigs.k8s.io/yaml"

	"github.com/openshift/ci-tools/pkg/dispatcher"
	"github.com/openshift/ci-tools/pkg/jobconfig"
	"github.com/openshift/ci-tools/pkg/util/gzip"
)

var (
	jobsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "efs_cluster_writer_jobs_total",
		Help: "Jobs processed by result.",
	}, []string{"result"})
	filesChanged = promauto.NewCounter(prometheus.CounterOpts{
		Name: "efs_cluster_writer_files_changed_total",
		Help: "Job files rewritten by the writer.",
	})
	lastGeneration = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "efs_cluster_writer_last_generation",
		Help: "Last dispatcher generation applied.",
	})
	reconcileDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "efs_cluster_writer_reconcile_duration_seconds",
		Help:    "Time spent in one reconcile pass.",
		Buckets: prometheus.DefBuckets,
	})
	concurrentWrites = promauto.NewCounter(prometheus.CounterOpts{
		Name: "efs_cluster_writer_concurrent_writes_total",
		Help: "Job files left unwritten because they changed during the pass.",
	})
	circuitBreaker = promauto.NewCounter(prometheus.CounterOpts{
		Name: "efs_cluster_writer_circuit_breaker_total",
		Help: "Reconcile passes aborted by the move limit.",
	})
)

// Assignment is the part of the dispatcher decision the writer needs.
type Assignment struct {
	Cluster string `json:"cluster"`
	// BaselineCluster is the placement without any active override. The durable
	// fallback carries this value so an expiring override is not baked into EFS.
	BaselineCluster string   `json:"baselineCluster"`
	Capabilities    []string `json:"capabilities,omitempty"`
	Root            string   `json:"root,omitempty"`
}

// fallbackCluster is the cluster to persist as the offline fallback.
func (a Assignment) fallbackCluster() string {
	if a.BaselineCluster != "" {
		return a.BaselineCluster
	}
	return a.Cluster
}

// Assignments is the bulk response from GET /assignments.
type Assignments struct {
	Generation  uint64                            `json:"generation"`
	Digest      string                            `json:"digest"`
	Assignments map[string]Assignment             `json:"assignments"`
	Inventory   map[string]dispatcher.ClusterInfo `json:"inventory"`
	Blocked     []string                          `json:"blocked,omitempty"`
}

// Client reads assignments and asks the dispatcher to rescan.
type Client interface {
	Assignments(ctx context.Context, after uint64) (*Assignments, error)
	Rescan(ctx context.Context) error
}

// HTTPClient implements Client.
type HTTPClient struct {
	AssignmentsURL string
	RescanURL      string
	Token          string
	HTTP           *http.Client
}

func (c *HTTPClient) Assignments(ctx context.Context, after uint64) (*Assignments, error) {
	u, err := url.Parse(c.AssignmentsURL)
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("root", "efs")
	if after != 0 {
		q.Set("after", fmt.Sprintf("%d", after))
	}
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("assignments: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	var result Assignments
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *HTTPClient) Rescan(ctx context.Context) error {
	if c.RescanURL == "" || c.Token == "" {
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.RescanURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("rescan: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	return nil
}

func (c *HTTPClient) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 6 * time.Minute}
}

// Config controls one writer.
type Config struct {
	EFSRoot        string
	BackupDir      string
	MaxMoves       int
	MaxMovePercent float64
	DryRun         bool
	Logger         *logrus.Entry
	// beforeWrite runs immediately before the final read of each file. Tests use
	// it to simulate a publisher rewriting the file during the pass.
	beforeWrite func(path string)
}

// Result summarizes one pass.
type Result struct {
	Generation uint64
	Updated    int
	Unchanged  int
	Missing    int
	Blocked    int
	Files      int
	// Deferred counts updates skipped because the file changed under us.
	Deferred int
	Aborted  bool
}

// Runner reconciles the EFS tree with the dispatcher assignments.
type Runner struct {
	Config Config
	Client Client
}

// change is one job file whose cluster fields differ from the assignments.
type change struct {
	path     string
	config   *prowconfig.JobConfig
	original []byte
	// jobs is how many cluster fields this file contributed, so a deferred file
	// can be subtracted from the reported update count.
	jobs int
}

// plan is the outcome of a read-only pass over the job tree.
type plan struct {
	result    Result
	changes   []change
	totalJobs int
	moves     int
	updates   int
	forced    int
}

// eligible reports whether a cluster can host a job right now.
type eligible func(cluster string) bool

// Reconcile reads the dispatcher assignments and converges the cluster field of
// every EFS job file to them. It never creates, deletes or renames a job.
func (r *Runner) Reconcile(ctx context.Context) (Result, error) {
	start := time.Now()
	defer func() { reconcileDuration.Observe(time.Since(start).Seconds()) }()

	assignments, err := r.currentAssignments(ctx)
	if err != nil {
		return Result{}, err
	}
	lastGeneration.Set(float64(assignments.Generation))

	planned, err := r.planChanges(assignments)
	if err != nil {
		return planned.result, err
	}
	if err := r.enforceMoveLimit(planned); err != nil {
		return planned.result, err
	}
	deferred, deferredJobs, err := r.applyChanges(planned.changes, assignments)
	if err != nil {
		return planned.result, err
	}

	planned.result.Files = len(planned.changes) - deferred
	planned.result.Updated = planned.updates - deferredJobs
	planned.result.Deferred = deferredJobs
	r.logger().WithFields(logrus.Fields{
		"generation": assignments.Generation, "files": planned.result.Files, "updated": planned.result.Updated,
		"deferred": planned.result.Deferred, "unchanged": planned.result.Unchanged,
		"missing": planned.result.Missing, "blocked": planned.result.Blocked,
		"dryRun": r.Config.DryRun,
	}).Info("reconcile complete")
	return planned.result, nil
}

// currentAssignments fetches the assignments, asking the dispatcher for a delta
// rescan first when a published job is not assigned yet.
func (r *Runner) currentAssignments(ctx context.Context) (*Assignments, error) {
	assignments, err := r.Client.Assignments(ctx, 0)
	if err != nil {
		return nil, fmt.Errorf("read assignments: %w", err)
	}
	// If a job was published since the dispatcher's last delta scan, ask it to
	// rescan and read the new generation before writing anything. This keeps the
	// writer self-sufficient: no publisher-to-writer call is required.
	missing, err := r.hasMissingJobs(assignments)
	if err != nil {
		return nil, err
	}
	if !missing {
		return assignments, nil
	}
	if err := r.Client.Rescan(ctx); err != nil {
		r.logger().WithError(err).Warn("rescan failed; continuing with the current generation")
	}
	refreshed, err := r.Client.Assignments(ctx, 0)
	if err != nil {
		return nil, fmt.Errorf("read assignments after rescan: %w", err)
	}
	return refreshed, nil
}

// planChanges walks the job tree and records what would change, without writing.
func (r *Runner) planChanges(assignments *Assignments) (plan, error) {
	canHost, blocked := assignments.policy()
	planned := plan{result: Result{Generation: assignments.Generation}}
	err := filepath.WalkDir(r.Config.EFSRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path != r.Config.EFSRoot && strings.HasPrefix(d.Name(), "..") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() || !strings.HasSuffix(path, ".yaml") {
			return nil
		}
		data, err := gzip.ReadFileMaybeGZIP(path)
		if err != nil {
			return err
		}
		jc, outcome, changed, err := computeFileChange(path, data, assignments, canHost, blocked)
		if err != nil {
			return err
		}
		planned.add(outcome)
		jobsTotal.WithLabelValues("missing").Add(float64(outcome.missing))
		jobsTotal.WithLabelValues("blocked").Add(float64(outcome.blocked))
		jobsTotal.WithLabelValues("unchanged").Add(float64(outcome.unchanged))
		jobsTotal.WithLabelValues("updated").Add(float64(outcome.updates))
		if changed {
			planned.changes = append(planned.changes, change{
				path: path, config: jc, original: data, jobs: outcome.updates,
			})
		}
		return nil
	})
	return planned, err
}

// policy derives the eligibility predicate and the blocked set from the response.
func (a *Assignments) policy() (eligible, map[string]bool) {
	blocked := make(map[string]bool, len(a.Blocked))
	for _, cluster := range a.Blocked {
		blocked[cluster] = true
	}
	// A manual cluster is deliberately absent from the build-farm inventory, so
	// inventory membership is not required. Only an empty or blocked target is
	// rejected: the dispatcher already resolved everything else.
	canHost := func(cluster string) bool {
		return cluster != "" && !blocked[cluster]
	}
	return canHost, blocked
}

func (p *plan) add(outcome fileOutcome) {
	p.totalJobs += outcome.total
	p.moves += outcome.moves
	p.updates += outcome.updates
	p.forced += outcome.forced
	p.result.Missing += outcome.missing
	p.result.Blocked += outcome.blocked
	p.result.Unchanged += outcome.unchanged
}

// fileOutcome counts the jobs of one file by result.
type fileOutcome struct {
	total     int
	missing   int
	blocked   int
	unchanged int
	updates   int
	moves     int
	forced    int
}

// computeFileChange converges one job file to the assignments and reports the
// per-job outcome. It is pure: it reads nothing and writes nothing, so it can be
// re-run against newer file content just before writing.
func computeFileChange(path string, data []byte, assignments *Assignments, canHost eligible, blocked map[string]bool) (*prowconfig.JobConfig, fileOutcome, bool, error) {
	jc := &prowconfig.JobConfig{}
	if err := yaml.Unmarshal(data, jc); err != nil {
		return nil, fileOutcome{}, false, fmt.Errorf("parse %s: %w", path, err)
	}
	before := cloneJobConfig(jc)
	outcome := fileOutcome{}
	changed := false
	planJob := func(name string, cluster string, set func(string)) {
		outcome.total++
		a, ok := assignments.Assignments[name]
		if !ok {
			outcome.missing++
			return
		}
		target := a.fallbackCluster()
		if cluster == target {
			outcome.unchanged++
			return
		}
		// The file needs a write, so its target must be usable.
		if !canHost(target) {
			outcome.blocked++
			return
		}
		// An empty cluster means the job has no placement yet, so filling it in
		// is not a move between placements. Charging those to the breaker would
		// refuse to seed a freshly published tree.
		if cluster != "" {
			if blocked[cluster] {
				outcome.forced++
			}
			outcome.moves++
		}
		outcome.updates++
		set(target)
		changed = true
	}
	for repo := range jc.PresubmitsStatic {
		for i := range jc.PresubmitsStatic[repo] {
			job := &jc.PresubmitsStatic[repo][i]
			planJob(job.Name, job.Cluster, func(c string) { job.Cluster = c })
		}
	}
	for repo := range jc.PostsubmitsStatic {
		for i := range jc.PostsubmitsStatic[repo] {
			job := &jc.PostsubmitsStatic[repo][i]
			planJob(job.Name, job.Cluster, func(c string) { job.Cluster = c })
		}
	}
	for i := range jc.Periodics {
		job := &jc.Periodics[i]
		planJob(job.Name, job.Cluster, func(c string) { job.Cluster = c })
	}
	if !changed {
		return jc, outcome, false, nil
	}
	if !onlyClusterChanged(before, jc) {
		return nil, outcome, false, fmt.Errorf("refusing to write %s: non-cluster field changed", path)
	}
	return jc, outcome, true, nil
}

// enforceMoveLimit aborts the pass when the plan moves more jobs than allowed.
func (r *Runner) enforceMoveLimit(planned plan) error {
	limit := r.Config.MaxMoves
	if p := int(float64(planned.totalJobs) * r.Config.MaxMovePercent / 100); p > limit {
		limit = p
	}
	if limit < 1 {
		limit = 1
	}
	if planned.moves-planned.forced <= limit {
		return nil
	}
	circuitBreaker.Inc()
	planned.result.Aborted = true
	return fmt.Errorf("refusing to move %d jobs (forced %d, limit %d of %d)", planned.moves, planned.forced, limit, planned.totalJobs)
}

// applyChanges writes the planned files. Each file is recomputed from its latest
// content immediately before writing, so an update published during the pass is
// merged instead of overwritten. A file whose content changes again after the
// write is left to the next pass.
func (r *Runner) applyChanges(changes []change, assignments *Assignments) (deferredFiles int, deferredJobs int, err error) {
	canHost, blocked := assignments.policy()
	for _, c := range changes {
		if r.Config.DryRun {
			r.logger().WithField("path", c.path).Info("dry-run: would update cluster")
			continue
		}
		pending := c
		if r.Config.beforeWrite != nil {
			r.Config.beforeWrite(c.path)
		}
		current, err := gzip.ReadFileMaybeGZIP(c.path)
		if err != nil {
			return deferredFiles, deferredJobs, fmt.Errorf("re-read %s: %w", c.path, err)
		}
		if !bytes.Equal(current, c.original) {
			// The publisher rewrote the file after planning. Re-plan against the
			// newer content and keep its changes.
			jc, outcome, changed, err := computeFileChange(c.path, current, assignments, canHost, blocked)
			if err != nil {
				return deferredFiles, deferredJobs, err
			}
			if !changed {
				continue
			}
			pending = change{path: c.path, config: jc, original: current, jobs: outcome.updates}
		}
		if err := r.backup(pending.path, pending.original, assignments.Generation); err != nil {
			return deferredFiles, deferredJobs, err
		}
		if err := jobconfig.WriteToFileAtomic(pending.path, pending.config); err != nil {
			return deferredFiles, deferredJobs, fmt.Errorf("write %s: %w", pending.path, err)
		}
		written, err := gzip.ReadFileMaybeGZIP(pending.path)
		if err != nil {
			return deferredFiles, deferredJobs, fmt.Errorf("verify %s: %w", pending.path, err)
		}
		if !bytes.Equal(written, pendingOriginalBytes(pending)) {
			// The publisher replaced the file again; its content is on disk and
			// the next pass re-applies the cluster value.
			concurrentWrites.Inc()
			deferredFiles++
			deferredJobs += pending.jobs
			r.logger().WithField("path", pending.path).Debug("file changed after write; deferring to the next pass")
			continue
		}
		filesChanged.Inc()
	}
	return deferredFiles, deferredJobs, nil
}

func pendingOriginalBytes(pending change) []byte {
	out, _ := yaml.Marshal(*pending.config)
	return out
}

func (r *Runner) logger() *logrus.Entry {
	if r.Config.Logger != nil {
		return r.Config.Logger
	}
	return logrus.WithField("component", "efs-cluster-writer")
}

func (r *Runner) backup(path string, data []byte, generation uint64) error {
	if r.Config.BackupDir == "" {
		return nil
	}
	rel, err := filepath.Rel(r.Config.EFSRoot, path)
	if err != nil {
		return err
	}
	dest := filepath.Join(r.Config.BackupDir, fmt.Sprintf("%d", generation), rel+".bak")
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dest, data, 0o644)
}

// hasMissingJobs reports whether any job in the EFS tree has no assignment.
func (r *Runner) hasMissingJobs(assignments *Assignments) (bool, error) {
	missing := false
	err := filepath.WalkDir(r.Config.EFSRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path != r.Config.EFSRoot && strings.HasPrefix(d.Name(), "..") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() || !strings.HasSuffix(path, ".yaml") || missing {
			return nil
		}
		data, err := gzip.ReadFileMaybeGZIP(path)
		if err != nil {
			return err
		}
		jc := &prowconfig.JobConfig{}
		if err := yaml.Unmarshal(data, jc); err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
		for repo := range jc.PresubmitsStatic {
			for i := range jc.PresubmitsStatic[repo] {
				if _, ok := assignments.Assignments[jc.PresubmitsStatic[repo][i].Name]; !ok {
					missing = true
					return nil
				}
			}
		}
		for repo := range jc.PostsubmitsStatic {
			for i := range jc.PostsubmitsStatic[repo] {
				if _, ok := assignments.Assignments[jc.PostsubmitsStatic[repo][i].Name]; !ok {
					missing = true
					return nil
				}
			}
		}
		for i := range jc.Periodics {
			if _, ok := assignments.Assignments[jc.Periodics[i].Name]; !ok {
				missing = true
				return nil
			}
		}
		return nil
	})
	return missing, err
}

func cloneJobConfig(in *prowconfig.JobConfig) *prowconfig.JobConfig {
	data, _ := yaml.Marshal(in)
	out := &prowconfig.JobConfig{}
	_ = yaml.Unmarshal(data, out)
	return out
}

// onlyClusterChanged reports whether the only differences are cluster fields.
func onlyClusterChanged(before, after *prowconfig.JobConfig) bool {
	zero := func(jc *prowconfig.JobConfig) *prowconfig.JobConfig {
		c := cloneJobConfig(jc)
		for repo := range c.PresubmitsStatic {
			for i := range c.PresubmitsStatic[repo] {
				c.PresubmitsStatic[repo][i].Cluster = ""
			}
		}
		for repo := range c.PostsubmitsStatic {
			for i := range c.PostsubmitsStatic[repo] {
				c.PostsubmitsStatic[repo][i].Cluster = ""
			}
		}
		for i := range c.Periodics {
			c.Periodics[i].Cluster = ""
		}
		return c
	}
	bs, _ := yaml.Marshal(zero(before))
	as, _ := yaml.Marshal(zero(after))
	return string(bs) == string(as)
}
