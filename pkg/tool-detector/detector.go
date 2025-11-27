package tooldetector

import (
	"bufio"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/sirupsen/logrus"
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
	// against where the branch was before the push:
	// - If BaseLink is set, it points to a compare URL we can parse.
	// - Otherwise, fall back to diffing parent(BaseSHA)...BaseSHA using git's ^ syntax.
	if d.jobSpec.Type == prowapi.PostsubmitJob {
		if d.jobSpec.Refs.BaseLink != "" {
			beforeSHA, err := extractBeforeSHAFromCompareURL(d.jobSpec.Refs.BaseLink)
			if err != nil {
				return nil, fmt.Errorf("failed to extract before SHA from BaseLink: %w", err)
			}
			baseRef = beforeSHA
		} else {
			baseRef += "^"
		}
	}
	changedFiles, err := d.getChangedFiles(baseRef)
	if err != nil {
		return nil, fmt.Errorf("get changed files: %w", err)
	}

	logrus.WithField("baseRef", baseRef).WithField("count", len(changedFiles)).WithField("files", changedFiles).Info("Detected changed files for tool detection")
	if len(changedFiles) == 0 {
		return nil, fmt.Errorf("no changed files detected between %s and HEAD", baseRef)
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

	logrus.WithField("count", len(changedPackages)).WithField("packages", sets.List(changedPackages)).Info("Detected changed packages")
	if len(changedPackages) == 0 {
		return affectedByImageChanges, nil
	}

	cmdTools, allPackages, err := d.loadCmdTools()
	if err != nil {
		return nil, fmt.Errorf("load cmd tools: %w", err)
	}

	affectedByPackages := d.findAffectedToolsFromPackages(cmdTools, allPackages, changedPackages, baseRef)

	logrus.WithField("affectedByPackages", sets.List(affectedByPackages)).
		WithField("affectedByImages", sets.List(affectedByImageChanges)).
		WithField("total", affectedByPackages.Union(affectedByImageChanges).Len()).
		Info("Detected affected tools")

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
	env := os.Environ()
	if os.Getenv("GOCACHE") == "" {
		env = append(env, "GOCACHE=/tmp/go-build-cache")
	}
	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedFiles,
		Env:  env,
	}

	pkgDirs := sets.New[string]()
	for _, file := range files {
		dir := filepath.Dir(file)
		if dir == "." || dir == "" {
			pkgDirs.Insert(".")
			continue
		}
		pkgDirs.Insert("./" + dir)
	}
	if pkgDirs.Len() == 0 {
		return sets.New[string](), nil
	}

	pkgs, err := packages.Load(cfg, sets.List(pkgDirs)...)
	if err != nil {
		return nil, err
	}

	var packageErrors []error
	for _, pkg := range pkgs {
		if len(pkg.Errors) > 0 {
			for _, pkgErr := range pkg.Errors {
				packageErrors = append(packageErrors, fmt.Errorf("package %s: %w", pkg.PkgPath, pkgErr))
			}
		}
	}
	if len(packageErrors) > 0 {
		return nil, fmt.Errorf("failed to load changed packages: %v", packageErrors)
	}

	changed := sets.New[string]()
	var emptyPkgPaths []string
	for _, pkg := range pkgs {
		if pkg.PkgPath == "" {
			emptyPkgPaths = append(emptyPkgPaths, fmt.Sprintf("files: %v", pkg.GoFiles))
		} else if strings.HasPrefix(pkg.PkgPath, ModulePrefix) {
			changed.Insert(pkg.PkgPath)
		}
	}

	// TODO: If no packages are changed let's return an error to trigger all builds for now. Keeping this as a temporary failsafe.
	// It will be ideal to remove this in the future to avoid building images for PRs that change non-go files.
	if len(files) > 0 && len(changed) == 0 {
		if len(emptyPkgPaths) > 0 {
			return nil, fmt.Errorf("go/packages returned packages with empty PkgPath for changed files %v: %v", files, emptyPkgPaths)
		}
		return nil, fmt.Errorf("go/packages returned 0 matching packages for changed files %v", files)
	}

	return changed, nil
}

func (d *Detector) loadCmdTools() ([]*packages.Package, map[string]*packages.Package, error) {
	env := os.Environ()
	if os.Getenv("GOCACHE") == "" {
		env = append(env, "GOCACHE=/tmp/go-build-cache")
	}
	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedImports | packages.NeedDeps,
		Env:  env,
	}

	pkgs, err := packages.Load(cfg, "./cmd/...")
	if err != nil {
		return nil, nil, err
	}

	var packageErrors []error
	for _, pkg := range pkgs {
		if len(pkg.Errors) > 0 {
			for _, pkgErr := range pkg.Errors {
				packageErrors = append(packageErrors, fmt.Errorf("package %s: %w", pkg.PkgPath, pkgErr))
			}
		}
	}
	if len(packageErrors) > 0 {
		return nil, nil, fmt.Errorf("failed to load cmd tools: %v", packageErrors)
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
				if path := dependencyPath(cmdPkg, changedPackages, allPackages); len(path) > 0 {
					logrus.Infof("Tool %s is affected via dependency chain: %s", toolName, strings.Join(path, " -> "))
				} else {
					logrus.Infof("Tool %s is affected (dependency chain could not be fully reconstructed)", toolName)
				}
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

// dependencyPath returns one dependency chain from the given cmdPkg down to any of the changedPackages, if such a chain exists.
func dependencyPath(cmdPkg *packages.Package, changedPackages sets.Set[string], allPackages map[string]*packages.Package) []string {
	visited := sets.New[string]()
	return findDependencyPathFrom(cmdPkg, changedPackages, allPackages, visited, nil)
}

func findDependencyPathFrom(pkg *packages.Package, changedPackages sets.Set[string], allPackages map[string]*packages.Package, visited sets.Set[string], path []string) []string {
	if pkg == nil || pkg.PkgPath == "" {
		return nil
	}
	if visited.Has(pkg.PkgPath) {
		return nil
	}
	visited.Insert(pkg.PkgPath)

	currentPath := append(path, pkg.PkgPath)
	if changedPackages.Has(pkg.PkgPath) {
		return currentPath
	}

	for _, imp := range pkg.Imports {
		var next *packages.Package
		if impPkg, ok := allPackages[imp.PkgPath]; ok {
			next = impPkg
		} else {
			next = imp
		}
		if candidate := findDependencyPathFrom(next, changedPackages, allPackages, visited, currentPath); candidate != nil {
			return candidate
		}
	}

	return nil
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
