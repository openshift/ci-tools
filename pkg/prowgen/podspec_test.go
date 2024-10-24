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

func TestReleaseLatest(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name          string
		generator     CiOperatorPodSpecGenerator
		expectedError error
	}{
		{
			name: "add release latest",
			generator: NewCiOperatorPodSpecGenerator().Add(
				Targets("tgt"),
				ReleaseLatest("quay.io/openshift-release-dev/ocp-release:4.15.12-x86_64"),
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

func TestReleaseInitial(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name          string
		generator     CiOperatorPodSpecGenerator
		expectedError error
	}{
		{
			name: "add release initial",
			generator: NewCiOperatorPodSpecGenerator().Add(
				Targets("tgt"),
				ReleaseInitial("quay.io/openshift-release-dev/ocp-release:4.15.12-x86_64"),
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

func TestTargetAdditionalSuffix(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		input string
	}{
		{
			name:  "target additional suffix is added",
			input: "1",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc := tc
			t.Parallel()
			g := NewCiOperatorPodSpecGenerator()
			g.Add(TargetAdditionalSuffix(tc.input))
			podspec, err := g.Build()
			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}
			testhelper.CompareWithFixture(t, podspec)
		})
	}
}

func TestCustomHashInput(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		inputs []string
	}{
		{
			name:   "custom hash input is added",
			inputs: []string{"one"},
		},
		{
			name:   "custom hash inputs are added",
			inputs: []string{"one", "two"},
		},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewCiOperatorPodSpecGenerator()
			for _, input := range tc.inputs {
				g.Add(CustomHashInput(input))
			}
			podspec, err := g.Build()
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
			g:    NewCiOperatorPodSpecGenerator().Add(Template("cluster-launch-installer-upi-e2e", "make things", "", "t", cp), Targets("t")),
		},
		{
			name: "template with different command",
			g:    NewCiOperatorPodSpecGenerator().Add(Template("cluster-launch-installer-upi-e2e", "make different things", "", "t", cp), Targets("t")),
		},
		{
			name: "different template with command",
			g:    NewCiOperatorPodSpecGenerator().Add(Template("cluster-launch-installer-libvirt-e2e", "make things", "", "t", cp)),
		},
		{
			name: "template with a custom test image",
			g:    NewCiOperatorPodSpecGenerator().Add(Template("cluster-launch-installer-upi-e2e", "make things", "custom-image", "t", cp), Targets("t")),
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

func TestInjectTestFrom(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		source      *api.MetadataWithTest
		expectedErr error
	}{
		{
			name: "inject coordinates without variant",
			source: &api.MetadataWithTest{
				Metadata: api.Metadata{
					Org:    "org-1",
					Repo:   "repo-1",
					Branch: "branch-1",
				},
				Test: "test-1",
			},
		},
		{
			name: "inject coordinates with variant",
			source: &api.MetadataWithTest{
				Metadata: api.Metadata{
					Org:     "org-2",
					Repo:    "repo-2",
					Branch:  "branch-2",
					Variant: "variant-2",
				},
				Test: "test-2",
			},
		},
		{
			name: "error on missing org",
			source: &api.MetadataWithTest{
				Metadata: api.Metadata{
					Repo:   "repo-1",
					Branch: "branch-1",
				},
				Test: "test-1",
			},
			expectedErr: errors.New("organization cannot be empty in injected test specification"),
		},
		{
			name: "error on missing repo",
			source: &api.MetadataWithTest{
				Metadata: api.Metadata{
					Org:    "org-1",
					Branch: "branch-1",
				},
				Test: "test-1",
			},
			expectedErr: errors.New("repository cannot be empty in injected test specification"),
		},
		{
			name: "error on missing branch",
			source: &api.MetadataWithTest{
				Metadata: api.Metadata{
					Org:  "org-1",
					Repo: "repo-1",
				},
				Test: "test-1",
			},
			expectedErr: errors.New("branch cannot be empty in injected test specification"),
		},
		{
			name: "error on missing test",
			source: &api.MetadataWithTest{
				Metadata: api.Metadata{
					Org:    "org-1",
					Repo:   "repo-1 ",
					Branch: "branch-1",
				},
			},
			expectedErr: errors.New("test cannot be empty in injected test specification"),
		},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			podspec, err := NewCiOperatorPodSpecGenerator().Add(InjectTestFrom(tc.source)).Build()
			if diff := cmp.Diff(tc.expectedErr, err, testhelper.EquateErrorMessage); diff != "" {
				t.Errorf("Error differs form expected:\n%s", diff)
			}
			if tc.expectedErr == nil {
				testhelper.CompareWithFixture(t, podspec)
			}
		})
	}
}
