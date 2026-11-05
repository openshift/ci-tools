package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/yaml"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/prowgen"
)

// selectConfigs copies every managed configuration, including variants, to destination.
func selectConfigs(source, destination string, cfg *prowgen.ManagedReposConfig) (int, error) {
	if cfg == nil {
		return 0, nil
	}
	repos := sets.New[string]()
	for repo := range cfg.Repos {
		repos.Insert(repo)
	}
	count := 0
	for _, repo := range sets.List(repos) {
		if !validPath(repo, 2) {
			return 0, fmt.Errorf("invalid repository: %s", repo)
		}
		directory := filepath.Join(source, "ci-operator/config", repo)
		entries, err := os.ReadDir(directory)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return 0, err
		}
		for _, entry := range entries {
			if entry.IsDir() || filepath.Ext(entry.Name()) != ".yaml" {
				continue
			}
			file := filepath.Join(directory, entry.Name())
			if entry.Type()&os.ModeSymlink != 0 {
				return 0, fmt.Errorf("configuration is a symlink: %s", file)
			}
			selected, err := copyManagedConfig(file, filepath.Join(destination, repo, entry.Name()), repo, cfg)
			if err != nil {
				return 0, err
			}
			if selected {
				count++
			}
		}
	}
	return count, nil
}

// copyManagedConfig copies one configuration if its branch is managed.
func copyManagedConfig(source, destination, repo string, cfg *prowgen.ManagedReposConfig) (bool, error) {
	data, err := os.ReadFile(source)
	if err != nil {
		return false, err
	}
	var config api.ReleaseBuildConfiguration
	if err := yaml.Unmarshal(data, &config); err != nil {
		return false, fmt.Errorf("reading %s: %w", source, err)
	}
	if config.Metadata.Org+"/"+config.Metadata.Repo != repo {
		return false, fmt.Errorf("metadata does not match %s", source)
	}
	if !cfg.IsManaged(repo, config.Metadata.Branch) {
		return false, nil
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0755); err != nil {
		return false, err
	}
	if err := os.WriteFile(destination, data, 0644); err != nil {
		return false, err
	}
	return true, nil
}

// validPath accepts canonical repository or inventory paths without traversal components.
func validPath(path string, components int) bool {
	if !filepath.IsLocal(path) || filepath.Clean(path) != path {
		return false
	}
	parts := strings.Split(path, "/")
	if len(parts) != components {
		return false
	}
	for _, part := range parts {
		if part == "." || part == ".." || part == "" {
			return false
		}
		for _, char := range part {
			if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '_' || char == '-' || char == '.' {
				continue
			}
			return false
		}
	}
	return true
}

// jobFileCollector gathers canonical generated shard paths.
type jobFileCollector struct {
	root  string
	files sets.Set[string]
}

// visit validates generated shard names and rejects symlinks.
func (c *jobFileCollector) visit(path string, entry fs.DirEntry, err error) error {
	if err != nil {
		return err
	}
	if entry.Type()&os.ModeSymlink != 0 {
		return fmt.Errorf("generated path is a symlink: %s", path)
	}
	if entry.IsDir() {
		return nil
	}
	relative, err := filepath.Rel(c.root, path)
	if err != nil {
		return err
	}
	if !validPath(relative, 3) || filepath.Ext(relative) != ".yaml" {
		return fmt.Errorf("invalid inventory path: %s", relative)
	}
	c.files.Insert(relative)
	return nil
}

// jobFiles returns the generated YAML inventory beneath root.
func jobFiles(root string) (sets.Set[string], error) {
	collector := jobFileCollector{root: root, files: sets.New[string]()}
	if err := filepath.WalkDir(root, collector.visit); err != nil {
		return nil, err
	}
	return collector.files, nil
}

// readInventory reads the previously owned paths; a missing inventory owns nothing.
func readInventory(path string) (sets.Set[string], error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return sets.New[string](), nil
	}
	if err != nil {
		return nil, err
	}
	files := sets.New[string]()
	if len(data) == 0 {
		return files, nil
	}
	for _, file := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
		if !validPath(file, 3) || filepath.Ext(file) != ".yaml" {
			return nil, fmt.Errorf("invalid inventory path: %s", file)
		}
		files.Insert(file)
	}
	return files, nil
}

// writeInventory atomically records the sorted set of owned shard paths.
func writeInventory(path string, files sets.Set[string]) error {
	data := ""
	if files.Len() > 0 {
		data = strings.Join(sets.List(files), "\n") + "\n"
	}
	return writeState(path, []byte(data))
}

// writeState replaces a state file through a temporary file in the same directory.
func writeState(path string, data []byte) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".state.")
	if err != nil {
		return err
	}
	defer removeTemporary(file.Name())
	if _, err := file.Write(data); err != nil {
		closeFile(file)
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}

// removeTemporary removes a temporary state file if it has not already been renamed.
func removeTemporary(path string) {
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		logrus.WithError(err).WithField("path", path).Warn("Failed to remove temporary state file")
	}
}

// rejectSymlinks rejects symlinks anywhere in an existing owned tree.
func rejectSymlinks(root string) error {
	err := filepath.WalkDir(root, rejectSymlink)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// rejectSymlink checks an individual entry during an owned-tree walk.
func rejectSymlink(path string, entry fs.DirEntry, err error) error {
	if err != nil {
		return err
	}
	if entry.Type()&os.ModeSymlink != 0 {
		return fmt.Errorf("owned path is a symlink: %s", path)
	}
	return nil
}

// copyFile copies a regular file into a newly created destination directory.
func copyFile(source, destination string) error {
	data, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0755); err != nil {
		return err
	}
	return os.WriteFile(destination, data, 0644)
}

// resolvedPath resolves symlinks in existing ancestors of a possibly new path.
func resolvedPath(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err == nil {
		return resolved, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	parent, err := resolvedPath(filepath.Dir(absolute))
	if err != nil {
		return "", err
	}
	return filepath.Join(parent, filepath.Base(absolute)), nil
}

// saveCandidate copies a preview to a new directory outside the watched EFS tree.
func saveCandidate(candidate, destination, root string) error {
	destination, err := resolvedPath(destination)
	if err != nil {
		return err
	}
	relative, err := filepath.Rel(root, destination)
	if err != nil {
		return err
	}
	if relative == "." || filepath.IsLocal(relative) {
		return errors.New("candidate artifacts must be outside the EFS root")
	}
	if err := os.Mkdir(destination, 0755); err != nil {
		return err
	}
	files, err := jobFiles(candidate)
	if err != nil {
		return err
	}
	for _, file := range sets.List(files) {
		if err := copyFile(filepath.Join(candidate, file), filepath.Join(destination, file)); err != nil {
			return err
		}
	}
	return nil
}
