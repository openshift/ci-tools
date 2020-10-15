package mention

import (
	"testing"

	"github.com/openshift/ci-tools/pkg/testhelper"
)

func TestRespnseFor(t *testing.T) {
	var testCases = []struct {
		name    string
		message string
	}{
		{
			name:    "unrelated text gets a sad response",
			message: "who is the president?",
		},
		{
			name:    "one keyword gets a button block",
			message: "help me file a bug",
		},
		{
			name:    "many keywords get many button block",
			message: "help me file a bug and request a consultation",
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			actual := responseFor(testCase.message)
			testhelper.CompareWithFixture(t, actual)
		})
	}
}
