package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/util/sets"
	prowconfig "sigs.k8s.io/prow/pkg/config"
	"sigs.k8s.io/yaml"

	"github.com/openshift/ci-tools/pkg/jobconfig"
	"github.com/openshift/ci-tools/pkg/prowgen"
)

// commands runs the existing Git, generation, sanitization and validation tools.
type commands interface {
	run(ctx context.Context, dir, program string, args ...string) error
	output(ctx context.Context, dir, program string, args ...string) ([]byte, error)
}

// execCommands connects subprocesses to the publisher's log streams.
type execCommands struct{}

// run executes a command in dir, streaming its output.
func (execCommands) run(ctx context.Context, dir, program string, args ...string) error {
	cmd := exec.CommandContext(ctx, program, args...)
	cmd.Dir, cmd.Stdout, cmd.Stderr = dir, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("running %s: %w", program, err)
	}
	return nil
}

// output executes a command in dir and returns its standard output.
func (execCommands) output(ctx context.Context, dir, program string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, program, args...)
	cmd.Dir, cmd.Stderr = dir, os.Stderr
	data, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("running %s: %w", program, err)
	}
	return data, nil
}

// publisher generates candidates and owns only the paths recorded in its inventory.
type publisher struct {
	commands commands
	rename   func(string, string) error
}

// run serializes source reconciliation, candidate generation and publication.
func (p *publisher) run(ctx context.Context, opts options) error {
	if err := opts.complete(); err != nil {
		return err
	}
	state := filepath.Join(opts.efsRoot, "..prowgen")
	if err := rejectSymlinks(state); err != nil {
		return err
	}
	if err := os.MkdirAll(state, 0755); err != nil {
		return err
	}
	lock, err := acquireLock(ctx, filepath.Join(state, "publish.lock"))
	if err != nil {
		return err
	}
	defer closeFile(lock)
	original := opts.releaseRepo
	opts, err = p.reconcileSource(ctx, opts)
	if err != nil {
		return err
	}
	if opts.releaseRepo != original {
		defer p.removeWorktree(original, opts.releaseRepo)
	}
	work, err := os.MkdirTemp(state, "run.")
	if err != nil {
		return err
	}
	defer removeDirectory(work)
	candidate, err := p.prepareCandidate(ctx, opts, work)
	if err != nil {
		return err
	}
	if opts.publish {
		return p.publishCandidate(ctx, opts, work, candidate)
	}
	if opts.candidateDir != "" {
		if err := saveCandidate(candidate.directory, opts.candidateDir, opts.efsRoot); err != nil {
			return err
		}
	}
	logrus.WithField("configs", candidate.configs).Info("Generated and sanitized candidate; no visible EFS files changed")
	return nil
}

// jobCandidate contains staged jobs and the old and desired ownership inventories.
type jobCandidate struct {
	directory string
	configs   int
	previous  sets.Set[string]
	desired   sets.Set[string]
}

// generateCandidate discovers the shards produced by the current managed configurations.
func (p *publisher) generateCandidate(ctx context.Context, opts options, work string) (jobCandidate, error) {
	candidate := jobCandidate{directory: filepath.Join(work, "jobs")}
	if err := os.MkdirAll(candidate.directory, 0755); err != nil {
		return candidate, err
	}
	cfg, err := prowgen.LoadManagedReposConfig(opts.managedConfig)
	if err != nil {
		return candidate, err
	}
	selected := filepath.Join(work, "config")
	candidate.configs, err = selectConfigs(opts.releaseRepo, selected, cfg)
	if err != nil {
		return candidate, err
	}
	if candidate.configs > 0 {
		if err := p.generate(ctx, opts.releaseRepo, selected, candidate.directory); err != nil {
			return candidate, err
		}
	}
	candidate.desired, err = jobFiles(candidate.directory)
	return candidate, err
}

// prepareCandidate restores overrides before regenerating and sanitizing managed jobs.
func (p *publisher) prepareCandidate(ctx context.Context, opts options, work string) (jobCandidate, error) {
	candidate, err := p.generateCandidate(ctx, opts, work)
	if err != nil {
		return candidate, err
	}
	candidate.previous, err = readInventory(filepath.Join(opts.efsRoot, "..prowgen/inventory"))
	if err != nil {
		return candidate, err
	}
	output := filepath.Join(opts.efsRoot, "generated")
	if err := rejectSymlinks(output); err != nil {
		return candidate, err
	}
	if err := seedJobs(opts.releaseRepo, output, candidate.directory, candidate.previous, candidate.desired); err != nil {
		return candidate, err
	}
	if candidate.configs == 0 {
		return candidate, nil
	}
	if err := p.generate(ctx, opts.releaseRepo, filepath.Join(work, "config"), candidate.directory); err != nil {
		return candidate, err
	}
	return candidate, p.sanitize(ctx, opts.releaseRepo, candidate.directory)
}

