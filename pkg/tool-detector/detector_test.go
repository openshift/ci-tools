package tooldetector

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"golang.org/x/tools/go/packages"

	"k8s.io/apimachinery/pkg/util/sets"
	prowapi "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	"sigs.k8s.io/prow/pkg/pod-utils/downwardapi"

	"github.com/openshift/ci-tools/pkg/api"
)

func TestNew(t *testing.T) {
	tests := []struct {
		name    string
		jobSpec *api.JobSpec
	}{
		{
			name: "jobSpec with BaseSHA",
			jobSpec: &api.JobSpec{
				JobSpec: downwardapi.JobSpec{
					Refs: &prowapi.Refs{
						BaseSHA: "jobspec-sha",
					},
				},
			},
		},
		{
			name:    "jobSpec without BaseSHA",
			jobSpec: &api.JobSpec{},
		},
		{
			name:    "nil jobSpec",
			jobSpec: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := New(tt.jobSpec)
			if d.jobSpec != tt.jobSpec {
				t.Errorf("Expected jobSpec %v, got %v", tt.jobSpec, d.jobSpec)
			}
		})
	}
}

func TestDetector_extractToolName(t *testing.T) {
	tests := []struct {
		name     string
		pkgPath  string
		expected string
	}{
		{
			name:     "simple tool",
			pkgPath:  "github.com/openshift/ci-tools/cmd/ci-operator",
			expected: "ci-operator",
		},
		{
			name:     "tool with subdirectory",
			pkgPath:  "github.com/openshift/ci-tools/cmd/branchingconfigmanagers/bugzilla-config-manager",
			expected: "branchingconfigmanagers",
		},
		{
			name:     "not a cmd package",
			pkgPath:  "github.com/openshift/ci-tools/pkg/api",
			expected: "",
		},
		{
			name:     "empty path",
			pkgPath:  "",
			expected: "",
		},
	}

	d := New(nil)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := d.extractToolName(tt.pkgPath)
			if result != tt.expected {
				t.Errorf("extractToolName(%q) = %q, want %q", tt.pkgPath, result, tt.expected)
			}
		})
	}
}

