package deprecatetemplates

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"sigs.k8s.io/prow/pkg/config"
)

func TestDeprecatedTemplatePrune(t *testing.T) {
	testCases := []struct {
		description string
		input       deprecatedTemplate
		expected    deprecatedTemplate
	}{
		{
			description: "non-current job is removed from unknown blockers",
			input: deprecatedTemplate{
				UnknownBlocker: &deprecatedTemplateBlocker{
					Description: "unknown",
					Jobs:        blockedJobs{"job": blockedJob{current: false}},
				},
			},
			expected: deprecatedTemplate{
				UnknownBlocker: &deprecatedTemplateBlocker{
					Description: "unknown",
					Jobs:        blockedJobs{},
				},
			},
		},
		{
			description: "non-current job is removed from unknown blockers, current is kept",
			input: deprecatedTemplate{
				UnknownBlocker: &deprecatedTemplateBlocker{
					Description: "unknown",
					Jobs: blockedJobs{
						"job":         blockedJob{current: false},
						"current-job": blockedJob{current: true},
					},
				},
			},
			expected: deprecatedTemplate{
				UnknownBlocker: &deprecatedTemplateBlocker{
					Description: "unknown",
					Jobs: blockedJobs{
						"current-job": blockedJob{current: true},
					},
				},
			},
		},
		{
			description: "non-current job is removed from all blockers, current jobs are kept",
			input: deprecatedTemplate{
				Blockers: map[string]deprecatedTemplateBlocker{
					"BLOCKER-1": {Jobs: blockedJobs{
						"job":         blockedJob{current: false},
						"current-job": blockedJob{current: true},
					}},
					"BLOCKER-2": {Jobs: blockedJobs{
						"job":         blockedJob{current: false},
						"current-job": blockedJob{current: true},
					}},
				},
				UnknownBlocker: &deprecatedTemplateBlocker{Description: "unknown"},
			},
			expected: deprecatedTemplate{
				UnknownBlocker: &deprecatedTemplateBlocker{Description: "unknown"},
				Blockers: map[string]deprecatedTemplateBlocker{
					"BLOCKER-1": {Jobs: blockedJobs{
						"current-job": blockedJob{current: true},
					}},
					"BLOCKER-2": {Jobs: blockedJobs{
						"current-job": blockedJob{current: true},
					}},
				},
			},
		},
		{
			description: "blocker without jobs is removed, blocker with jobs is kept",
			input: deprecatedTemplate{
				Blockers: map[string]deprecatedTemplateBlocker{
					"BLOCKER-KEPT":    {Jobs: blockedJobs{"current-job": blockedJob{current: true}}},
					"BLOCKER-REMOVED": {Jobs: blockedJobs{"job": blockedJob{current: false}}},
				},
				UnknownBlocker: &deprecatedTemplateBlocker{Description: "unknown"},
			},
			expected: deprecatedTemplate{
				UnknownBlocker: &deprecatedTemplateBlocker{Description: "unknown"},
				Blockers: map[string]deprecatedTemplateBlocker{
					"BLOCKER-KEPT": {Jobs: blockedJobs{"current-job": blockedJob{current: true}}},
				},
			},
		},
		{
			description: "blockers are pruned entirely when all jobs are pruned",
			input: deprecatedTemplate{
				Blockers: map[string]deprecatedTemplateBlocker{
					"BLOCKER-REMOVED":     {Jobs: blockedJobs{"job": blockedJob{current: false}}},
					"BLOCKER-REMOVED-TOO": {Jobs: blockedJobs{"another-job": blockedJob{current: false}}},
				},
				UnknownBlocker: &deprecatedTemplateBlocker{Description: "unknown"},
			},
			expected: deprecatedTemplate{
				UnknownBlocker: &deprecatedTemplateBlocker{Description: "unknown"},
			},
		},
	}
	allowUnexported := cmp.AllowUnexported(blockedJob{}, deprecatedTemplateBlocker{})
	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			tc.input.prune()
			if diff := cmp.Diff(tc.input, tc.expected, allowUnexported); diff != "" {
				t.Errorf("%s: deprecated template record differs from expected:\n%s", tc.description, diff)
			}
		})
	}
}

