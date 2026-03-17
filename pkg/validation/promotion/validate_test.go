package promotion

import (
	"os"
	"path/filepath"
	"testing"

	"k8s.io/apimachinery/pkg/util/sets"
	prowcfg "sigs.k8s.io/prow/pkg/config"

	cioperatorapi "github.com/openshift/ci-tools/pkg/api"
)

func TestIsMainOrMasterBranch(t *testing.T) {
	tests := []struct {
		branch string
		want   bool
	}{
		{"main", true},
		{"master", true},
		{"release-4.22", false},
		{"openshift-4.22", false},
		{"", false},
		{"other", false},
	}
	for _, tt := range tests {
		t.Run(tt.branch, func(t *testing.T) {
			if got := isMainOrMasterBranch(tt.branch); got != tt.want {
				t.Errorf("isMainOrMasterBranch(%q) = %v, want %v", tt.branch, got, tt.want)
			}
		})
	}
}

func TestIsReleaseBranch(t *testing.T) {
	tests := []struct {
		branch         string
		currentRelease string
		want           bool
	}{
		{"release-4.22", "4.22", true},
		{"openshift-4.22", "4.22", true},
		{"release-4.21", "4.22", false},
		{"openshift-4.21", "4.22", false},
		{"main", "4.22", false},
		{"master", "4.22", false},
	}
	for _, tt := range tests {
		name := tt.branch + "_" + tt.currentRelease
		t.Run(name, func(t *testing.T) {
			if got := isReleaseBranch(tt.branch, tt.currentRelease); got != tt.want {
				t.Errorf("isReleaseBranch(%q, %q) = %v, want %v", tt.branch, tt.currentRelease, got, tt.want)
			}
		})
	}
}

