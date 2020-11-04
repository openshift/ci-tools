package deprecatetemplates

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"k8s.io/test-infra/prow/config"
)

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
				UnknownBlocker: deprecatedTemplateBlocker{Jobs: blockedJobs{job: blockedJob{Generated: false, Kind: "unknown"}}},
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
						Jobs: blockedJobs{job: blockedJob{Generated: false, Kind: "unknown"}},
					},
				},
			},
		},
		{
			description: "adding job already in unknown blockers is a nop",
			existingDT: deprecatedTemplate{
				UnknownBlocker: deprecatedTemplateBlocker{Jobs: blockedJobs{job: blockedJob{Generated: false, Kind: "unknown"}}},
			},
			expectedDT: deprecatedTemplate{
				UnknownBlocker: deprecatedTemplateBlocker{Jobs: blockedJobs{job: blockedJob{Generated: false, Kind: "unknown"}}},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			tc.existingDT.insert(config.JobBase{Name: job})
			if diff := cmp.Diff(tc.existingDT, tc.expectedDT); diff != "" {
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
			if diff := cmp.Diff(&expected, &actual, cmpopts.IgnoreUnexported(allowlist{})); diff != "" {
				t.Errorf("%s: allowlist differs from expected:\n%s", tc.description, diff)
			}
		})
	}
}