func TestDeprecatedTemplateInsert(t *testing.T) {
	job := "inserted-job"

	testCases := []struct {
		description string
		blockers    JiraHints
		existingDT  deprecatedTemplate
		expectedDT  deprecatedTemplate
	}{
		{
			description: "new job is added to unknown blockers by default",
			existingDT: deprecatedTemplate{
				UnknownBlocker: &deprecatedTemplateBlocker{Jobs: blockedJobs{}},
			},
			expectedDT: deprecatedTemplate{
				UnknownBlocker: &deprecatedTemplateBlocker{Jobs: blockedJobs{job: blockedJob{Generated: false, Kind: "unknown", current: true}}},
			},
		},
		{
			description: "new job is added to specified blockers if set",
			blockers: JiraHints{
				"HERE": "serious blocker",
				"YADA": "ya does not correctly da",
			},
			existingDT: deprecatedTemplate{
				UnknownBlocker: &deprecatedTemplateBlocker{Jobs: blockedJobs{}},
			},
			expectedDT: deprecatedTemplate{
				UnknownBlocker: &deprecatedTemplateBlocker{Jobs: blockedJobs{}},
				Blockers: map[string]deprecatedTemplateBlocker{
					"HERE": {
						Description: "serious blocker",
						Jobs:        blockedJobs{job: blockedJob{Generated: false, Kind: "unknown", current: true}},
					},
					"YADA": {
						Description: "ya does not correctly da",
						Jobs:        blockedJobs{job: blockedJob{Generated: false, Kind: "unknown", current: true}},
					},
				},
			},
		},
		{
			description: "job with a known blocker is not added to unknown blockers",
			existingDT: deprecatedTemplate{
				Blockers: map[string]deprecatedTemplateBlocker{
					"DPTP-1235": {
						Jobs: blockedJobs{job: blockedJob{Generated: false, Kind: "unknown"}},
					},
				},
			},
			expectedDT: deprecatedTemplate{
				Blockers: map[string]deprecatedTemplateBlocker{
					"DPTP-1235": {
						Jobs: blockedJobs{job: blockedJob{Generated: false, Kind: "unknown", current: true}},
					},
				},
			},
		},
		{
			description: "adding job already in unknown blockers only sets the current field",
			existingDT: deprecatedTemplate{
				UnknownBlocker: &deprecatedTemplateBlocker{Jobs: blockedJobs{job: blockedJob{Generated: false, Kind: "unknown"}}},
			},
			expectedDT: deprecatedTemplate{
				UnknownBlocker: &deprecatedTemplateBlocker{Jobs: blockedJobs{job: blockedJob{Generated: false, Kind: "unknown", current: true}}},
			},
		},
		{
			description: "do not choke on nil map",
			existingDT: deprecatedTemplate{
				UnknownBlocker: &deprecatedTemplateBlocker{Jobs: nil},
			},
			expectedDT: deprecatedTemplate{
				UnknownBlocker: &deprecatedTemplateBlocker{
					Jobs:       blockedJobs{job: blockedJob{Generated: false, Kind: "unknown", current: true}},
					newlyAdded: true,
				},
			},
		},
	}

	allowUnexported := cmp.AllowUnexported(blockedJob{}, deprecatedTemplateBlocker{})
	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			if err := tc.existingDT.insert(config.JobBase{Name: job}, tc.blockers); err != nil {
				t.Fatalf("received error: %v", err)
			}
			if diff := cmp.Diff(tc.existingDT, tc.expectedDT, allowUnexported); diff != "" {
				t.Fatalf("%s: deprecated template record differs from expected:\n%s", tc.description, diff)
			}
		})
	}
}

func TestAllowlistInsert(t *testing.T) {
	template := "template"
	job := "job"
	anotherJob := "another-job"

	testCases := []struct {
		description   string
		before        map[string]*deprecatedTemplate
		expectedAfter map[string]*deprecatedTemplate
	}{
		{
			description: "add job to new template record",
			expectedAfter: map[string]*deprecatedTemplate{
				template: {
					Name: template,
					UnknownBlocker: &deprecatedTemplateBlocker{
						Description: "unknown",
						Jobs:        blockedJobs{job: blockedJob{Generated: false, Kind: "unknown"}},
					},
				},
			},
		},
		{
			description: "add job to existing template record",
			before: map[string]*deprecatedTemplate{
				template: {
					Name: template,
					UnknownBlocker: &deprecatedTemplateBlocker{
						Jobs: blockedJobs{job: blockedJob{Generated: false, Kind: "unknown"}},
					},
				},
			},
			expectedAfter: map[string]*deprecatedTemplate{
				template: {
					Name: template,
					UnknownBlocker: &deprecatedTemplateBlocker{
						Jobs: blockedJobs{job: blockedJob{Generated: false, Kind: "unknown"}},
					},
					Blockers: nil,
				},
			},
		},
		{
			description: "add job to existing template record, already known blocker",
			before: map[string]*deprecatedTemplate{
				template: {
					Name: template,
					Blockers: map[string]deprecatedTemplateBlocker{
						"DPTP-1234": {
							Jobs: blockedJobs{job: blockedJob{Generated: false, Kind: "unknown"}},
						},
					},
				},
			},
			expectedAfter: map[string]*deprecatedTemplate{
				template: {
					Name: template,
					Blockers: map[string]deprecatedTemplateBlocker{
						"DPTP-1234": {
							Jobs: blockedJobs{job: blockedJob{Generated: false, Kind: "unknown"}},
						},
					},
				},
			},
		},
		{
			description: "add job to existing template record, already unknown blocker",
			before: map[string]*deprecatedTemplate{
				template: {
					Name: template,
					UnknownBlocker: &deprecatedTemplateBlocker{
						Jobs: blockedJobs{
							job:        blockedJob{Generated: false, Kind: "unknown"},
							anotherJob: blockedJob{Generated: false, Kind: "unknown"},
						},
					},
				},
			},
			expectedAfter: map[string]*deprecatedTemplate{
				template: {
					Name: template,
					UnknownBlocker: &deprecatedTemplateBlocker{
						Jobs: blockedJobs{
							job:        blockedJob{Generated: false, Kind: "unknown"},
							anotherJob: blockedJob{Generated: false, Kind: "unknown"},
						},
					},
				},
			},
		},
	}

	ignoreUnexported := cmpopts.IgnoreUnexported(allowlist{}, blockedJob{}, deprecatedTemplateBlocker{})
	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			actual := allowlist{Templates: tc.before}
			if err := actual.Insert(config.JobBase{Name: job}, template); err != nil {
				t.Fatalf("received error: %v", err)
			}
			expected := allowlist{Templates: tc.expectedAfter}
			if diff := cmp.Diff(&expected, &actual, ignoreUnexported); diff != "" {
				t.Fatalf("%s: allowlist differs from expected:\n%s", tc.description, diff)
			}
		})
	}
}