func TestDetector_hasDependency(t *testing.T) {
	createPkg := func(path string, imports []string) *packages.Package {
		pkg := &packages.Package{PkgPath: path}
		pkg.Imports = make(map[string]*packages.Package)
		for _, imp := range imports {
			pkg.Imports[imp] = &packages.Package{PkgPath: imp}
		}
		return pkg
	}

	tests := []struct {
		name        string
		pkg         *packages.Package
		targets     sets.Set[string]
		allPackages map[string]*packages.Package
		expected    bool
	}{
		{
			name:    "direct dependency",
			pkg:     createPkg("cmd/tool", []string{"pkg/api"}),
			targets: sets.New("pkg/api"),
			allPackages: map[string]*packages.Package{
				"cmd/tool": createPkg("cmd/tool", []string{"pkg/api"}),
				"pkg/api":  createPkg("pkg/api", nil),
			},
			expected: true,
		},
		{
			name:    "transitive dependency",
			pkg:     createPkg("cmd/tool", []string{"pkg/util"}),
			targets: sets.New("pkg/api"),
			allPackages: map[string]*packages.Package{
				"cmd/tool": createPkg("cmd/tool", []string{"pkg/util"}),
				"pkg/util": createPkg("pkg/util", []string{"pkg/api"}),
				"pkg/api":  createPkg("pkg/api", nil),
			},
			expected: true,
		},
		{
			name:    "no dependency",
			pkg:     createPkg("cmd/tool", []string{"pkg/util"}),
			targets: sets.New("pkg/api"),
			allPackages: map[string]*packages.Package{
				"cmd/tool": createPkg("cmd/tool", []string{"pkg/util"}),
				"pkg/util": createPkg("pkg/util", nil),
			},
			expected: false,
		},
		{
			name:    "self is target",
			pkg:     createPkg("pkg/api", nil),
			targets: sets.New("pkg/api"),
			allPackages: map[string]*packages.Package{
				"pkg/api": createPkg("pkg/api", nil),
			},
			expected: true,
		},
	}

	d := New(nil)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			visited := sets.New[string]()
			result := d.hasDependency(tt.pkg, tt.targets, tt.allPackages, visited)
			if result != tt.expected {
				t.Errorf("hasDependency() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestDetector_findAffectedToolsFromPackages(t *testing.T) {
	createPkg := func(path string, imports []string) *packages.Package {
		pkg := &packages.Package{PkgPath: path}
		pkg.Imports = make(map[string]*packages.Package)
		for _, imp := range imports {
			pkg.Imports[imp] = &packages.Package{PkgPath: imp}
		}
		return pkg
	}

	tests := []struct {
		name            string
		cmdTools        []*packages.Package
		allPackages     map[string]*packages.Package
		changedPackages sets.Set[string]
		expected        sets.Set[string]
	}{
		{
			name: "single change affects single tool",
			cmdTools: []*packages.Package{
				createPkg("github.com/openshift/ci-tools/cmd/tool1", []string{"github.com/openshift/ci-tools/pkg/api"}),
				createPkg("github.com/openshift/ci-tools/cmd/tool2", []string{"github.com/openshift/ci-tools/pkg/util"}),
			},
			allPackages: map[string]*packages.Package{
				"github.com/openshift/ci-tools/cmd/tool1": createPkg("github.com/openshift/ci-tools/cmd/tool1", []string{"github.com/openshift/ci-tools/pkg/api"}),
				"github.com/openshift/ci-tools/cmd/tool2": createPkg("github.com/openshift/ci-tools/cmd/tool2", []string{"github.com/openshift/ci-tools/pkg/util"}),
				"github.com/openshift/ci-tools/pkg/api":   createPkg("github.com/openshift/ci-tools/pkg/api", nil),
				"github.com/openshift/ci-tools/pkg/util":  createPkg("github.com/openshift/ci-tools/pkg/util", nil),
			},
			changedPackages: sets.New("github.com/openshift/ci-tools/pkg/api"),
			expected:        sets.New("tool1"),
		},
		{
			name: "single change affects multiple tools",
			cmdTools: []*packages.Package{
				createPkg("github.com/openshift/ci-tools/cmd/tool1", []string{"github.com/openshift/ci-tools/pkg/api"}),
				createPkg("github.com/openshift/ci-tools/cmd/tool2", []string{"github.com/openshift/ci-tools/pkg/api"}),
				createPkg("github.com/openshift/ci-tools/cmd/tool3", []string{"github.com/openshift/ci-tools/pkg/util"}),
			},
			allPackages: map[string]*packages.Package{
				"github.com/openshift/ci-tools/cmd/tool1": createPkg("github.com/openshift/ci-tools/cmd/tool1", []string{"github.com/openshift/ci-tools/pkg/api"}),
				"github.com/openshift/ci-tools/cmd/tool2": createPkg("github.com/openshift/ci-tools/cmd/tool2", []string{"github.com/openshift/ci-tools/pkg/api"}),
				"github.com/openshift/ci-tools/cmd/tool3": createPkg("github.com/openshift/ci-tools/cmd/tool3", []string{"github.com/openshift/ci-tools/pkg/util"}),
				"github.com/openshift/ci-tools/pkg/api":   createPkg("github.com/openshift/ci-tools/pkg/api", nil),
				"github.com/openshift/ci-tools/pkg/util":  createPkg("github.com/openshift/ci-tools/pkg/util", nil),
			},
			changedPackages: sets.New("github.com/openshift/ci-tools/pkg/api"),
			expected:        sets.New("tool1", "tool2"),
		},
		{
			name: "multiple changes affect multiple tools",
			cmdTools: []*packages.Package{
				createPkg("github.com/openshift/ci-tools/cmd/tool1", []string{"github.com/openshift/ci-tools/pkg/api"}),
				createPkg("github.com/openshift/ci-tools/cmd/tool2", []string{"github.com/openshift/ci-tools/pkg/util"}),
				createPkg("github.com/openshift/ci-tools/cmd/tool3", []string{"github.com/openshift/ci-tools/pkg/config"}),
			},
			allPackages: map[string]*packages.Package{
				"github.com/openshift/ci-tools/cmd/tool1":  createPkg("github.com/openshift/ci-tools/cmd/tool1", []string{"github.com/openshift/ci-tools/pkg/api"}),
				"github.com/openshift/ci-tools/cmd/tool2":  createPkg("github.com/openshift/ci-tools/cmd/tool2", []string{"github.com/openshift/ci-tools/pkg/util"}),
				"github.com/openshift/ci-tools/cmd/tool3":  createPkg("github.com/openshift/ci-tools/cmd/tool3", []string{"github.com/openshift/ci-tools/pkg/config"}),
				"github.com/openshift/ci-tools/pkg/api":    createPkg("github.com/openshift/ci-tools/pkg/api", nil),
				"github.com/openshift/ci-tools/pkg/util":   createPkg("github.com/openshift/ci-tools/pkg/util", nil),
				"github.com/openshift/ci-tools/pkg/config": createPkg("github.com/openshift/ci-tools/pkg/config", nil),
			},
			changedPackages: sets.New("github.com/openshift/ci-tools/pkg/api", "github.com/openshift/ci-tools/pkg/util"),
			expected:        sets.New("tool1", "tool2"),
		},
		{
			name: "multiple changes affect same tool",
			cmdTools: []*packages.Package{
				createPkg("github.com/openshift/ci-tools/cmd/tool1", []string{"github.com/openshift/ci-tools/pkg/api", "github.com/openshift/ci-tools/pkg/util"}),
				createPkg("github.com/openshift/ci-tools/cmd/tool2", []string{"github.com/openshift/ci-tools/pkg/config"}),
			},
			allPackages: map[string]*packages.Package{
				"github.com/openshift/ci-tools/cmd/tool1":  createPkg("github.com/openshift/ci-tools/cmd/tool1", []string{"github.com/openshift/ci-tools/pkg/api", "github.com/openshift/ci-tools/pkg/util"}),
				"github.com/openshift/ci-tools/cmd/tool2":  createPkg("github.com/openshift/ci-tools/cmd/tool2", []string{"github.com/openshift/ci-tools/pkg/config"}),
				"github.com/openshift/ci-tools/pkg/api":    createPkg("github.com/openshift/ci-tools/pkg/api", nil),
				"github.com/openshift/ci-tools/pkg/util":   createPkg("github.com/openshift/ci-tools/pkg/util", nil),
				"github.com/openshift/ci-tools/pkg/config": createPkg("github.com/openshift/ci-tools/pkg/config", nil),
			},
			changedPackages: sets.New("github.com/openshift/ci-tools/pkg/api", "github.com/openshift/ci-tools/pkg/util"),
			expected:        sets.New("tool1"),
		},
		{
			name: "transitive dependencies with multiple changes",
			cmdTools: []*packages.Package{
				createPkg("github.com/openshift/ci-tools/cmd/tool1", []string{"github.com/openshift/ci-tools/pkg/util"}),
				createPkg("github.com/openshift/ci-tools/cmd/tool2", []string{"github.com/openshift/ci-tools/pkg/config"}),
			},
			allPackages: map[string]*packages.Package{
				"github.com/openshift/ci-tools/cmd/tool1":  createPkg("github.com/openshift/ci-tools/cmd/tool1", []string{"github.com/openshift/ci-tools/pkg/util"}),
				"github.com/openshift/ci-tools/cmd/tool2":  createPkg("github.com/openshift/ci-tools/cmd/tool2", []string{"github.com/openshift/ci-tools/pkg/config"}),
				"github.com/openshift/ci-tools/pkg/util":   createPkg("github.com/openshift/ci-tools/pkg/util", []string{"github.com/openshift/ci-tools/pkg/api"}),
				"github.com/openshift/ci-tools/pkg/config": createPkg("github.com/openshift/ci-tools/pkg/config", []string{"github.com/openshift/ci-tools/pkg/api"}),
				"github.com/openshift/ci-tools/pkg/api":    createPkg("github.com/openshift/ci-tools/pkg/api", nil),
			},
			changedPackages: sets.New("github.com/openshift/ci-tools/pkg/api"),
			expected:        sets.New("tool1", "tool2"),
		},
		{
			name: "no tools affected by changes",
			cmdTools: []*packages.Package{
				createPkg("github.com/openshift/ci-tools/cmd/tool1", []string{"github.com/openshift/ci-tools/pkg/util"}),
				createPkg("github.com/openshift/ci-tools/cmd/tool2", []string{"github.com/openshift/ci-tools/pkg/config"}),
			},
			allPackages: map[string]*packages.Package{
				"github.com/openshift/ci-tools/cmd/tool1":  createPkg("github.com/openshift/ci-tools/cmd/tool1", []string{"github.com/openshift/ci-tools/pkg/util"}),
				"github.com/openshift/ci-tools/cmd/tool2":  createPkg("github.com/openshift/ci-tools/cmd/tool2", []string{"github.com/openshift/ci-tools/pkg/config"}),
				"github.com/openshift/ci-tools/pkg/util":   createPkg("github.com/openshift/ci-tools/pkg/util", nil),
				"github.com/openshift/ci-tools/pkg/config": createPkg("github.com/openshift/ci-tools/pkg/config", nil),
			},
			changedPackages: sets.New("github.com/openshift/ci-tools/pkg/api"),
			expected:        sets.New[string](),
		},
	}

	d := New(nil)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := d.findAffectedToolsFromPackages(tt.cmdTools, tt.allPackages, tt.changedPackages, "test-base-ref")
			if diff := cmp.Diff(tt.expected, result); diff != "" {
				t.Errorf("findAffectedToolsFromPackages() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestExtractBeforeSHAFromCompareURL(t *testing.T) {
	tests := []struct {
		name        string
		compareURL  string
		expectedSHA string
		expectError bool
	}{
		{
			name:        "valid compare URL with shortened SHA",
			compareURL:  "https://github.com/openshift/release/compare/38d0442581b2...422fb3179f57",
			expectedSHA: "38d0442581b2",
			expectError: false,
		},
		{
			name:        "valid compare URL with full SHA",
			compareURL:  "https://github.com/org/repo/compare/abc123def4567890123456789012345678901234...def456abc1237890123456789012345678901234",
			expectedSHA: "abc123def4567890123456789012345678901234",
			expectError: false,
		},
		{
			name:        "invalid URL - not a compare URL",
			compareURL:  "https://github.com/org/repo/commit/abc123",
			expectError: true,
		},
		{
			name:        "invalid URL - missing compare part",
			compareURL:  "https://github.com/org/repo/abc123...def456",
			expectError: true,
		},
		{
			name:        "invalid format - missing ...",
			compareURL:  "https://github.com/org/repo/compare/abc123",
			expectError: true,
		},
		{
			name:        "invalid format - multiple ...",
			compareURL:  "https://github.com/org/repo/compare/abc123...def456...ghi789",
			expectError: true,
		},
		{
			name:        "empty URL",
			compareURL:  "",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sha, err := extractBeforeSHAFromCompareURL(tt.compareURL)
			if tt.expectError {
				if err == nil {
					t.Errorf("expected error but got none")
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				if sha != tt.expectedSHA {
					t.Errorf("expected SHA %q, got %q", tt.expectedSHA, sha)
				}
			}
		})
	}
}
