package prowgen

import (
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/testhelper"
)

func TestNewCiOperatorPodSpecGenerator(t *testing.T) {
	t.Parallel()
	meta := api.Metadata{
		Org:    "org",
		Repo:   "repository",
		Branch: "branch",
	}
	tests := []struct {
		name    string
		variant string
	}{
		{
			name: "podspec for public repo",
		},
		{
			name:    "parameter is added for variant",
			variant: "variant",
		},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tcMeta := meta
			tcMeta.Variant = tc.variant
			podspec, err := NewCiOperatorPodSpecGenerator(tcMeta).Build()
			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}
			testhelper.CompareWithFixture(t, podspec)
		})
	}
}

func TestGitHubToken(t *testing.T) {
	t.Parallel()
	meta := api.Metadata{
		Org:    "org",
		Repo:   "repository",
		Branch: "branch",
	}
	tests := []struct {
		name string
		g    CiOperatorPodSpecGenerator
	}{
		{
			name: "podspec for private repo without reusing Prow's volume with credentials",
			g:    NewCiOperatorPodSpecGenerator(meta).GitHubToken(false),
		},
		{
			name: "podspec for private repo, reusing Prow's volume with credentials",
			g:    NewCiOperatorPodSpecGenerator(meta).GitHubToken(true),
		},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			podspec, err := tc.g.Build()
			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}
			testhelper.CompareWithFixture(t, podspec)
		})
	}
}

func TestReleaseRpms(t *testing.T) {
	t.Parallel()
	meta := api.Metadata{
		Org:     "org",
		Repo:    "repository",
		Branch:  "branch",
		Variant: "variant",
	}
	tests := []struct {
		name          string
		generator     CiOperatorPodSpecGenerator
		expectedError error
	}{
		{
			name:      "envvar additional envvar generated for template",
			generator: NewCiOperatorPodSpecGenerator(meta).Targets("tgt").ClusterProfile(api.ClusterProfileAWS).Template("template", "kommand", "").ReleaseRpms("3.11"),
		},
		{
			name:          "release RPM do not make sense without a template",
			generator:     NewCiOperatorPodSpecGenerator(meta).Targets("tgt").ReleaseRpms("3.11"),
			expectedError: errors.New("empty template but nonempty release RPM version"),
		},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			podspec, err := tc.generator.Build()
			if diff := cmp.Diff(tc.expectedError, err, testhelper.EquateErrorMessage); diff != "" {
				t.Errorf("Error differs from expected:\n%s", diff)
			}
			if tc.expectedError == nil {
				testhelper.CompareWithFixture(t, podspec)
			}
		})
	}
}

func TestTargets(t *testing.T) {
	t.Parallel()
	meta := api.Metadata{
		Org:    "org",
		Repo:   "repository",
		Branch: "branch",
	}
	tests := []struct {
		name    string
		targets []string
	}{
		{
			name:    "single target",
			targets: []string{"t1"},
		},
		{
			name:    "multiple targets",
			targets: []string{"t2", "t1"},
		},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			podspec, err := NewCiOperatorPodSpecGenerator(meta).Targets(tc.targets...).Build()
			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}
			testhelper.CompareWithFixture(t, podspec)
		})
	}
}

func TestCIPullSecret(t *testing.T) {
	t.Parallel()
	meta := api.Metadata{
		Org:     "org",
		Repo:    "repository",
		Branch:  "branch",
		Variant: "variant",
	}
	tests := []struct {
		name string
	}{
		{
			name: "secret is added",
		},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			podspec, err := NewCiOperatorPodSpecGenerator(meta).CIPullSecret().Build()
			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}
			testhelper.CompareWithFixture(t, podspec)
		})
	}
}

func TestClaims(t *testing.T) {
	t.Parallel()
	meta := api.Metadata{
		Org:     "org",
		Repo:    "repository",
		Branch:  "branch",
		Variant: "variant",
	}
	tests := []struct {
		name string
	}{
		{
			name: "secret is added",
		},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			podspec, err := NewCiOperatorPodSpecGenerator(meta).Claims().Build()
			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}
			testhelper.CompareWithFixture(t, podspec)
		})
	}
}

func TestLeaseClient(t *testing.T) {
	t.Parallel()
	meta := api.Metadata{
		Org:     "org",
		Repo:    "repository",
		Branch:  "branch",
		Variant: "variant",
	}
	tests := []struct {
		name string
	}{
		{
			name: "secret is added",
		},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			podspec, err := NewCiOperatorPodSpecGenerator(meta).LeaseClient().Build()
			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}
			testhelper.CompareWithFixture(t, podspec)
		})
	}
}