func TestIsPromotionFullyDisabled(t *testing.T) {
	tests := []struct {
		name string
		cfg  *cioperatorapi.ReleaseBuildConfiguration
		want bool
	}{
		{
			name: "nil promotion",
			cfg:  &cioperatorapi.ReleaseBuildConfiguration{PromotionConfiguration: nil},
			want: true,
		},
		{
			name: "empty targets",
			cfg:  &cioperatorapi.ReleaseBuildConfiguration{PromotionConfiguration: &cioperatorapi.PromotionConfiguration{Targets: nil}},
			want: true,
		},
		{
			name: "single target disabled",
			cfg: &cioperatorapi.ReleaseBuildConfiguration{
				PromotionConfiguration: &cioperatorapi.PromotionConfiguration{
					Targets: []cioperatorapi.PromotionTarget{{Name: "4.22", Disabled: true}},
				},
			},
			want: true,
		},
		{
			name: "single target enabled",
			cfg: &cioperatorapi.ReleaseBuildConfiguration{
				PromotionConfiguration: &cioperatorapi.PromotionConfiguration{
					Targets: []cioperatorapi.PromotionTarget{{Name: "4.22", Disabled: false}},
				},
			},
			want: false,
		},
		{
			name: "multiple targets all disabled",
			cfg: &cioperatorapi.ReleaseBuildConfiguration{
				PromotionConfiguration: &cioperatorapi.PromotionConfiguration{
					Targets: []cioperatorapi.PromotionTarget{
						{Name: "4.22", Disabled: true},
						{Name: "4.22-priv", Disabled: true},
					},
				},
			},
			want: true,
		},
		{
			name: "multiple targets one enabled",
			cfg: &cioperatorapi.ReleaseBuildConfiguration{
				PromotionConfiguration: &cioperatorapi.PromotionConfiguration{
					Targets: []cioperatorapi.PromotionTarget{
						{Name: "4.22", Disabled: true},
						{Name: "4.22-priv", Disabled: false},
					},
				},
			},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isPromotionFullyDisabled(tt.cfg); got != tt.want {
				t.Errorf("isPromotionFullyDisabled() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseCurrentReleaseFromInfraPeriodicsData(t *testing.T) {
	got, err := parseCurrentReleaseFromInfraPeriodicsData([]byte(`
periodics:
  - name: periodic-prow-auto-config-brancher
    spec:
      containers:
        - args:
            - --current-release=4.22
`))
	if err != nil {
		t.Fatalf("parseCurrentReleaseFromInfraPeriodicsData: %v", err)
	}
	if got != "4.22" {
		t.Errorf("got %q, want 4.22", got)
	}
	_, err = parseCurrentReleaseFromInfraPeriodicsData([]byte("periodics: []"))
	if err == nil {
		t.Error("expected error")
	}
}

func TestGetCurrentReleaseFromInfraPeriodics(t *testing.T) {
	dir := t.TempDir()
	validPath := filepath.Join(dir, "infra-periodics.yaml")
	validContent := `
periodics:
  - name: periodic-prow-auto-config-brancher
    spec:
      containers:
        - args:
            - --current-release=4.22
            - --config-dir=/config
`
	if err := os.WriteFile(validPath, []byte(validContent), 0644); err != nil {
		t.Fatal(err)
	}
	got, err := getCurrentReleaseFromInfraPeriodics(validPath)
	if err != nil {
		t.Fatalf("getCurrentReleaseFromInfraPeriodics() error = %v", err)
	}
	if got != "4.22" {
		t.Errorf("getCurrentReleaseFromInfraPeriodics() = %q, want 4.22", got)
	}

	_, err = getCurrentReleaseFromInfraPeriodics(filepath.Join(dir, "missing.yaml"))
	if err == nil {
		t.Error("expected error for missing file")
	}

	noJobPath := filepath.Join(dir, "no-job.yaml")
	if err := os.WriteFile(noJobPath, []byte("periodics: []"), 0644); err != nil {
		t.Fatal(err)
	}
	_, err = getCurrentReleaseFromInfraPeriodics(noJobPath)
	if err == nil {
		t.Error("expected error when job missing")
	}
}

func TestTideIncludesCurrentReleaseBranches(t *testing.T) {
	tide := &prowcfg.Tide{
		TideGitHubConfig: prowcfg.TideGitHubConfig{
			Queries: []prowcfg.TideQuery{
				{IncludedBranches: []string{"release-4.22", "openshift-4.22"}},
			},
		},
	}
	if !tideIncludesCurrentReleaseBranches(tide, "4.22") {
		t.Error("expected true")
	}
	if tideIncludesCurrentReleaseBranches(tide, "4.21") {
		t.Error("expected false for 4.21")
	}
}

func TestProwTideCache(t *testing.T) {
	dir := t.TempDir()
	org, repo := "openshift", "some-repo"
	base := filepath.Join(dir, org, repo)
	if err := os.MkdirAll(base, 0755); err != nil {
		t.Fatal(err)
	}
	prowPath := filepath.Join(base, "_prowconfig.yaml")
	content := `
tide:
  queries:
    - includedBranches:
        - release-4.22
        - openshift-4.22
`
	if err := os.WriteFile(prowPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	c := newProwTideCache(dir)
	ok, err := c.hasCurrentReleaseBranchInProw(org, repo, "4.22")
	if err != nil {
		t.Fatalf("hasCurrentReleaseBranchInProw: %v", err)
	}
	if !ok {
		t.Error("expected true")
	}
	ok, err = c.hasCurrentReleaseBranchInProw(org, repo, "4.21")
	if err != nil {
		t.Fatalf("hasCurrentReleaseBranchInProw: %v", err)
	}
	if ok {
		t.Error("expected false for 4.21")
	}
	ok, err = c.hasCurrentReleaseBranchInProw("other", "repo", "4.22")
	if err != nil {
		t.Fatalf("hasCurrentReleaseBranchInProw: %v", err)
	}
	if ok {
		t.Error("expected false for missing file")
	}
	_, err = c.hasCurrentReleaseBranchInProw(org, repo, "4.22")
	if err != nil {
		t.Errorf("second read should use cache: %v", err)
	}
}

func TestProwTideCache_UnmarshalError(t *testing.T) {
	dir := t.TempDir()
	org, repo := "openshift", "bad-yaml"
	base := filepath.Join(dir, org, repo)
	if err := os.MkdirAll(base, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "_prowconfig.yaml"), []byte("{invalid"), 0644); err != nil {
		t.Fatal(err)
	}
	c := newProwTideCache(dir)
	_, err := c.hasCurrentReleaseBranchInProw(org, repo, "4.22")
	if err == nil {
		t.Error("expected unmarshal error")
	}
}

func TestValidate_EmptyConfigDir(t *testing.T) {
	dir := t.TempDir()
	err := Validate(dir, "4.22", "", "", nil)
	if err != nil {
		t.Errorf("Validate(empty dir, currentRelease=4.22) = %v", err)
	}
}

func TestValidate_RequiresCurrentReleaseOrInfraPeriodics(t *testing.T) {
	dir := t.TempDir()
	err := Validate(dir, "", "", "", nil)
	if err == nil {
		t.Error("Validate() with no current-release and no infra-periodics expected error")
	}
}

func TestValidate_ResolvesCurrentReleaseFromInfraPeriodics(t *testing.T) {
	configDir := t.TempDir()
	infraDir := t.TempDir()
	infraPath := filepath.Join(infraDir, "infra.yaml")
	if err := os.WriteFile(infraPath, []byte(`
periodics:
  - name: periodic-prow-auto-config-brancher
    spec:
      containers:
        - args: ["--current-release=4.21"]
`), 0644); err != nil {
		t.Fatal(err)
	}
	err := Validate(configDir, "", "", infraPath, nil)
	if err != nil {
		t.Errorf("Validate() with infra-periodics only = %v", err)
	}
}

func TestValidate_IgnoreSet(t *testing.T) {
	if !DefaultIgnore.Has("openshift/gatekeeper") {
		t.Error("DefaultIgnore should contain openshift/gatekeeper")
	}
	dir := t.TempDir()
	customIgnore := sets.New[string]("custom/ignored-repo")
	err := Validate(dir, "4.22", "", "", customIgnore)
	if err != nil {
		t.Errorf("Validate(empty dir with custom ignore) = %v", err)
	}
}

func TestValidate_RejectsPromotionToDisallowedNamespace(t *testing.T) {
	dir := t.TempDir()
	repoDir := filepath.Join(dir, "foo", "bar")
	if err := os.MkdirAll(repoDir, 0755); err != nil {
		t.Fatal(err)
	}
	mainCfg := `build_root:
  image_stream_tag:
    name: release
    namespace: openshift
    tag: golang-1.10
resources:
  '*':
    limits:
      cpu: 500Mi
    requests:
      cpu: 10Mi
promotion:
  to:
    - name: "4.22"
      namespace: not-ocp
      disabled: false
tests:
- as: test
  commands: make test
  container:
    from: src
zz_generated_metadata:
  branch: main
  org: foo
  repo: bar
`
	path := filepath.Join(repoDir, "foo-bar-main.yaml")
	if err := os.WriteFile(path, []byte(mainCfg), 0644); err != nil {
		t.Fatal(err)
	}
	err := Validate(dir, "4.22", "", "", nil)
	if err == nil {
		t.Fatal("expected validation error for disallowed promotion namespace")
	}
}

func TestValidate_ErrorsOnMalformedProwConfigWhenEnforcingReleaseBranch(t *testing.T) {
	configDir := t.TempDir()
	prowDir := t.TempDir()
	repoDir := filepath.Join(configDir, "foo", "bar")
	if err := os.MkdirAll(repoDir, 0755); err != nil {
		t.Fatal(err)
	}
	prowRepoDir := filepath.Join(prowDir, "foo", "bar")
	if err := os.MkdirAll(prowRepoDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(prowRepoDir, "_prowconfig.yaml"), []byte("tide: ["), 0644); err != nil {
		t.Fatal(err)
	}
	base := `build_root:
  image_stream_tag:
    name: release
    namespace: openshift
    tag: golang-1.10
resources:
  '*':
    limits:
      cpu: 500Mi
    requests:
      cpu: 10Mi
tests:
- as: test
  commands: make test
  container:
    from: src
`
	mainCfg := base + `zz_generated_metadata:
  branch: main
  org: foo
  repo: bar
`
	releaseCfg := base + `promotion:
  to:
    - name: "4.22"
      namespace: ocp
      disabled: false
zz_generated_metadata:
  branch: release-4.22
  org: foo
  repo: bar
`
	if err := os.WriteFile(filepath.Join(repoDir, "foo-bar-main.yaml"), []byte(mainCfg), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, "foo-bar-release-4.22.yaml"), []byte(releaseCfg), 0644); err != nil {
		t.Fatal(err)
	}
	err := Validate(configDir, "4.22", prowDir, "", nil)
	if err == nil {
		t.Fatal("expected error from malformed prow config")
	}
}
