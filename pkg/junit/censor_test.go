package junit

import (
	"testing"

	"sigs.k8s.io/prow/pkg/secretutil"

	"github.com/openshift/ci-tools/pkg/testhelper"
)

func TestCensorTestSuite(t *testing.T) {
	censorer := secretutil.NewCensorer()
	censorer.Refresh("secret")
	input := TestSuite{
		Name: "some secret",
		Properties: []*TestSuiteProperty{
			{Name: "secret things", Value: "secret values"},
			{Name: "also secret things", Value: "really secret values"},
		},
		TestCases: []*TestCase{
			{
				Name:        "somehow secret",
				SkipMessage: &SkipMessage{Message: "skipped due to secret"},
				FailureOutput: &FailureOutput{
					Message: "failed due to secret",
					Output:  "secret failure output",
				},
				SystemOut: "output containing secret",
				SystemErr: "error containing secret",
			},
			{
				Name:        "somehow also secret",
				SkipMessage: &SkipMessage{Message: "also skipped due to secret"},
				FailureOutput: &FailureOutput{
					Message: "also failed due to secret",
					Output:  "also secret failure output",
				},
				SystemOut: "also output containing secret",
				SystemErr: "also error containing secret",
			},
		},
		Children: []*TestSuite{
			{
				Name: "some nested secret",
				Properties: []*TestSuiteProperty{
					{Name: "nested secret things", Value: "nested secret values"},
					{Name: "also nested secret things", Value: "really nested secret values"},
				},
				TestCases: []*TestCase{
					{
						Name:        "somehow nested secret",
						SkipMessage: &SkipMessage{Message: "skipped due to nested secret"},
						FailureOutput: &FailureOutput{
							Message: "failed due to nested secret",
							Output:  "nested secret failure output",
						},
						SystemOut: "output containing nested secret",
						SystemErr: "error containing nested secret",
					},
					{
						Name:        "somehow also nested secret",
						SkipMessage: &SkipMessage{Message: "also skipped due to nested secret"},
						FailureOutput: &FailureOutput{
							Message: "also failed due to nested secret",
							Output:  "also nested secret failure output",
						},
						SystemOut: "also output containing nested secret",
						SystemErr: "also error containing nested secret",
					},
				},
				Children: []*TestSuite{
					{
						Name: "some very nested secret",
						Properties: []*TestSuiteProperty{
							{Name: "very nested secret things", Value: "very nested secret values"},
							{Name: "also very nested secret things", Value: "really very nested secret values"},
						},
						TestCases: []*TestCase{
							{
								Name:        "somehow very nested secret",
								SkipMessage: &SkipMessage{Message: "skipped due to very nested secret"},
								FailureOutput: &FailureOutput{
									Message: "failed due to very nested secret",
									Output:  "very nested secret failure output",
								},
								SystemOut: "output containing very nested secret",
								SystemErr: "error containing very nested secret",
							},
							{
								Name:        "somehow also very nested secret",
								SkipMessage: &SkipMessage{Message: "also skipped due to very nested secret"},
								FailureOutput: &FailureOutput{
									Message: "also failed due to very nested secret",
									Output:  "also very nested secret failure output",
								},
								SystemOut: "also output containing very nested secret",
								SystemErr: "also error containing very nested secret",
							},
						},
						Children: nil,
					},
				},
			},
		},
	}
	CensorTestSuite(censorer, &input)
	testhelper.CompareWithFixture(t, input)
}
