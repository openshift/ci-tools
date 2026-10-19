// dispatcher-performance summarizes downloaded ci-operator metrics without estimating savings.
package main

import (
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"slices"
	"time"
)

// artifact contains only the metrics needed for comparison; other plugin fields remain compatible.
type artifact struct {
	Insights                 []insight `json:"test_platform_insights"`
	Builds                   []build   `json:"openshift_builds"`
	Pods                     []pod     `json:"pods"`
	job, identity, inputHash string
}

// insight retains optional values so older artifacts do not count as negative observations.
type insight struct {
	Name    string `json:"name"`
	Context struct {
		JobSpec      struct{ Job, ProwJobID, Target string } `json:"job_spec"`
		InputHash    string                                  `json:"input_hash"`
		Reused       *bool                                   `json:"reused"`
		Duration     *float64                                `json:"duration_seconds"`
		StartedAfter *float64                                `json:"started_after"`
	} `json:"additional_context"`
}

// build represents an observation, which may occur in several job artifacts.
type build struct {
	UID        string    `json:"uid"`
	ForImage   string    `json:"for_image"`
	Duration   *float64  `json:"duration_seconds"`
	Start      time.Time `json:"start_time"`
	Completion time.Time `json:"completion_time"`
	Status     string    `json:"status"`
}

// measuredDuration excludes the producer's default zero for builds with no measured interval.
func (b build) measuredDuration() (*float64, error) {
	if !b.Start.IsZero() && !b.Completion.IsZero() && b.Completion.Before(b.Start) {
		return nil, fmt.Errorf("build completion precedes start")
	}
	if b.Start.IsZero() || b.Completion.IsZero() {
		return nil, nil
	}
	switch b.Status {
	case "Complete", "Failed", "Error", "Cancelled":
		duration := b.Completion.Sub(b.Start).Seconds()
		return &duration, nil
	}
	return nil, nil
}

// pod represents an observation with scheduling latency encoded in nanoseconds.
type pod struct {
	UID        string   `json:"uid"`
	Scheduling *float64 `json:"scheduling_latency"`
}

// distribution uses nearest-rank percentiles and null values when no samples exist.
type distribution struct {
	Samples int      `json:"samples"`
	P50     *float64 `json:"p50_seconds"`
	P95     *float64 `json:"p95_seconds"`
}

// reuseCoverage exposes the actual numerator and denominator separately from missing data.
type reuseCoverage struct {
	Reused   int `json:"reused_runs"`
	Observed int `json:"runs_with_reuse_observation"`
	Missing  int `json:"runs_missing_reuse_observation"`
}

// buildSummary reports observed elapsed duration, not CPU usage or node hours.
type buildSummary struct {
	Unique          int     `json:"unique_uids"`
	Duration        float64 `json:"deduplicated_duration_seconds"`
	MissingUID      int     `json:"observations_missing_uid"`
	MissingDuration int     `json:"unique_uids_missing_duration"`
}

// podSummary exposes UID and latency coverage alongside deduplicated percentiles.
type podSummary struct {
	Unique         int          `json:"unique_uids"`
	MissingUID     int          `json:"observations_missing_uid"`
	MissingLatency int          `json:"unique_uids_missing_scheduling_latency"`
	Scheduling     distribution `json:"scheduling_latency"`
}

// possibleRepeat identifies distinct builds for equal inputs and image names, including retries.
type possibleRepeat struct {
	InputHash string `json:"input_hash"`
	ForImage  string `json:"for_image"`
	Builds    int    `json:"distinct_build_uids"`
}

// summary keeps run-level timings separate from shared resource observations.
type summary struct {
	Runs            int                     `json:"runs"`
	Reuse           reuseCoverage           `json:"namespace_reuse"`
	Timings         map[string]distribution `json:"timings"`
	Builds          buildSummary            `json:"builds"`
	Pods            podSummary              `json:"pods"`
	PossibleRepeats []possibleRepeat        `json:"possible_repeated_builds"`
}

// cohort preserves all job families while providing a matched-only aggregate.
type cohort struct {
	Files      int                `json:"artifact_files"`
	Duplicates int                `json:"duplicate_artifacts"`
	Overall    summary            `json:"overall"`
	Matched    summary            `json:"matched_jobs_overall"`
	Jobs       map[string]summary `json:"jobs"`
}

// comparison makes changes in job mix visible instead of attributing them to dispatching.
type comparison struct {
	Baseline      cohort   `json:"baseline"`
	Candidate     cohort   `json:"candidate"`
	Matched       []string `json:"matched_jobs"`
	BaselineOnly  []string `json:"jobs_baseline_only"`
	CandidateOnly []string `json:"jobs_candidate_only"`
}