func TestStatsFromJobs(t *testing.T) {
	jobs := blockedJobs{
		"1": blockedJob{Generated: false, Kind: "presubmit"},
		"2": blockedJob{Generated: false, Kind: "presubmit"},
		"3": blockedJob{Generated: true, Kind: "periodic"},
		"4": blockedJob{Generated: true, Kind: "release"},
		"5": blockedJob{Generated: false, Kind: "unknown"},
	}
	stats := statsFromJobs("name", "blocker", jobs)
	expected := statsLine{
		template:    "name",
		blocker:     "blocker",
		total:       5,
		handcrafted: 3,
		generated:   2,
		presubmits:  2,
		postsubmits: 0,
		release:     1,
		periodics:   1,
		unknown:     1,
	}
	if diff := cmp.Diff(stats, expected, cmp.AllowUnexported(statsLine{})); diff != "" {
		t.Errorf("stats differ from expected:\n%s", diff)
	}
}

func TestAllowlistStats(t *testing.T) {
	d := deprecatedTemplate{
		Name: "template",
		UnknownBlocker: &deprecatedTemplateBlocker{
			Jobs: map[string]blockedJob{
				"1": {Generated: true, Kind: "presubmit"},
				"2": {Generated: false, Kind: "presubmit"},
			},
		},
		Blockers: map[string]deprecatedTemplateBlocker{
			"blocker-1": {
				Jobs: blockedJobs{
					"3": {Generated: false, Kind: "periodic"},
					"4": {Generated: false, Kind: "periodic"},
					"5": {Generated: true, Kind: "release"},
				},
			},
			"blocker-2": {
				Jobs: blockedJobs{
					"5": {Generated: true, Kind: "release"},
					"6": {Generated: false, Kind: "unknown"},
				},
			},
		},
	}
	expectedTotal := statsLine{template: "template", blocker: blockerColTotal, total: 6, handcrafted: 4, generated: 2, presubmits: 2, postsubmits: 0, release: 1, periodics: 2, unknown: 1}
	expectedUnknown := statsLine{template: "template", blocker: blockerColUnknown, total: 2, handcrafted: 1, generated: 1, presubmits: 2}
	expectedBlockers := []statsLine{
		{template: "template", blocker: "blocker-1", total: 3, handcrafted: 2, generated: 1, periodics: 2, release: 1},
		{template: "template", blocker: "blocker-2", total: 2, handcrafted: 1, generated: 1, release: 1, unknown: 1},
	}
	total, unknown, blockers := d.Stats()
	allowunexported := cmp.AllowUnexported(statsLine{})
	if diff := cmp.Diff(expectedTotal, total, allowunexported); diff != "" {
		t.Errorf("Total stats differ from expected:\n%s", diff)
	}
	if diff := cmp.Diff(expectedUnknown, unknown, allowunexported); diff != "" {
		t.Errorf("Unknown blocker stats differ from expected:\n%s", diff)
	}
	if diff := cmp.Diff(expectedBlockers, blockers, allowunexported); diff != "" {
		t.Errorf("Blocker stats differ from expected:\n%s", diff)
	}
}
