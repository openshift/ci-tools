package tooldetector

import (
	"os"
	"path/filepath"
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
			d := New(tt.jobSpec, nil)
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
			pkgPath:  "github.com/openshift/ci-tools/cmd/branchingconfigmanagers/fast-forwarding-config-manager",
			expected: "fast-forwarding-config-manager",
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

	d := New(nil, nil)
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

	d := New(nil, nil)
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

	d := New(nil, nil)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := d.findAffectedToolsFromPackages(tt.cmdTools, tt.allPackages, tt.changedPackages, "test-base-ref")
			if diff := cmp.Diff(tt.expected, result); diff != "" {
				t.Errorf("findAffectedToolsFromPackages() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestDetector_getAffectedToolsByImageChanges(t *testing.T) {
	tests := []struct {
		name         string
		config       *api.ReleaseBuildConfiguration
		changedFiles []string
		expected     sets.Set[string]
	}{
		{
			name:         "no config",
			config:       nil,
			changedFiles: []string{"images/ci-operator/Dockerfile"},
			expected:     sets.New[string](),
		},
		{
			name: "single image with context_dir change",
			config: &api.ReleaseBuildConfiguration{
				Images: api.ImageConfiguration{Items: []api.ProjectDirectoryImageBuildStepConfiguration{
					{
						To: "ci-operator",
						ProjectDirectoryImageBuildInputs: api.ProjectDirectoryImageBuildInputs{
							ContextDir: "images/ci-operator",
						},
					},
				}},
			},
			changedFiles: []string{"images/ci-operator/Dockerfile"},
			expected:     sets.New("ci-operator"),
		},
		{
			name: "single image with context_dir change - trailing slash",
			config: &api.ReleaseBuildConfiguration{
				Images: api.ImageConfiguration{Items: []api.ProjectDirectoryImageBuildStepConfiguration{
					{
						To: "ci-operator",
						ProjectDirectoryImageBuildInputs: api.ProjectDirectoryImageBuildInputs{
							ContextDir: "images/ci-operator/",
						},
					},
				}},
			},
			changedFiles: []string{"images/ci-operator/Dockerfile"},
			expected:     sets.New("ci-operator"),
		},
		{
			name: "multiple files in same context_dir",
			config: &api.ReleaseBuildConfiguration{
				Images: api.ImageConfiguration{Items: []api.ProjectDirectoryImageBuildStepConfiguration{
					{
						To: "ci-operator",
						ProjectDirectoryImageBuildInputs: api.ProjectDirectoryImageBuildInputs{
							ContextDir: "images/ci-operator",
						},
					},
				}},
			},
			changedFiles: []string{
				"images/ci-operator/Dockerfile",
				"images/ci-operator/entrypoint.sh",
				"images/ci-operator/config.yaml",
			},
			expected: sets.New("ci-operator"),
		},
		{
			name: "multiple images with different context_dirs",
			config: &api.ReleaseBuildConfiguration{
				Images: api.ImageConfiguration{Items: []api.ProjectDirectoryImageBuildStepConfiguration{
					{
						To: "ci-operator",
						ProjectDirectoryImageBuildInputs: api.ProjectDirectoryImageBuildInputs{
							ContextDir: "images/ci-operator",
						},
					},
					{
						To: "pj-rehearse",
						ProjectDirectoryImageBuildInputs: api.ProjectDirectoryImageBuildInputs{
							ContextDir: "images/pj-rehearse",
						},
					},
				}},
			},
			changedFiles: []string{
				"images/ci-operator/Dockerfile",
				"images/pj-rehearse/Dockerfile",
			},
			expected: sets.New("ci-operator", "pj-rehearse"),
		},
		{
			name: "change in different directory",
			config: &api.ReleaseBuildConfiguration{
				Images: api.ImageConfiguration{Items: []api.ProjectDirectoryImageBuildStepConfiguration{
					{
						To: "ci-operator",
						ProjectDirectoryImageBuildInputs: api.ProjectDirectoryImageBuildInputs{
							ContextDir: "images/ci-operator",
						},
					},
				}},
			},
			changedFiles: []string{"images/other-tool/Dockerfile"},
			expected:     sets.New[string](),
		},
		{
			name: "mixed changes - some match, some don't",
			config: &api.ReleaseBuildConfiguration{
				Images: api.ImageConfiguration{Items: []api.ProjectDirectoryImageBuildStepConfiguration{
					{
						To: "ci-operator",
						ProjectDirectoryImageBuildInputs: api.ProjectDirectoryImageBuildInputs{
							ContextDir: "images/ci-operator",
						},
					},
					{
						To: "pj-rehearse",
						ProjectDirectoryImageBuildInputs: api.ProjectDirectoryImageBuildInputs{
							ContextDir: "images/pj-rehearse",
						},
					},
				}},
			},
			changedFiles: []string{
				"images/ci-operator/Dockerfile",
				"images/other-tool/Dockerfile",
				"pkg/api/types.go",
			},
			expected: sets.New("ci-operator"),
		},
		{
			name: "image with empty context_dir",
			config: &api.ReleaseBuildConfiguration{
				Images: api.ImageConfiguration{Items: []api.ProjectDirectoryImageBuildStepConfiguration{
					{
						To: "ci-operator",
						ProjectDirectoryImageBuildInputs: api.ProjectDirectoryImageBuildInputs{
							ContextDir: "",
						},
					},
				}},
			},
			changedFiles: []string{"images/ci-operator/Dockerfile"},
			expected:     sets.New[string](),
		},
		{
			name: "no changed files",
			config: &api.ReleaseBuildConfiguration{
				Images: api.ImageConfiguration{Items: []api.ProjectDirectoryImageBuildStepConfiguration{
					{
						To: "ci-operator",
						ProjectDirectoryImageBuildInputs: api.ProjectDirectoryImageBuildInputs{
							ContextDir: "images/ci-operator",
						},
					},
				}},
			},
			changedFiles: []string{},
			expected:     sets.New[string](),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := New(nil, tt.config)
			result := d.getAffectedToolsByImageChanges(tt.changedFiles)
			if diff := cmp.Diff(tt.expected, result); diff != "" {
				t.Errorf("getAffectedToolsByImageChanges() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestDetector_getAffectedToolsByBinaryInputs(t *testing.T) {
	makePaths := func(paths ...string) []api.ImageSourcePath {
		var result []api.ImageSourcePath
		for _, p := range paths {
			result = append(result, api.ImageSourcePath{SourcePath: p})
		}
		return result
	}

	tests := []struct {
		name            string
		config          *api.ReleaseBuildConfiguration
		alreadyAffected sets.Set[string]
		expected        sets.Set[string]
	}{
		{
			name:            "nil config returns empty",
			config:          nil,
			alreadyAffected: sets.New("private-prow-configs-mirror"),
			expected:        sets.New[string](),
		},
		{
			name: "no images with bin inputs",
			config: &api.ReleaseBuildConfiguration{
				Images: api.ImageConfiguration{
					Items: []api.ProjectDirectoryImageBuildStepConfiguration{
						{
							To: "some-image",
							ProjectDirectoryImageBuildInputs: api.ProjectDirectoryImageBuildInputs{
								ContextDir: "images/some-image",
							},
						},
					},
				},
			},
			alreadyAffected: sets.New("some-tool"),
			expected:        sets.New[string](),
		},
		{
			name: "bundle image affected when one of its binaries is affected",
			config: &api.ReleaseBuildConfiguration{
				Images: api.ImageConfiguration{
					Items: []api.ProjectDirectoryImageBuildStepConfiguration{
						{
							To: "auto-config-brancher",
							ProjectDirectoryImageBuildInputs: api.ProjectDirectoryImageBuildInputs{
								ContextDir: "images/auto-config-brancher",
								Inputs: map[string]api.ImageBuildInputs{
									"bin": {
										Paths: makePaths(
											"/go/bin/auto-config-brancher",
											"/go/bin/private-prow-configs-mirror",
											"/go/bin/config-brancher",
										),
									},
								},
							},
						},
					},
				},
			},
			alreadyAffected: sets.New("private-prow-configs-mirror"),
			expected:        sets.New("auto-config-brancher"),
		},
		{
			name: "bundle image not affected when none of its binaries are affected",
			config: &api.ReleaseBuildConfiguration{
				Images: api.ImageConfiguration{
					Items: []api.ProjectDirectoryImageBuildStepConfiguration{
						{
							To: "auto-config-brancher",
							ProjectDirectoryImageBuildInputs: api.ProjectDirectoryImageBuildInputs{
								ContextDir: "images/auto-config-brancher",
								Inputs: map[string]api.ImageBuildInputs{
									"bin": {
										Paths: makePaths(
											"/go/bin/auto-config-brancher",
											"/go/bin/private-prow-configs-mirror",
										),
									},
								},
							},
						},
					},
				},
			},
			alreadyAffected: sets.New("ci-operator"),
			expected:        sets.New[string](),
		},
		{
			name: "multiple bundle images, only matching one is affected",
			config: &api.ReleaseBuildConfiguration{
				Images: api.ImageConfiguration{
					Items: []api.ProjectDirectoryImageBuildStepConfiguration{
						{
							To: "auto-config-brancher",
							ProjectDirectoryImageBuildInputs: api.ProjectDirectoryImageBuildInputs{
								ContextDir: "images/auto-config-brancher",
								Inputs: map[string]api.ImageBuildInputs{
									"bin": {
										Paths: makePaths(
											"/go/bin/auto-config-brancher",
											"/go/bin/private-prow-configs-mirror",
										),
									},
								},
							},
						},
						{
							To: "prow-job-dispatcher",
							ProjectDirectoryImageBuildInputs: api.ProjectDirectoryImageBuildInputs{
								ContextDir: "images/prow-job-dispatcher",
								Inputs: map[string]api.ImageBuildInputs{
									"bin": {
										Paths: makePaths(
											"/go/bin/prow-job-dispatcher",
											"/go/bin/sanitize-prow-jobs",
										),
									},
								},
							},
						},
					},
				},
			},
			alreadyAffected: sets.New("private-prow-configs-mirror"),
			expected:        sets.New("auto-config-brancher"),
		},
		{
			name: "multiple bundle images both affected",
			config: &api.ReleaseBuildConfiguration{
				Images: api.ImageConfiguration{
					Items: []api.ProjectDirectoryImageBuildStepConfiguration{
						{
							To: "auto-config-brancher",
							ProjectDirectoryImageBuildInputs: api.ProjectDirectoryImageBuildInputs{
								ContextDir: "images/auto-config-brancher",
								Inputs: map[string]api.ImageBuildInputs{
									"bin": {
										Paths: makePaths(
											"/go/bin/auto-config-brancher",
											"/go/bin/sanitize-prow-jobs",
										),
									},
								},
							},
						},
						{
							To: "prow-job-dispatcher",
							ProjectDirectoryImageBuildInputs: api.ProjectDirectoryImageBuildInputs{
								ContextDir: "images/prow-job-dispatcher",
								Inputs: map[string]api.ImageBuildInputs{
									"bin": {
										Paths: makePaths(
											"/go/bin/prow-job-dispatcher",
											"/go/bin/sanitize-prow-jobs",
										),
									},
								},
							},
						},
					},
				},
			},
			alreadyAffected: sets.New("sanitize-prow-jobs"),
			expected:        sets.New("auto-config-brancher", "prow-job-dispatcher"),
		},
		{
			name: "already affected image not double-counted",
			config: &api.ReleaseBuildConfiguration{
				Images: api.ImageConfiguration{
					Items: []api.ProjectDirectoryImageBuildStepConfiguration{
						{
							To: "auto-config-brancher",
							ProjectDirectoryImageBuildInputs: api.ProjectDirectoryImageBuildInputs{
								ContextDir: "images/auto-config-brancher",
								Inputs: map[string]api.ImageBuildInputs{
									"bin": {
										Paths: makePaths(
											"/go/bin/auto-config-brancher",
											"/go/bin/private-prow-configs-mirror",
										),
									},
								},
							},
						},
					},
				},
			},
			alreadyAffected: sets.New("auto-config-brancher", "private-prow-configs-mirror"),
			expected:        sets.New("auto-config-brancher"),
		},
		{
			name: "image with no matching input key is skipped",
			config: &api.ReleaseBuildConfiguration{
				Images: api.ImageConfiguration{
					Items: []api.ProjectDirectoryImageBuildStepConfiguration{
						{
							To: "some-image",
							ProjectDirectoryImageBuildInputs: api.ProjectDirectoryImageBuildInputs{
								ContextDir: "images/some-image",
								Inputs: map[string]api.ImageBuildInputs{
									"src": {
										Paths: makePaths("/go/bin/some-tool"),
									},
								},
							},
						},
					},
				},
			},
			alreadyAffected: sets.New("some-tool"),
			expected:        sets.New[string](),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := New(nil, tt.config)
			result := d.getAffectedToolsByBinaryInputs(tt.alreadyAffected)
			if diff := cmp.Diff(tt.expected, result); diff != "" {
				t.Errorf("getAffectedToolsByBinaryInputs() mismatch (-want +got):\n%s", diff)
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

func TestParseForcedImagesFromCommitMessages(t *testing.T) {
	allowed := sets.New("pod-scaler", "pr-reminder", "pipeline-controller")

	tests := []struct {
		name           string
		messages       []string
		expectedForced sets.Set[string]
		expectedAll    bool
	}{
		{
			name: "single image directive",
			messages: []string{
				`commit title

/image pod-scaler`,
			},
			expectedForced: sets.New("pod-scaler"),
		},
		{
			name: "multiple image directive",
			messages: []string{
				`commit title

/image pod-scaler pr-reminder`,
			},
			expectedForced: sets.New("pod-scaler", "pr-reminder"),
		},
		{
			name: "all directive builds everything",
			messages: []string{
				`commit title

/image all`,
			},
			expectedForced: sets.New("pod-scaler", "pr-reminder", "pipeline-controller"),
			expectedAll:    true,
		},
		{
			name: "unknown images are ignored",
			messages: []string{
				`commit title

/image pod-scaler missing-image`,
			},
			expectedForced: sets.New("pod-scaler"),
		},
		{
			name: "must start at beginning of line",
			messages: []string{
				`commit title

some context /image pod-scaler
 /image pr-reminder`,
			},
			expectedForced: sets.New[string](),
		},
		{
			name: "supports only singular command",
			messages: []string{
				`commit title

/images pod-scaler`,
			},
			expectedForced: sets.New[string](),
		},
		{
			name: "collects directives across multiple commits",
			messages: []string{
				`commit one

/image pod-scaler`,
				`commit two

/image pr-reminder`,
			},
			expectedForced: sets.New("pod-scaler", "pr-reminder"),
		},
		{
			name: "/image with no arguments has no effect",
			messages: []string{
				`commit title

/image`,
			},
			expectedForced: sets.New[string](),
		},
		{
			name: "/image with trailing whitespace only has no effect",
			messages: []string{
				"commit title\n\n/image   ",
			},
			expectedForced: sets.New[string](),
		},
		{
			name:           "empty commit message",
			messages:       []string{""},
			expectedForced: sets.New[string](),
		},
		{
			name: "CRLF line endings",
			messages: []string{
				"commit title\r\n\r\n/image pod-scaler\r\n",
			},
			expectedForced: sets.New("pod-scaler"),
		},
		{
			name: "mixed valid and invalid on one line keeps valid ones",
			messages: []string{
				`commit title

/image pod-scaler bad-name pr-reminder also-bad`,
			},
			expectedForced: sets.New("pod-scaler", "pr-reminder"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			forced, all := parseForcedImagesFromCommitMessages(tt.messages, allowed)
			if diff := cmp.Diff(tt.expectedForced, forced); diff != "" {
				t.Fatalf("forced images mismatch (-want +got):\n%s", diff)
			}
			if all != tt.expectedAll {
				t.Fatalf("expected all=%v, got %v", tt.expectedAll, all)
			}
		})
	}
}

func TestDetector_loadChangedPackages(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Clean(filepath.Join(wd, "..", ".."))
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(wd) })

	tests := []struct {
		name    string
		files   []string
		want    sets.Set[string]
		wantErr bool
	}{
		{
			name: "skips build-tagged packages",
			files: []string{
				"cmd/pod-scaler/main.go",
				"test/e2e/pod-scaler/e2e_test.go",
			},
			want: sets.New("github.com/openshift/ci-tools/cmd/pod-scaler"),
		},
	}

	d := New(nil, nil)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := d.loadChangedPackages(tt.files)
			if (err != nil) != tt.wantErr {
				t.Fatalf("loadChangedPackages() error = %v, wantErr %v", err, tt.wantErr)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("loadChangedPackages() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