// reader owns traversal state so WalkDir needs no capturing closure.
type reader struct {
	artifacts         []artifact
	seen              map[string]struct{}
	files, duplicates int
}

// visit validates every artifact, including copies, before removing duplicate runs.
func (r *reader) visit(path string, entry fs.DirEntry, err error) error {
	if err != nil {
		return err
	}
	if entry.IsDir() || entry.Name() != "ci-operator-metrics.json" {
		return nil
	}
	r.files++
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading %s: %w", path, err)
	}
	a, err := parseArtifact(data)
	if err != nil {
		return fmt.Errorf("parsing %s: %w", path, err)
	}
	if _, found := r.seen[a.identity]; found {
		r.duplicates++
		return nil
	}
	r.seen[a.identity] = struct{}{}
	r.artifacts = append(r.artifacts, a)
	return nil
}

// parseArtifact rejects invalid measurements rather than silently skewing percentiles.
func parseArtifact(data []byte) (artifact, error) {
	var a artifact
	if err := json.Unmarshal(data, &a); err != nil {
		return a, err
	}
	for _, event := range a.Insights {
		if !validTime(event.Context.Duration) || !validTime(event.Context.StartedAfter) {
			return a, fmt.Errorf("negative or nonfinite insight time")
		}
		if event.Name == "started" {
			a.job = event.Context.JobSpec.Job
			if event.Context.JobSpec.ProwJobID != "" {
				a.identity = event.Context.JobSpec.ProwJobID + "\x00" + event.Context.JobSpec.Target
			}
		}
		if event.Name == "namespace_initialized" {
			a.inputHash = event.Context.InputHash
		}
	}
	if a.job == "" {
		return a, fmt.Errorf("missing started job_spec.job")
	}
	if a.identity == "" {
		a.identity = fmt.Sprintf("sha256:%x", sha256.Sum256(data))
	}
	for i := range a.Builds {
		b := &a.Builds[i]
		if !validTime(b.Duration) {
			return a, fmt.Errorf("negative or nonfinite build duration")
		}
		duration, err := b.measuredDuration()
		if err != nil {
			return a, err
		}
		b.Duration = duration
	}
	for _, p := range a.Pods {
		if !validTime(p.Scheduling) {
			return a, fmt.Errorf("negative or nonfinite pod scheduling latency")
		}
	}
	return a, nil
}

// validTime permits missing measurements while rejecting impossible observed values.
func validTime(value *float64) bool {
	return value == nil || (*value >= 0 && !math.IsNaN(*value) && !math.IsInf(*value, 0))
}

// readArtifacts traverses nested download directories deterministically.
func readArtifacts(root string) (reader, error) {
	r := reader{seen: map[string]struct{}{}}
	info, err := os.Stat(root)
	if err != nil {
		return r, fmt.Errorf("opening artifact directory: %w", err)
	}
	if !info.IsDir() {
		return r, fmt.Errorf("artifact path is not a directory: %s", root)
	}
	if err := filepath.WalkDir(root, r.visit); err != nil {
		return r, err
	}
	if r.files == 0 {
		return r, fmt.Errorf("no ci-operator-metrics.json artifacts in %s", root)
	}
	return r, nil
}

// percentiles uses nearest rank, making even one observed sample useful.
func percentiles(values []float64) distribution {
	d := distribution{Samples: len(values)}
	if len(values) == 0 {
		return d
	}
	slices.Sort(values)
	p50, p95 := values[int(math.Ceil(float64(len(values))*0.50))-1], values[int(math.Ceil(float64(len(values))*0.95))-1]
	d.P50, d.P95 = &p50, &p95
	return d
}

// observe retains the maximum completed observation for a resource UID.
func observe(values map[string]*float64, uid string, value *float64) {
	previous, exists := values[uid]
	if !exists || (value != nil && (previous == nil || *value > *previous)) {
		values[uid] = value
	}
}

