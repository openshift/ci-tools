package tooldetector

import (
	"bufio"
	"fmt"
	"net/url"
	"os/exec"
	"strings"

	"golang.org/x/tools/go/packages"

	"k8s.io/apimachinery/pkg/util/sets"
	prowapi "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"

	"github.com/openshift/ci-tools/pkg/api"
)

const (
	ModulePrefix = "github.com/openshift/ci-tools"
	CmdPrefix    = ModulePrefix + "/cmd"
)

// Detector detects which cmd tools are affected by code changes
type Detector struct {
	jobSpec *api.JobSpec
	config  *api.ReleaseBuildConfiguration
}

// New creates a new detector.
func New(jobSpec *api.JobSpec, config *api.ReleaseBuildConfiguration) *Detector {
	return &Detector{jobSpec: jobSpec, config: config}
}

// AffectedTools returns the set of cmd tool names that are affected by changes
func (d *Detector) AffectedTools() (sets.Set[string], error) {
	if d.jobSpec == nil || d.jobSpec.Refs == nil || d.jobSpec.Refs.BaseSHA == "" {
		return nil, fmt.Errorf("jobSpec.Refs.BaseSHA is required but not available")
	}
	baseRef := d.jobSpec.Refs.BaseSHA

	// For postsubmits, BaseSHA is the commit that was just pushed (HEAD). We need to compare
	// against where the branch was before the push. The BaseLink contains a GitHub compare URL
	// in the format: https://github.com/org/repo/compare/beforeSHA...afterSHA, so we can extract
	// the "before" SHA from it.
	if d.jobSpec.Type == prowapi.PostsubmitJob {
		if d.jobSpec.Refs.BaseLink == "" {
			return nil, fmt.Errorf("BaseLink is required for postsubmit jobs but is missing")
		}
		beforeSHA, err := extractBeforeSHAFromCompareURL(d.jobSpec.Refs.BaseLink)
		if err != nil {
			return nil, fmt.Errorf("failed to extract before SHA from BaseLink: %w", err)
		}
		baseRef = beforeSHA
	}
	changedFiles, err := d.getChangedFiles(baseRef)
	if err != nil {
		return nil, fmt.Errorf("get changed files: %w", err)
	}

	if len(changedFiles) == 0 {
		return sets.New[string](), nil
	}

	affectedByImageChanges := d.getAffectedToolsByImageChanges(changedFiles)

	goFiles := []string{}
	for _, file := range changedFiles {
		if strings.HasSuffix(file, ".go") {
			goFiles = append(goFiles, file)
		}
	}

	if len(goFiles) == 0 {
		return affectedByImageChanges, nil
	}

	changedPackages, err := d.loadChangedPackages(goFiles)
	if err != nil {
		return nil, fmt.Errorf("load changed packages: %w", err)
	}

	if len(changedPackages) == 0 {
		return affectedByImageChanges, nil
	}

	cmdTools, allPackages, err := d.loadCmdTools()
	if err != nil {
		return nil, fmt.Errorf("load cmd tools: %w", err)
	}

	affectedByPackages := d.findAffectedToolsFromPackages(cmdTools, allPackages, changedPackages, baseRef)
	return affectedByPackages.Union(affectedByImageChanges), nil
}

func (d *Detector) getChangedFiles(baseRef string) ([]string, error) {
	cmd := exec.Command("git", "diff", "--name-only", "--diff-filter=ACMR", baseRef+"...HEAD")
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git diff %s...HEAD: %w", baseRef, err)
	}

	var files []string
	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			files = append(files, line)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scanning git diff output: %w", err)
	}

	return files, nil
}

func (d *Detector) loadChangedPackages(files []string) (sets.Set[string], error) {
	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedFiles,
	}

	patterns := make([]string, 0, len(files))
	for _, file := range files {
		patterns = append(patterns, "file="+file)
	}

	pkgs, err := packages.Load(cfg, patterns...)
	if err != nil {
		return nil, err
	}

	changed := sets.New[string]()
	for _, pkg := range pkgs {
		if pkg.PkgPath != "" && strings.HasPrefix(pkg.PkgPath, ModulePrefix) {
			changed.Insert(pkg.PkgPath)
		}
	}

	return changed, nil
}

