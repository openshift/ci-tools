package bumper_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/openshift/ci-tools/pkg/branchcuts/bumper"
)

func TestBumpReleaseControllerConfig(t *testing.T) {
	tests := []struct {
		name              string
		releaseConfig     *bumper.ReleaseConfig
		wantReleaseConfig *bumper.ReleaseConfig
		ocpRelease        string
	}{
		{
			name: "Bump config properly",
			releaseConfig: &bumper.ReleaseConfig{
				Name:             "name*4.10",
				Message:          "message-4.10",
				MirrorPrefix:     "mirror-prefix_4.10",
				OverrideCLIImage: "cli-4.10",
				Check: map[string]bumper.ReleaseCheck{
					"check1": {
						ConsistentImages: &bumper.CheckConsistentImages{
							Parent: "parent_4.10",
						},
					},
				},
				Verify: map[string]bumper.ReleaseVerification{
					"verify1": {
						ProwJob: &bumper.ProwJobVerification{
							Name: "pj-4.10",
						},
					},
				},
				Publish: map[string]bumper.ReleasePublish{
					"pub1": {
						VerifyBugs: &bumper.PublishVerifyBugs{
							PreviousReleaseTag: &bumper.VerifyBugsTagInfo{
								Name: "prt-4.10",
								Tag:  "t-4.10-from-4.9",
							},
						},
						ImageStreamRef: &bumper.PublishStreamReference{
							Name: "isr_4.10",
						},
						TagRef: &bumper.PublishTagReference{
							Name: "tr_4.10",
						},
					},
				},
			},
			wantReleaseConfig: &bumper.ReleaseConfig{
				Name:             "name*4.11",
				Message:          "message-4.11",
				MirrorPrefix:     "mirror-prefix_4.11",
				OverrideCLIImage: "cli-4.11",
				Check: map[string]bumper.ReleaseCheck{
					"check1": {
						ConsistentImages: &bumper.CheckConsistentImages{
							Parent: "parent_4.11",
						},
					},
				},
				Verify: map[string]bumper.ReleaseVerification{
					"verify1": {
						ProwJob: &bumper.ProwJobVerification{
							Name: "pj-4.11",
						},
					},
				},
				Publish: map[string]bumper.ReleasePublish{
					"pub1": {
						VerifyBugs: &bumper.PublishVerifyBugs{
							PreviousReleaseTag: &bumper.VerifyBugsTagInfo{
								Name: "prt-4.11",
								Tag:  "t-4.11-from-4.10",
							},
						},
						ImageStreamRef: &bumper.PublishStreamReference{
							Name: "isr_4.11",
						},
						TagRef: &bumper.PublishTagReference{
							Name: "tr_4.11",
						},
					},
				},
			},
			ocpRelease: "4.10",
		},
		{
			name: "Handle nils properly",
			releaseConfig: &bumper.ReleaseConfig{
				Name:             "name*4.10",
				Message:          "message-4.10",
				MirrorPrefix:     "mirror-prefix_4.10",
				OverrideCLIImage: "cli-4.10",
				Check: map[string]bumper.ReleaseCheck{
					"check2": {
						ConsistentImages: nil,
					},
				},
				Verify: map[string]bumper.ReleaseVerification{
					"verify2": {
						ProwJob: nil,
					},
				},
				Publish: map[string]bumper.ReleasePublish{
					"pub2": {
						VerifyBugs:     nil,
						ImageStreamRef: nil,
						TagRef:         nil,
					},
				},
			},
			wantReleaseConfig: &bumper.ReleaseConfig{
				Name:             "name*4.11",
				Message:          "message-4.11",
				MirrorPrefix:     "mirror-prefix_4.11",
				OverrideCLIImage: "cli-4.11",
				Check: map[string]bumper.ReleaseCheck{
					"check2": {
						ConsistentImages: nil,
					},
				},
				Verify: map[string]bumper.ReleaseVerification{
					"verify2": {
						ProwJob: nil,
					},
				},
				Publish: map[string]bumper.ReleasePublish{
					"pub2": {
						VerifyBugs:     nil,
						ImageStreamRef: nil,
						TagRef:         nil,
					},
				},
			},
			ocpRelease: "4.10",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			b, err := bumper.NewReleaseControllerConfigBumper(test.ocpRelease, "")
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			result, err := b.BumpContent(test.releaseConfig)
			if err != nil {
				t.Error(err)
			}
			diff := cmp.Diff(result, test.wantReleaseConfig)
			if diff != "" {
				t.Errorf("Unexpected changes '%s'", diff)
			}
		})
	}
}