func TestSecrets(t *testing.T) {
	t.Parallel()
	meta := api.Metadata{
		Org:     "org",
		Repo:    "repository",
		Branch:  "branch",
		Variant: "variant",
	}
	tests := []struct {
		name    string
		secrets []*api.Secret
	}{
		{
			name: "empty list is a nop",
		},
		{
			name:    "one secret",
			secrets: []*api.Secret{{Name: "sekret"}},
		},
		{
			name:    "multiple secrets",
			secrets: []*api.Secret{{Name: "sekret"}, {Name: "another", MountPath: "/with/path"}},
		},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			podspec, err := NewCiOperatorPodSpecGenerator(meta).Secrets(tc.secrets...).Build()
			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}
			testhelper.CompareWithFixture(t, podspec)
		})
	}
}

func TestPromotion(t *testing.T) {
	t.Parallel()
	meta := api.Metadata{
		Org:     "org",
		Repo:    "repository",
		Branch:  "branch",
		Variant: "variant",
	}
	tests := []struct {
		name string
	}{
		{
			name: "secret and parameters are added",
		},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			podspec, err := NewCiOperatorPodSpecGenerator(meta).Promotion().Build()
			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}
			testhelper.CompareWithFixture(t, podspec)
		})
	}
}

func TestClusterProfile(t *testing.T) {
	t.Parallel()
	meta := api.Metadata{
		Org:    "org",
		Repo:   "repository",
		Branch: "branch",
	}
	tests := api.ClusterProfiles()
	for _, tc := range tests {
		tc := tc
		t.Run(string(tc), func(t *testing.T) {
			t.Parallel()
			podspec, err := NewCiOperatorPodSpecGenerator(meta).ClusterProfile(tc).Targets("test-name").Build()
			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}
			testhelper.CompareWithFixture(t, podspec)
		})
	}

	errorTests := []struct {
		name          string
		g             CiOperatorPodSpecGenerator
		expectedError error
	}{
		{
			name:          "no targets",
			g:             NewCiOperatorPodSpecGenerator(meta).ClusterProfile(api.ClusterProfileAWS),
			expectedError: errors.New("ci-operator must have exactly one target when using a cluster profile"),
		},
		{
			name:          "multiple targets",
			g:             NewCiOperatorPodSpecGenerator(meta).ClusterProfile(api.ClusterProfileAWS).Targets("one", "two"),
			expectedError: errors.New("ci-operator must have exactly one target when using a cluster profile"),
		},
	}
	for _, tc := range errorTests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := tc.g.Build()
			if diff := cmp.Diff(tc.expectedError, err, testhelper.EquateErrorMessage); diff != "" {
				t.Errorf("Error differs from expected:\n%s", diff)
			}
		})
	}

}

func TestTemplate(t *testing.T) {
	t.Parallel()
	meta := api.Metadata{
		Org:    "org",
		Repo:   "repository",
		Branch: "branch",
	}
	cp := api.ClusterProfileAWS
	tests := []struct {
		name          string
		g             CiOperatorPodSpecGenerator
		expectedError error
	}{
		{
			name: "template with command",
			g:    NewCiOperatorPodSpecGenerator(meta).ClusterProfile(cp).Targets("t").Template("cluster-launch-installer-upi-e2e", "make things", ""),
		},
		{
			name: "template with different command",
			g:    NewCiOperatorPodSpecGenerator(meta).ClusterProfile(cp).Targets("t").Template("cluster-launch-installer-upi-e2e", "make different things", ""),
		},
		{
			name: "different template with command",
			g:    NewCiOperatorPodSpecGenerator(meta).ClusterProfile(cp).Targets("t").Template("cluster-launch-installer-libvirt-e2e", "make things", ""),
		},
		{
			name: "template with a custom test image",
			g:    NewCiOperatorPodSpecGenerator(meta).ClusterProfile(cp).Targets("t").Template("cluster-launch-installer-upi-e2e", "make things", "custom-image"),
		},
		{
			name:          "template without cluster profile -> error",
			g:             NewCiOperatorPodSpecGenerator(meta).Targets("t").Template("tmplt", "c", ""),
			expectedError: errors.New("template requires cluster profile"),
		},
		{
			name:          "template with multiple targets -> error",
			g:             NewCiOperatorPodSpecGenerator(meta).ClusterProfile(cp).Template("tmplt", "c", ""),
			expectedError: errors.New("[ci-operator must have exactly one target when using a cluster profile, ci-operator must have exactly one target when using a template]"),
		},
		{
			name:          "template with zero targets -> error",
			g:             NewCiOperatorPodSpecGenerator(meta).ClusterProfile(cp).Template("tmplt", "c", "").Targets("t1", "t2"),
			expectedError: errors.New("[ci-operator must have exactly one target when using a cluster profile, ci-operator must have exactly one target when using a template]"),
		},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			podspec, err := tc.g.Build()
			if diff := cmp.Diff(tc.expectedError, err, testhelper.EquateErrorMessage); diff != "" {
				t.Errorf("Error differs from expected:\n%s", diff)
			}
			if tc.expectedError == nil {
				testhelper.CompareWithFixture(t, podspec)
			}

		})
	}
}