func (d *Detector) loadCmdTools() ([]*packages.Package, map[string]*packages.Package, error) {
	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedImports | packages.NeedDeps,
	}

	pkgs, err := packages.Load(cfg, "./cmd/...")
	if err != nil {
		return nil, nil, err
	}

	allPackages := make(map[string]*packages.Package)
	for _, pkg := range pkgs {
		allPackages[pkg.PkgPath] = pkg
	}

	var cmdTools []*packages.Package
	for _, pkg := range pkgs {
		if strings.HasPrefix(pkg.PkgPath, CmdPrefix) {
			cmdTools = append(cmdTools, pkg)
		}
	}

	return cmdTools, allPackages, nil
}

func (d *Detector) findAffectedToolsFromPackages(cmdTools []*packages.Package, allPackages map[string]*packages.Package, changedPackages sets.Set[string], baseRef string) sets.Set[string] {
	affected := sets.New[string]()

	// If the base ref has changes to go.mod or go.sum, we need to build all tools.
	// We could potentially detect the affected tools, but for now we just build all tools when
	// the base ref has changes to go.* or the vendor directory.
	if d.hasModuleDependencyChanges(baseRef) {
		for _, cmdPkg := range cmdTools {
			if toolName := d.extractToolName(cmdPkg.PkgPath); toolName != "" {
				affected.Insert(toolName)
			}
		}
		return affected
	}

	for _, cmdPkg := range cmdTools {
		visited := sets.New[string]()
		if d.hasDependency(cmdPkg, changedPackages, allPackages, visited) {
			if toolName := d.extractToolName(cmdPkg.PkgPath); toolName != "" {
				affected.Insert(toolName)
			}
		}
	}

	return affected
}

func (d *Detector) hasDependency(pkg *packages.Package, targets sets.Set[string], allPackages map[string]*packages.Package, visited sets.Set[string]) bool {
	if visited.Has(pkg.PkgPath) {
		return false
	}
	visited.Insert(pkg.PkgPath)

	if targets.Has(pkg.PkgPath) {
		return true
	}

	for _, imp := range pkg.Imports {
		if impPkg, ok := allPackages[imp.PkgPath]; ok {
			if d.hasDependency(impPkg, targets, allPackages, visited) {
				return true
			}
		} else if d.hasDependency(imp, targets, allPackages, visited) {
			return true
		}
	}

	return false
}

func (d *Detector) extractToolName(pkgPath string) string {
	if !strings.HasPrefix(pkgPath, CmdPrefix) {
		return ""
	}

	parts := strings.Split(strings.TrimPrefix(pkgPath, CmdPrefix+"/"), "/")
	if len(parts) == 0 {
		return ""
	}

	return parts[0]
}

// extractBeforeSHAFromCompareURL extracts the "before" SHA from a GitHub compare URL.
// The URL format is: https://github.com/org/repo/compare/beforeSHA...afterSHA
func extractBeforeSHAFromCompareURL(compareURL string) (string, error) {
	parsedURL, err := url.Parse(compareURL)
	if err != nil {
		return "", fmt.Errorf("failed to parse compare URL: %w", err)
	}
	pathParts := strings.Split(parsedURL.Path, "/")
	if len(pathParts) < 4 || pathParts[len(pathParts)-2] != "compare" {
		return "", fmt.Errorf("invalid compare URL format, expected /org/repo/compare/before...after, got: %s", parsedURL.Path)
	}
	comparePart := pathParts[len(pathParts)-1]
	parts := strings.Split(comparePart, "...")
	if len(parts) != 2 {
		return "", fmt.Errorf("invalid compare format, expected beforeSHA...afterSHA, got: %s", comparePart)
	}
	return parts[0], nil
}

func (d *Detector) hasModuleDependencyChanges(baseRef string) bool {
	cmd := exec.Command("git", "diff", "--name-only", baseRef+"...HEAD")
	output, err := cmd.Output()
	if err != nil {
		return false
	}

	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "go.mod" || line == "go.sum" || strings.HasPrefix(line, "vendor/") {
			return true
		}
	}
	return false
}

// getAffectedToolsByImageChanges checks if any files in image context directories changed
// and returns the set of tools affected by those changes
func (d *Detector) getAffectedToolsByImageChanges(changedFiles []string) sets.Set[string] {
	affected := sets.New[string]()

	if d.config == nil {
		return affected
	}

	for _, image := range d.config.Images {
		if image.ContextDir == "" {
			continue
		}

		contextDir := image.ContextDir
		if !strings.HasSuffix(contextDir, "/") {
			contextDir += "/"
		}

		for _, changedFile := range changedFiles {
			if strings.HasPrefix(changedFile, contextDir) {
				affected.Insert(string(image.To))
				break
			}
		}
	}

	return affected
}