// sanitize applies the checked-in cluster policy to the generated jobs.
func (p *publisher) sanitize(ctx context.Context, source, candidate string) error {
	return p.commands.run(ctx, source, "sanitize-prow-jobs", "--prow-jobs-dir", candidate,
		"--config-path", filepath.Join(source, "core-services/sanitize-prow-jobs/_config.yaml"),
		"--cluster-config-path", filepath.Join(source, "core-services/sanitize-prow-jobs/_clusters.yaml"))
}

// publishCandidate validates the complete job tree before changing visible output.
func (p *publisher) publishCandidate(ctx context.Context, opts options, work string, candidate jobCandidate) error {
	unowned := filepath.Join(work, "unowned")
	if err := os.MkdirAll(unowned, 0755); err != nil {
		return err
	}
	snapshot := visibleSnapshot{root: opts.efsRoot, destination: unowned, owned: candidate.previous}
	if err := filepath.WalkDir(opts.efsRoot, snapshot.visit); err != nil {
		return err
	}
	if err := p.validate(ctx, opts.releaseRepo, candidate.directory, unowned); err != nil {
		return err
	}
	revision, err := p.commands.output(ctx, opts.releaseRepo, "git", "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	state := filepath.Join(opts.efsRoot, "..prowgen")
	if err := p.publish(state, filepath.Join(opts.efsRoot, "generated"), candidate.directory, candidate.previous, candidate.desired); err != nil {
		return err
	}
	if err := writeState(filepath.Join(state, "revision"), revision); err != nil {
		return err
	}
	logrus.WithFields(logrus.Fields{"files": candidate.desired.Len(), "revision": strings.TrimSpace(string(revision))}).Info("Published managed jobs")
	return nil
}

// reconcileSource remaps ownership rules into the latest release checkout when publishing.
func (p *publisher) reconcileSource(ctx context.Context, opts options) (options, error) {
	if !opts.publish {
		return opts, nil
	}
	managed, err := filepath.EvalSymlinks(opts.managedConfig)
	if err != nil {
		return opts, err
	}
	relative, err := filepath.Rel(opts.releaseRepo, managed)
	if err != nil {
		return opts, err
	}
	if !filepath.IsLocal(relative) {
		return opts, errors.New("published ownership must come from the source checkout")
	}
	opts.releaseRepo, err = p.latestSource(ctx, opts.releaseRepo)
	if err != nil {
		return opts, err
	}
	opts.managedConfig = filepath.Join(opts.releaseRepo, relative)
	return opts, nil
}

// acquireLock waits for an advisory filesystem lock, respecting cancellation.
func acquireLock(ctx context.Context, path string) (*os.File, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return nil, err
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			closeFile(file)
			return nil, err
		}
		err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return file, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EINTR) {
			closeFile(file)
			return nil, fmt.Errorf("locking %s: %w", path, err)
		}
		select {
		case <-ctx.Done():
			closeFile(file)
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

// latestSource fetches release/main and creates an isolated checkout for old triggers.
func (p *publisher) latestSource(ctx context.Context, source string) (string, error) {
	if err := p.commands.run(ctx, source, "git", "fetch", "--no-tags", "https://github.com/openshift/release.git", "main"); err != nil {
		return "", err
	}
	head, err := p.commands.output(ctx, source, "git", "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	latest, err := p.commands.output(ctx, source, "git", "rev-parse", "FETCH_HEAD")
	if err != nil {
		return "", err
	}
	if bytes.Equal(head, latest) {
		return source, nil
	}
	worktree, err := os.MkdirTemp("", "prowgen-efs-source.")
	if err != nil {
		return "", err
	}
	if err := p.commands.run(ctx, source, "git", "worktree", "add", "--detach", worktree, strings.TrimSpace(string(latest))); err != nil {
		removeDirectory(worktree)
		return "", err
	}
	logrus.WithField("revision", strings.TrimSpace(string(latest))).Info("Reconciling latest release/main")
	return worktree, nil
}

// removeWorktree removes an isolated checkout even after the run is cancelled.
func (p *publisher) removeWorktree(source, worktree string) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err := p.commands.run(ctx, source, "git", "worktree", "remove", "--force", worktree); err != nil {
		logrus.WithError(err).WithField("path", worktree).Warn("Failed to remove source worktree")
	}
}

// generate runs normal Prowgen only against the isolated managed configuration tree.
func (p *publisher) generate(ctx context.Context, source, selected, candidate string) error {
	return p.commands.run(ctx, source, "ci-operator-prowgen", "--from-dir", selected,
		"--registry", filepath.Join(source, "ci-operator/step-registry"), "--to-dir", candidate)
}

// validate checks the combined Git, proposed managed and other visible EFS jobs.
func (p *publisher) validate(ctx context.Context, source, candidate, unowned string) error {
	return p.commands.run(ctx, source, "checkconfig",
		"--config-path", filepath.Join(source, "core-services/prow/02_config/_config.yaml"),
		"--supplemental-prow-config-dir", "core-services/prow/02_config",
		"--plugin-config", filepath.Join(source, "core-services/prow/02_config/_plugins.yaml"),
		"--supplemental-plugin-config-dir", "core-services/prow/02_config",
		"--job-config-path", filepath.Join(source, "ci-operator/jobs"),
		"--job-config-path", candidate, "--job-config-path", unowned,
		"--strict", "--exclude-warning", "long-job-names", "--exclude-warning", "mismatched-tide-lenient")
}

// seedJobs preserves generated job overrides and refuses ownership of manual shards.
func seedJobs(source, output, candidate string, old, desired sets.Set[string]) error {
	for _, file := range sets.List(desired) {
		seed := filepath.Join(source, "ci-operator/jobs", file)
		published := filepath.Join(output, file)
		if _, err := os.Stat(published); err == nil {
			if !old.Has(file) {
				return fmt.Errorf("refusing to replace unowned file: %s", file)
			}
			seed = published
		} else if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		data, err := os.ReadFile(seed)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if err := generatedOnly(data); err != nil {
			return fmt.Errorf("%s: %w", seed, err)
		}
		if err := os.WriteFile(filepath.Join(candidate, file), data, 0644); err != nil {
			return err
		}
	}
	return nil
}

// generatedOnly rejects shards containing jobs owned by another generator or a human.
func generatedOnly(data []byte) error {
	var jobs prowconfig.JobConfig
	if err := yaml.Unmarshal(data, &jobs); err != nil {
		return err
	}
	for _, group := range jobs.PresubmitsStatic {
		for _, job := range group {
			if job.Labels[jobconfig.LabelGenerator] != "prowgen" {
				return fmt.Errorf("separate hand-written jobs before migrating %s", job.Name)
			}
		}
	}
	for _, group := range jobs.PostsubmitsStatic {
		for _, job := range group {
			if job.Labels[jobconfig.LabelGenerator] != "prowgen" {
				return fmt.Errorf("separate hand-written jobs before migrating %s", job.Name)
			}
		}
	}
	for _, job := range jobs.Periodics {
		if job.Labels[jobconfig.LabelGenerator] != "prowgen" {
			return fmt.Errorf("separate hand-written jobs before migrating %s", job.Name)
		}
	}
	return nil
}

// publish saves a recovery inventory before atomically replacing individual files.
func (p *publisher) publish(state, output, candidate string, old, desired sets.Set[string]) error {
	inventory := filepath.Join(state, "inventory")
	if err := writeInventory(inventory, old.Union(desired)); err != nil {
		return err
	}
	for _, file := range sets.List(desired) {
		if err := p.replaceFile(filepath.Join(candidate, file), filepath.Join(output, file)); err != nil {
			return fmt.Errorf("publishing %s: %w", file, err)
		}
	}
	for _, file := range sets.List(old.Difference(desired)) {
		if err := os.Remove(filepath.Join(output, file)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return writeInventory(inventory, desired)
}

// replaceFile atomically installs a staged file unless its published contents match.
func (p *publisher) replaceFile(staged, destination string) error {
	data, err := os.ReadFile(staged)
	if err != nil {
		return err
	}
	existing, err := os.ReadFile(destination)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err == nil && bytes.Equal(data, existing) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0755); err != nil {
		return err
	}
	if err := os.Chmod(staged, 0644); err != nil {
		return err
	}
	return p.rename(staged, destination)
}

// visibleSnapshot copies other writers' visible YAML into an isolated validation tree.
type visibleSnapshot struct {
	root        string
	destination string
	owned       sets.Set[string]
}

// visit skips Prow's hidden directories and excludes only recorded managed files.
func (s *visibleSnapshot) visit(path string, entry fs.DirEntry, err error) error {
	if err != nil {
		return err
	}
	if path == s.root {
		return nil
	}
	if strings.HasPrefix(entry.Name(), "..") {
		if entry.IsDir() {
			return filepath.SkipDir
		}
		return nil
	}
	if entry.Type()&os.ModeSymlink != 0 {
		return fmt.Errorf("visible EFS path is a symlink: %s", path)
	}
	if entry.IsDir() || (filepath.Ext(path) != ".yaml" && filepath.Ext(path) != ".yml") {
		return nil
	}
	relative, err := filepath.Rel(s.root, path)
	if err != nil {
		return err
	}
	if strings.HasPrefix(relative, "generated/") && s.owned.Has(strings.TrimPrefix(relative, "generated/")) {
		return nil
	}
	return copyFile(path, filepath.Join(s.destination, relative))
}

// closeFile closes a file, logging cleanup failures.
func closeFile(file *os.File) {
	if err := file.Close(); err != nil {
		logrus.WithError(err).Warn("Failed to close file")
	}
}

// removeDirectory removes a temporary tree, logging cleanup failures.
func removeDirectory(path string) {
	if err := os.RemoveAll(path); err != nil {
		logrus.WithError(err).WithField("path", path).Warn("Failed to remove temporary directory")
	}
}
