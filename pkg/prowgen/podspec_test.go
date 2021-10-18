package prowgen

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/testhelper"
)

func TestNewCiOperatorPodSpecGenerator(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		mutators []PodSpecMutator
	}{
		{
			name: "defaults repo",
		},
		{
			name:     "no parameter is added when variant is empty",
			mutators: []PodSpecMutator{Variant("")},
		},
		{
			name:     "parameter is added for variant",
			mutators: []PodSpecMutator{Variant("variant")},
		},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewCiOperatorPodSpecGenerator()
			for i := range tc.mutators {
				g.Add(tc.mutators[i])
			}
			podspec, err := g.Build()
			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}
			testhelper.CompareWithFixture(t, podspec)
		})
	}
}

func TestGitHubToken(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name                  string
		reuseDecorationVolume bool
	}{
		{
			name:                  "podspec for private repo without reusing Prow's volume with credentials",
			reuseDecorationVolume: false,
		},
		{
			name:                  "podspec for private repo, reusing Prow's volume with credentials",
			reuseDecorationVolume: true,
		},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewCiOperatorPodSpecGenerator()
			g.Add(GitHubToken(tc.reuseDecorationVolume))
			podspec, err := g.Build()
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
		Org:    "org",
		Repo:   "repository",
		Branch: "branch",
	}
	tests := []struct {
		name          string
		generator     CiOperatorPodSpecGenerator
		expectedError error
	}{
		{
			name: "envvar additional envvar generated for template",
			generator: NewCiOperatorPodSpecGenerator().Add(
				Targets("tgt"),
				ClusterProfile(api.ClusterProfileAWS, "tgt"),
				Template("template", "kommand", "", "tgt", api.ClusterProfileAWS),
				ReleaseRpms("3.11", meta),
			),
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
			podspec, err := NewCiOperatorPodSpecGenerator().Add(Targets(tc.targets...)).Build()
			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}
			testhelper.CompareWithFixture(t, podspec)
		})
	}
}

func TestCIPullSecret(t *testing.T) {
	t.Parallel()
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
			g := NewCiOperatorPodSpecGenerator()
			g.Add(CIPullSecret())
			podspec, err := g.Build()
			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}
			testhelper.CompareWithFixture(t, podspec)
		})
	}
}

func TestClaims(t *testing.T) {
	t.Parallel()
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
			g := NewCiOperatorPodSpecGenerator()
			g.Add(Claims())
			podspec, err := g.Build()
			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}
			testhelper.CompareWithFixture(t, podspec)
		})
	}
}

func TestLeaseClient(t *testing.T) {
	t.Parallel()
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
			g := NewCiOperatorPodSpecGenerator()
			g.Add(LeaseClient())
			podspec, err := g.Build()
			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}
			testhelper.CompareWithFixture(t, podspec)
		})
	}
}

func TestSecrets(t *testing.T) {
	t.Parallel()
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
			g := NewCiOperatorPodSpecGenerator()
			g.Add(Secrets(tc.secrets...))
			podspec, err := g.Build()
			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}
			testhelper.CompareWithFixture(t, podspec)
		})
	}
}

func TestPromotion(t *testing.T) {
	t.Parallel()
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
			podspec, err := NewCiOperatorPodSpecGenerator().Add(Promotion()).Build()
			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}
			testhelper.CompareWithFixture(t, podspec)
		})
	}
}

func TestClusterProfile(t *testing.T) {
	t.Parallel()
	tests := []api.ClusterProfile{
		api.ClusterProfileAWS,
		api.ClusterProfileGCP,
		api.ClusterProfileOpenStack,
		api.ClusterProfileAWSCPaaS,
	}
	for _, tc := range tests {
		tc := tc
		t.Run(string(tc), func(t *testing.T) {
			t.Parallel()
			podspec, err := NewCiOperatorPodSpecGenerator().Add(ClusterProfile(tc, "test-name"), Targets("test-name")).Build()
			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}
			testhelper.CompareWithFixture(t, podspec)
		})
	}
}

func TestTemplate(t *testing.T) {
	t.Parallel()
	cp := api.ClusterProfileAWS
	tests := []struct {
		name          string
		g             CiOperatorPodSpecGenerator
		expectedError error
	}{
		{
			name: "template with command",
			g:    NewCiOperatorPodSpecGenerator().Add(ClusterProfile(cp, "t"), Template("cluster-launch-installer-upi-e2e", "make things", "", "t", cp), Targets("t")),
		},
		{
			name: "template with different command",
			g:    NewCiOperatorPodSpecGenerator().Add(ClusterProfile(cp, "t"), Template("cluster-launch-installer-upi-e2e", "make different things", "", "t", cp), Targets("t")),
		},
		{
			name: "different template with command",
			g:    NewCiOperatorPodSpecGenerator().Add(ClusterProfile(cp, "t"), Template("cluster-launch-installer-libvirt-e2e", "make things", "", "t", cp)),
		},
		{
			name: "template with a custom test image",
			g:    NewCiOperatorPodSpecGenerator().Add(ClusterProfile(cp, "t"), Template("cluster-launch-installer-upi-e2e", "make things", "custom-image", "t", cp), Targets("t")),
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
