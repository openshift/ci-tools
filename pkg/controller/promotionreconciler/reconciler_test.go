package promotionreconciler

import (
	"io/ioutil"
	"testing"

	"sigs.k8s.io/yaml"

	imagev1 "github.com/openshift/api/image/v1"
)

func TestRefForIST(t *testing.T) {
	testCases := []struct {
		name           string
		srcFile        string
		expectedOrg    string
		expectedRepo   string
		expectedBranch string
		expectedCommit string
	}{
		{
			name:           "normal",
			srcFile:        "testdata/imagestreamtag.yaml",
			expectedOrg:    "openshift",
			expectedRepo:   "cluster-openshift-apiserver-operator",
			expectedBranch: "master",
			expectedCommit: "96d6c74347445e0687267165a1a7d8f2c98dd3a1",
		},
		{
			name:           "source location has .git suffix",
			srcFile:        "testdata/ist_with_git_suffix.yaml",
			expectedOrg:    "openshift",
			expectedRepo:   "release",
			expectedBranch: "master",
			expectedCommit: "71e03eafe37b34af3768c8fcae077885d29e16f7",
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rawImageStreamTag, err := ioutil.ReadFile(tc.srcFile)
			if err != nil {
				t.Fatalf("failed to read imagestreamtag fixture: %v", err)
			}
			ist := &imagev1.ImageStreamTag{}
			if err := yaml.Unmarshal(rawImageStreamTag, ist); err != nil {
				t.Fatalf("failed to unmarshal imagestreamTag: %v", err)
			}
			ref, err := refForIST(ist)
			if err != nil {
				t.Fatalf("failed to get ref for ist: %v", err)
			}
			if ref.org != tc.expectedOrg {
				t.Errorf("expected org to be %s , was %q", tc.expectedOrg, ref.org)
			}
			if ref.repo != tc.expectedRepo {
				t.Errorf("expected repo to be %s , was %q", tc.expectedRepo, ref.repo)
			}
			if ref.branch != tc.expectedBranch {
				t.Errorf("expected branch to be %s , was %q", tc.expectedBranch, ref.branch)
			}
			if ref.commit != tc.expectedCommit {
				t.Errorf("expected commit to be %s , was %q", tc.expectedCommit, ref.commit)
			}
		})
	}
}
