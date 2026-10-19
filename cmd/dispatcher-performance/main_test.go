package main

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// metricArtifact makes independent runs explicit while keeping test observations readable.
func metricArtifact(job, id, target, observations string) string {
	return `{"test_platform_insights":[{"name":"started","additional_context":{"job_spec":{"job":"` + job + `","prowjobid":"` + id + `","target":"` + target + `"}}}` + observations + `]}`
}

// writeArtifacts models nested downloaded artifact directories in lexical order.
func writeArtifacts(t *testing.T, contents []string) string {
	t.Helper()
	dir := t.TempDir()
	for i, content := range contents {
		path := filepath.Join(dir, string(rune('a'+i)))
		if err := os.Mkdir(path, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, "ci-operator-metrics.json"), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// withResources adds build and pod observations independently of insight fixtures.
func withResources(raw, builds, pods string) string {
	return strings.TrimSuffix(raw, "}") + `,"openshift_builds":` + builds + `,"pods":` + pods + `}`
}

func TestReadArtifacts(t *testing.T) {
	first := metricArtifact("job", "run", "target", "")
	for _, tc := range []struct {
		name                     string
		files                    []string
		wantRuns, wantDuplicates int
		err                      string
	}{
		{name: "run identity deduplicates distinct files", files: []string{first, metricArtifact("job", "run", "target", `,{"name":"execution_started","additional_context":{"started_after":7}}`)}, wantRuns: 1, wantDuplicates: 1},
		{name: "targets remain separate", files: []string{first, metricArtifact("job", "run", "other", "")}, wantRuns: 2},
		{name: "same job independent runs", files: []string{first, metricArtifact("job", "second", "target", "")}, wantRuns: 2},
		{name: "content fallback removes copies", files: []string{metricArtifact("job", "", "", ""), metricArtifact("job", "", "", "")}, wantRuns: 1, wantDuplicates: 1},
		{name: "empty directory", err: "no ci-operator-metrics.json artifacts"},
		{name: "malformed JSON", files: []string{"{"}, err: "unexpected end of JSON input"},
		{name: "missing job identity", files: []string{"{}"}, err: "missing started job_spec.job"},
		{name: "duplicate still validated", files: []string{first, metricArtifact("job", "run", "target", `,{"name":"execution_started","additional_context":{"started_after":-1}}`)}, err: "negative or nonfinite insight time"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, err := readArtifacts(writeArtifacts(t, tc.files))
			if tc.err != "" {
				if err == nil || !strings.Contains(err.Error(), tc.err) {
					t.Fatalf("want error %q, got %v", tc.err, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(r.artifacts) != tc.wantRuns || r.duplicates != tc.wantDuplicates || r.files != len(tc.files) {
				t.Fatalf("unexpected counts: files=%d runs=%d duplicates=%d", r.files, len(r.artifacts), r.duplicates)
			}
		})
	}
}

func TestSummarize(t *testing.T) {
	init := `,{"name":"namespace_initialized","additional_context":{"input_hash":"hash","reused":true,"duration_seconds":4}},{"name":"execution_started","additional_context":{"started_after":10}},{"name":"execution_completed","additional_context":{"duration_seconds":30}}`
	for _, tc := range []struct {
		name        string
		files       []string
		wantBuilds  buildSummary
		wantPods    podSummary
		wantReuse   reuseCoverage
		wantSamples int
		wantRepeats []possibleRepeat
	}{
		{
			name: "shared resource UIDs use maximum observation across jobs",
			files: []string{
				withResources(metricArtifact("job-a", "run-a", "", init), `[{"uid":"shared","for_image":"image","duration_seconds":12,"start_time":"2026-01-01T00:00:00Z","completion_time":"2026-01-01T00:00:12Z","status":"Complete"}]`, `[{"uid":"pod","scheduling_latency":1000000000}]`),
				withResources(metricArtifact("job-b", "run-b", "", init), `[{"uid":"shared","for_image":"image","duration_seconds":15,"start_time":"2026-01-01T00:00:00Z","completion_time":"2026-01-01T00:00:15Z","status":"Complete"},{"uid":"second","for_image":"image","duration_seconds":3,"start_time":"2026-01-01T00:00:00Z","completion_time":"2026-01-01T00:00:03Z","status":"Complete"}]`, `[{"uid":"pod","scheduling_latency":2000000000}]`),
			},
			wantBuilds: buildSummary{Unique: 2, Duration: 18}, wantPods: podSummary{Unique: 1, Scheduling: percentiles([]float64{2})},
			wantReuse: reuseCoverage{Reused: 2, Observed: 2}, wantSamples: 2, wantRepeats: []possibleRepeat{{"hash", "image", 2}},
		},
		{
			name:       "legacy artifacts expose missing observation coverage",
			files:      []string{withResources(metricArtifact("job", "run", "", ""), `[{"duration_seconds":12},{"uid":"no-duration"}]`, `[{"scheduling_latency":1000000000},{"uid":"no-latency"}]`)},
			wantBuilds: buildSummary{Unique: 1, MissingUID: 1, MissingDuration: 1}, wantPods: podSummary{Unique: 1, MissingUID: 1, MissingLatency: 1}, wantReuse: reuseCoverage{Missing: 1}, wantRepeats: []possibleRepeat{},
		},
		{
			name:      "observed false reuse is in denominator",
			files:     []string{metricArtifact("job", "run", "", `,{"name":"namespace_initialized","additional_context":{"reused":false,"duration_seconds":0}},{"name":"execution_started","additional_context":{"started_after":0}},{"name":"execution_completed","additional_context":{"duration_seconds":0}}`)},
			wantReuse: reuseCoverage{Observed: 1}, wantSamples: 1, wantRepeats: []possibleRepeat{},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, err := readArtifacts(writeArtifacts(t, tc.files))
			if err != nil {
				t.Fatal(err)
			}
			got := summarize(r.artifacts)
			if !reflect.DeepEqual(got.Builds, tc.wantBuilds) || !reflect.DeepEqual(got.Pods, tc.wantPods) || got.Reuse != tc.wantReuse || !reflect.DeepEqual(got.PossibleRepeats, tc.wantRepeats) {
				t.Fatalf("unexpected summary: %+v", got)
			}
			for _, name := range []string{"namespace_initialization", "ci_operator_setup", "ci_operator_total"} {
				if got.Timings[name].Samples != tc.wantSamples {
					t.Errorf("%s samples: want %d, got %d", name, tc.wantSamples, got.Timings[name].Samples)
				}
			}
		})
	}
}

func TestBuildDurationCoverage(t *testing.T) {
	for _, tc := range []struct {
		name, observation string
		want              buildSummary
		err               string
	}{
		{name: "pending producer default zero", observation: `{"uid":"build","start_time":"0001-01-01T00:00:00Z","completion_time":"0001-01-01T00:00:00Z","duration_seconds":0,"status":"Pending"}`, want: buildSummary{Unique: 1, MissingDuration: 1}},
		{name: "running producer default zero", observation: `{"uid":"build","start_time":"2026-01-01T00:00:00Z","completion_time":"0001-01-01T00:00:00Z","duration_seconds":0,"status":"Running"}`, want: buildSummary{Unique: 1, MissingDuration: 1}},
		{name: "failed before start", observation: `{"uid":"build","start_time":"0001-01-01T00:00:00Z","completion_time":"2026-01-01T00:00:00Z","duration_seconds":0,"status":"Failed"}`, want: buildSummary{Unique: 1, MissingDuration: 1}},
		{name: "completed actual zero", observation: `{"uid":"build","start_time":"2026-01-01T00:00:00Z","completion_time":"2026-01-01T00:00:00Z","duration_seconds":0,"status":"Complete"}`, want: buildSummary{Unique: 1}},
		{name: "failed measured fractional interval", observation: `{"uid":"build","start_time":"2026-01-01T00:00:00Z","completion_time":"2026-01-01T00:00:01.5Z","duration_seconds":1,"status":"Failed"}`, want: buildSummary{Unique: 1, Duration: 1.5}},
		{name: "duration without timestamps is unmeasured", observation: `{"uid":"build","duration_seconds":12,"status":"Complete"}`, want: buildSummary{Unique: 1, MissingDuration: 1}},
		{name: "nonterminal interval is unmeasured", observation: `{"uid":"build","start_time":"2026-01-01T00:00:00Z","completion_time":"2026-01-01T00:00:01Z","duration_seconds":1,"status":"Running"}`, want: buildSummary{Unique: 1, MissingDuration: 1}},
		{name: "reversed interval rejected", observation: `{"uid":"build","start_time":"2026-01-01T00:00:01Z","completion_time":"2026-01-01T00:00:00Z","duration_seconds":0,"status":"Complete"}`, err: "build completion precedes start"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := withResources(metricArtifact("job", "run", "", ""), "["+tc.observation+"]", "[]")
			a, err := parseArtifact([]byte(raw))
			if tc.err != "" {
				if err == nil || !strings.Contains(err.Error(), tc.err) {
					t.Fatalf("want error %q, got %v", tc.err, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := summarize([]artifact{a}).Builds; got != tc.want {
				t.Fatalf("want %+v, got %+v", tc.want, got)
			}
		})
	}
}

func TestCompare(t *testing.T) {
	for _, tc := range []struct {
		name                                 string
		baseline, candidate                  []string
		matched, baselineOnly, candidateOnly []string
	}{
		{name: "mixed job families", baseline: []string{"common", "old"}, candidate: []string{"common", "new"}, matched: []string{"common"}, baselineOnly: []string{"old"}, candidateOnly: []string{"new"}},
		{name: "no matched families", baseline: []string{"old"}, candidate: []string{"new"}, matched: []string{}, baselineOnly: []string{"old"}, candidateOnly: []string{"new"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var baseline, candidate reader
			for _, name := range tc.baseline {
				baseline.artifacts = append(baseline.artifacts, artifact{job: name})
			}
			for _, name := range tc.candidate {
				candidate.artifacts = append(candidate.artifacts, artifact{job: name})
			}
			got := compare(baseline, candidate)
			if !reflect.DeepEqual(got.Matched, tc.matched) || !reflect.DeepEqual(got.BaselineOnly, tc.baselineOnly) || !reflect.DeepEqual(got.CandidateOnly, tc.candidateOnly) {
				t.Fatalf("unexpected coverage: %+v", got)
			}
			if got.Baseline.Matched.Runs != len(tc.matched) || got.Candidate.Matched.Runs != len(tc.matched) || len(got.Baseline.Jobs) != len(tc.baseline) {
				t.Fatalf("unexpected run counts: %+v", got)
			}
			first, err := json.Marshal(got)
			if err != nil {
				t.Fatal(err)
			}
			second, err := json.Marshal(compare(baseline, candidate))
			if err != nil {
				t.Fatal(err)
			}
			if string(first) != string(second) {
				t.Fatal("output is not deterministic")
			}
		})
	}
}

func TestPercentiles(t *testing.T) {
	for _, tc := range []struct {
		name     string
		values   []float64
		p50, p95 float64
	}{
		{name: "no samples"},
		{name: "one sample", values: []float64{7}, p50: 7, p95: 7},
		{name: "two samples nearest rank", values: []float64{8, 2}, p50: 2, p95: 8},
		{name: "twenty samples boundary", values: []float64{20, 19, 18, 17, 16, 15, 14, 13, 12, 11, 10, 9, 8, 7, 6, 5, 4, 3, 2, 1}, p50: 10, p95: 19},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := percentiles(tc.values)
			if got.Samples != len(tc.values) {
				t.Fatal(got)
			}
			if len(tc.values) == 0 {
				if got.P50 != nil || got.P95 != nil {
					t.Fatal(got)
				}
				return
			}
			if *got.P50 != tc.p50 || *got.P95 != tc.p95 {
				t.Fatalf("unexpected percentiles: %+v", got)
			}
		})
	}
}

func TestInvalidMeasurements(t *testing.T) {
	for _, tc := range []struct{ name, raw, err string }{
		{name: "negative build", raw: withResources(metricArtifact("job", "id", "", ""), `[{"duration_seconds":-1}]`, `[]`), err: "negative or nonfinite build duration"},
		{name: "negative scheduling", raw: withResources(metricArtifact("job", "id", "", ""), `[]`, `[{"scheduling_latency":-1}]`), err: "negative or nonfinite pod scheduling latency"},
		{name: "overflow JSON float", raw: metricArtifact("job", "id", "", `,{"name":"execution_completed","additional_context":{"duration_seconds":1e999}}`), err: "cannot unmarshal number"},
		{name: "invalid JSON NaN", raw: metricArtifact("job", "id", "", `,{"name":"execution_completed","additional_context":{"duration_seconds":NaN}}`), err: "invalid character"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseArtifact([]byte(tc.raw))
			if err == nil || !strings.Contains(err.Error(), tc.err) {
				t.Fatalf("want %q, got %v", tc.err, err)
			}
		})
	}
	for _, tc := range []struct {
		name  string
		value float64
		valid bool
	}{
		{name: "nan", value: math.NaN()}, {name: "positive infinity", value: math.Inf(1)}, {name: "negative infinity", value: math.Inf(-1)}, {name: "zero", valid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := validTime(&tc.value); got != tc.valid {
				t.Fatalf("validTime = %v, want %v", got, tc.valid)
			}
		})
	}
}
