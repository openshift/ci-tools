package repo_test

import (
	"strings"
	"testing"

	"gopkg.in/ini.v1"

	"github.com/openshift/ci-tools/pkg/branchcuts/bumper/repo"
)

func TestBumpRepo(t *testing.T) {
	tests := []struct {
		id            string
		content       string
		wantContent   string
		curOCPRelease string
	}{
		{
			"Bump properly",
			`[rhel-repo-1]
name = test
baseurl = https://fake-repo.com/ocp/4.10/test
`,
			`[rhel-repo-1]
name = test
baseurl = https://fake-repo.com/ocp/4.11/test

`,
			"4.10",
		},
		{
			"Bump dash",
			`[rhel-repo-1]
name = test
baseurl = https://fake-repo.com/ocp/4-10/test
`,
			`[rhel-repo-1]
name = test
baseurl = https://fake-repo.com/ocp/4-11/test

`,
			"4.10",
		},
	}

	ini.PrettySection = true
	ini.PrettyFormat = false
	ini.PrettyEqual = true

	for _, test := range tests {
		test := test
		t.Run(test.id, func(t *testing.T) {
			t.Parallel()
			b, err := repo.NewRepoBumper(&repo.RepoBumperOptions{
				CurOCPRelease: test.curOCPRelease,
			})
			if err != nil {
				t.Error(err)
			}
			file, err := ini.Load([]byte(test.content))
			if err != nil {
				t.Error(err)
			}

			result, err := b.BumpContent(file)
			if err != nil {
				t.Error(err)
			} else {
				buf := strings.Builder{}
				if _, err := result.WriteTo(&buf); err != nil {
					t.Error(err)
				}
				resultContent := buf.String()
				if test.wantContent != resultContent {
					t.Errorf("Expected '%s' but got '%s'\n", test.wantContent, resultContent)
				}
			}
		})
	}
}