// summarize deduplicates resources across all artifacts in this scope.
func summarize(artifacts []artifact) summary {
	s := summary{Runs: len(artifacts), Timings: map[string]distribution{}, PossibleRepeats: []possibleRepeat{}}
	timings := map[string][]float64{"namespace_initialization": nil, "ci_operator_setup": nil, "ci_operator_total": nil}
	builds, pods := map[string]*float64{}, map[string]*float64{}
	repeats := map[string]map[string]map[string]struct{}{}
	for _, a := range artifacts {
		runTimes := map[string]*float64{}
		var reused *bool
		for _, event := range a.Insights {
			switch event.Name {
			case "namespace_initialized":
				observe(runTimes, "namespace_initialization", event.Context.Duration)
				reused = event.Context.Reused
			case "execution_started":
				observe(runTimes, "ci_operator_setup", event.Context.StartedAfter)
			case "execution_completed":
				observe(runTimes, "ci_operator_total", event.Context.Duration)
			}
		}
		if reused == nil {
			s.Reuse.Missing++
		}
		if reused != nil {
			s.Reuse.Observed++
			if *reused {
				s.Reuse.Reused++
			}
		}
		for name, value := range runTimes {
			if value != nil {
				timings[name] = append(timings[name], *value)
			}
		}
		for _, b := range a.Builds {
			if b.UID == "" {
				s.Builds.MissingUID++
				continue
			}
			observe(builds, b.UID, b.Duration)
			if a.inputHash == "" || b.ForImage == "" {
				continue
			}
			if repeats[a.inputHash] == nil {
				repeats[a.inputHash] = map[string]map[string]struct{}{}
			}
			if repeats[a.inputHash][b.ForImage] == nil {
				repeats[a.inputHash][b.ForImage] = map[string]struct{}{}
			}
			repeats[a.inputHash][b.ForImage][b.UID] = struct{}{}
		}
		for _, p := range a.Pods {
			if p.UID == "" {
				s.Pods.MissingUID++
				continue
			}
			observe(pods, p.UID, p.Scheduling)
		}
	}
	for name, values := range timings {
		s.Timings[name] = percentiles(values)
	}
	s.Builds.Unique = len(builds)
	// Sort UIDs so floating-point summation is reproducible across executions.
	for _, uid := range sortedKeys(builds) {
		if builds[uid] == nil {
			s.Builds.MissingDuration++
			continue
		}
		s.Builds.Duration += *builds[uid]
	}
	s.Pods.Unique = len(pods)
	var latencies []float64
	for _, value := range pods {
		if value == nil {
			s.Pods.MissingLatency++
			continue
		}
		latencies = append(latencies, *value/1e9)
	}
	s.Pods.Scheduling = percentiles(latencies)
	for _, hash := range sortedKeys(repeats) {
		for _, image := range sortedKeys(repeats[hash]) {
			if count := len(repeats[hash][image]); count > 1 {
				s.PossibleRepeats = append(s.PossibleRepeats, possibleRepeat{hash, image, count})
			}
		}
	}
	return s
}

// sortedKeys provides deterministic job and resource ordering without external dependencies.
func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

// byJob groups all distinct run identities within each exact Prow job name.
func byJob(artifacts []artifact) map[string][]artifact {
	jobs := map[string][]artifact{}
	for _, a := range artifacts {
		jobs[a.job] = append(jobs[a.job], a)
	}
	return jobs
}

// makeCohort applies the same job-name filter to each side of a comparison.
func makeCohort(r reader, matched []string) cohort {
	c := cohort{Files: r.files, Duplicates: r.duplicates, Overall: summarize(r.artifacts), Jobs: map[string]summary{}}
	jobs := byJob(r.artifacts)
	for job, artifacts := range jobs {
		c.Jobs[job] = summarize(artifacts)
	}
	var selected []artifact
	for _, job := range matched {
		selected = append(selected, jobs[job]...)
	}
	c.Matched = summarize(selected)
	return c
}

// compare exposes unmatched families instead of hiding changes in workload mix.
func compare(baseline, candidate reader) comparison {
	c := comparison{Matched: []string{}, BaselineOnly: []string{}, CandidateOnly: []string{}}
	baseJobs, candidateJobs := byJob(baseline.artifacts), byJob(candidate.artifacts)
	for _, name := range sortedKeys(baseJobs) {
		if _, found := candidateJobs[name]; found {
			c.Matched = append(c.Matched, name)
			continue
		}
		c.BaselineOnly = append(c.BaselineOnly, name)
	}
	for _, name := range sortedKeys(candidateJobs) {
		if _, found := baseJobs[name]; !found {
			c.CandidateOnly = append(c.CandidateOnly, name)
		}
	}
	c.Baseline, c.Candidate = makeCohort(baseline, c.Matched), makeCohort(candidate, c.Matched)
	return c
}

// run separates command errors from process exit for straightforward testing.
func run(baseline, candidate string) error {
	if baseline == "" || candidate == "" {
		return fmt.Errorf("both --baseline and --candidate directories are required")
	}
	b, err := readArtifacts(baseline)
	if err != nil {
		return fmt.Errorf("baseline: %w", err)
	}
	c, err := readArtifacts(candidate)
	if err != nil {
		return fmt.Errorf("candidate: %w", err)
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(compare(b, c)); err != nil {
		return fmt.Errorf("writing comparison: %w", err)
	}
	return nil
}

// main accepts local artifacts so measurement never needs credentials or network access.
func main() {
	baseline := flag.String("baseline", "", "directory of baseline ci-operator-metrics.json artifacts")
	candidate := flag.String("candidate", "", "directory of candidate ci-operator-metrics.json artifacts")
	flag.Parse()
	if err := run(*baseline, *candidate); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
