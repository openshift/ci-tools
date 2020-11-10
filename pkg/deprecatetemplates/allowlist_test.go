package deprecatetemplates

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"k8s.io/test-infra/prow/config"
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
				UnknownBlocker: deprecatedTemplateBlocker{
					Description: "unknown",
					Jobs:        blockedJobs{"job": blockedJob{current: false}},
				},
			},
			expected: deprecatedTemplate{
				UnknownBlocker: deprecatedTemplateBlocker{
					Description: "unknown",
					Jobs:        blockedJobs{},
				},
			},
		},
		{
			description: "non-current job is removed from unknown blockers, current is kept",
			input: deprecatedTemplate{
				UnknownBlocker: deprecatedTemplateBlocker{
					Description: "unknown",
					Jobs: blockedJobs{
						"job":         blockedJob{current: false},
						"current-job": blockedJob{current: true},
					},
				},
			},
			expected: deprecatedTemplate{
				UnknownBlocker: deprecatedTemplateBlocker{
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
				UnknownBlocker: deprecatedTemplateBlocker{Description: "unknown"},
			},
			expected: deprecatedTemplate{
				UnknownBlocker: deprecatedTemplateBlocker{Description: "unknown"},
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
				UnknownBlocker: deprecatedTemplateBlocker{Description: "unknown"},
			},
			expected: deprecatedTemplate{
				UnknownBlocker: deprecatedTemplateBlocker{Description: "unknown"},
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
				UnknownBlocker: deprecatedTemplateBlocker{Description: "unknown"},
			},
			expected: deprecatedTemplate{
				UnknownBlocker: deprecatedTemplateBlocker{Description: "unknown"},
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			tc.input.prune()
			if diff := cmp.Diff(tc.input, tc.expected, cmp.AllowUnexported(blockedJob{})); diff != "" {
				t.Errorf("%s: deprecated template record differs from expected:\n%s", tc.description, diff)
			}
		})
	}
}

func TestDeprecatedTemplateInsert(t *testing.T) {
	job := "inserted-job"

	testCases := []struct {
		description string
		existingDT  deprecatedTemplate
		expectedDT  deprecatedTemplate
	}{
		{
			description: "new job is added to unknown blockers",
			existingDT: deprecatedTemplate{
				UnknownBlocker: deprecatedTemplateBlocker{Jobs: blockedJobs{}},
			},
			expectedDT: deprecatedTemplate{
				UnknownBlocker: deprecatedTemplateBlocker{Jobs: blockedJobs{job: blockedJob{Generated: false, Kind: "unknown", current: true}}},
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
				UnknownBlocker: deprecatedTemplateBlocker{Jobs: blockedJobs{job: blockedJob{Generated: false, Kind: "unknown"}}},
			},
			expectedDT: deprecatedTemplate{
				UnknownBlocker: deprecatedTemplateBlocker{Jobs: blockedJobs{job: blockedJob{Generated: false, Kind: "unknown", current: true}}},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			tc.existingDT.insert(config.JobBase{Name: job})
			if diff := cmp.Diff(tc.existingDT, tc.expectedDT, cmp.AllowUnexported(blockedJob{})); diff != "" {
				t.Errorf("%s: deprecated template record differs from expected:\n%s", tc.description, diff)
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
		before        map[string]deprecatedTemplate
		expectedAfter map[string]deprecatedTemplate
	}{
		{
			description: "add job to new template record",
			expectedAfter: map[string]deprecatedTemplate{
				template: {
					Name: template,
					UnknownBlocker: deprecatedTemplateBlocker{
						Description: "unknown",
						Jobs:        blockedJobs{job: blockedJob{Generated: false, Kind: "unknown"}},
					},
				},
			},
		},
		{
			description: "add job to existing template record",
			before: map[string]deprecatedTemplate{
				template: {
					Name: template,
					UnknownBlocker: deprecatedTemplateBlocker{
						Jobs: blockedJobs{job: blockedJob{Generated: false, Kind: "unknown"}},
					},
				},
			},
			expectedAfter: map[string]deprecatedTemplate{
				template: {
					Name: template,
					UnknownBlocker: deprecatedTemplateBlocker{
						Jobs: blockedJobs{job: blockedJob{Generated: false, Kind: "unknown"}},
					},
					Blockers: nil,
				},
			},
		},
		{
			description: "add job to existing template record, already known blocker",
			before: map[string]deprecatedTemplate{
				template: {
					Name: template,
					Blockers: map[string]deprecatedTemplateBlocker{
						"DPTP-1234": {
							Jobs: blockedJobs{job: blockedJob{Generated: false, Kind: "unknown"}},
						},
					},
				},
			},
			expectedAfter: map[string]deprecatedTemplate{
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
			before: map[string]deprecatedTemplate{
				template: {
					Name: template,
					UnknownBlocker: deprecatedTemplateBlocker{
						Jobs: blockedJobs{
							job:        blockedJob{Generated: false, Kind: "unknown"},
							anotherJob: blockedJob{Generated: false, Kind: "unknown"},
						},
					},
				},
			},
			expectedAfter: map[string]deprecatedTemplate{
				template: {
					Name: template,
					UnknownBlocker: deprecatedTemplateBlocker{
						Jobs: blockedJobs{
							job:        blockedJob{Generated: false, Kind: "unknown"},
							anotherJob: blockedJob{Generated: false, Kind: "unknown"},
						},
					},
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			actual := allowlist{Templates: tc.before}
			actual.Insert(config.JobBase{Name: job}, template)
			expected := allowlist{Templates: tc.expectedAfter}
			if diff := cmp.Diff(&expected, &actual, cmpopts.IgnoreUnexported(allowlist{}, blockedJob{})); diff != "" {
				t.Errorf("%s: allowlist differs from expected:\n%s", tc.description, diff)
			}
		})
	}
}
