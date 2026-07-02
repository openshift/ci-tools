package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"

	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/yaml"

	"github.com/openshift/ci-tools/pkg/api/ocplifecycle"
)

var (
	legacyRepositoryPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+(?:/[A-Za-z0-9_.-]+)?$`)
	namePattern             = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
	branchPattern           = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
)

const (
	branchFamilyRelease   = "release"
	branchFamilyOpenShift = "openshift"
)

type forwardingConfig struct {
	DefaultBranch   *defaultBranchForwarding  `json:"default_branch,omitempty"`
	ReleaseBranches []releaseBranchForwarding `json:"release_branches,omitempty"`
}

type defaultBranchForwarding struct {
	ConfigsPromotingTo string         `json:"configs_promoting_to"`
	Forward            []forwardBlock `json:"forward,omitempty"`
	Targets            []string       `json:"targets,omitempty"`
	Ignore             []ignoreEntry  `json:"ignore,omitempty"`
}

type releaseBranchForwarding struct {
	Source  string         `json:"source"`
	Forward []forwardBlock `json:"forward,omitempty"`
	Targets []string       `json:"targets,omitempty"`
	Ignore  []ignoreEntry  `json:"ignore,omitempty"`
}

type forwardBlock struct {
	Family  string        `json:"family"`
	Targets []string      `json:"targets"`
	Only    []ignoreEntry `json:"only,omitempty"`
	Ignore  []ignoreEntry `json:"ignore,omitempty"`
}

type ignoreEntry struct {
	Org    string `json:"org,omitempty"`
	Repo   string `json:"repo,omitempty"`
	Source string `json:"source,omitempty"`
	Target string `json:"target,omitempty"`
}

func (e *ignoreEntry) UnmarshalJSON(raw []byte) error {
	var legacy string
	if err := json.Unmarshal(raw, &legacy); err == nil {
		if legacy != strings.TrimSpace(legacy) || !legacyRepositoryPattern.MatchString(legacy) {
			return fmt.Errorf("legacy ignore entry %q must be an org or org/repo", legacy)
		}
		parts := strings.Split(legacy, "/")
		switch len(parts) {
		case 1:
			e.Org = parts[0]
		case 2:
			e.Org, e.Repo = parts[0], parts[1]
		default:
			return fmt.Errorf("legacy ignore entry %q must be an org or org/repo", legacy)
		}
		return nil
	}
	type structured ignoreEntry
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var value structured
	if err := decoder.Decode(&value); err != nil {
		return err
	}
	*e = ignoreEntry(value)
	return nil
}

func loadForwardingConfig(path string) (*forwardingConfig, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read forwarding config: %w", err)
	}
	config := &forwardingConfig{}
	if err := yaml.UnmarshalStrict(raw, config); err != nil {
		return nil, fmt.Errorf("parse forwarding config: %w", err)
	}
	if err := config.validate(); err != nil {
		return nil, fmt.Errorf("validate forwarding config: %w", err)
	}
	return config, nil
}

func (c *forwardingConfig) validate() error {
	if c.DefaultBranch == nil && len(c.ReleaseBranches) == 0 {
		return errors.New("at least one of default_branch or release_branches must be configured")
	}
	var errs []error
	if c.DefaultBranch != nil {
		if err := validateRelease("default_branch.configs_promoting_to", c.DefaultBranch.ConfigsPromotingTo); err != nil {
			errs = append(errs, err)
		}
		if err := validateForwardingMode("default_branch", c.DefaultBranch.Forward, c.DefaultBranch.Targets, c.DefaultBranch.Ignore); err != nil {
			errs = append(errs, err)
		}
		forwards := c.DefaultBranch.forwardBlocks()
		if err := validateDuplicateForwards("default_branch.forward", forwards); err != nil {
			errs = append(errs, err)
		}
		for i, forward := range forwards {
			if err := validateForwardBlock(fmt.Sprintf("default_branch.forward[%d]", i), forward, ""); err != nil {
				errs = append(errs, err)
			}
		}
	}
	sources := sets.New[string]()
	for i, forwarding := range c.ReleaseBranches {
		prefix := fmt.Sprintf("release_branches[%d]", i)
		if err := validateRelease(prefix+".source", forwarding.Source); err != nil {
			errs = append(errs, err)
		}
		if sources.Has(forwarding.Source) {
			errs = append(errs, fmt.Errorf("%s.source %q is duplicated", prefix, forwarding.Source))
		}
		sources.Insert(forwarding.Source)
		if err := validateForwardingMode(prefix, forwarding.Forward, forwarding.Targets, forwarding.Ignore); err != nil {
			errs = append(errs, err)
		}
		forwards := forwarding.forwardBlocks()
		if err := validateDuplicateForwards(prefix+".forward", forwards); err != nil {
			errs = append(errs, err)
		}
		for j, forward := range forwards {
			if err := validateForwardBlock(fmt.Sprintf("%s.forward[%d]", prefix, j), forward, forwarding.Source); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

func validateDuplicateForwards(field string, forwards []forwardBlock) error {
	seen := sets.New[string]()
	var errs []error
	for i, forward := range forwards {
		for _, target := range forward.Targets {
			key := forward.Family + "/" + target
			if seen.Has(key) {
				errs = append(errs, fmt.Errorf("%s[%d] duplicates family/target %q", field, i, key))
			}
			seen.Insert(key)
		}
	}
	return errors.Join(errs...)
}

func (f defaultBranchForwarding) forwardBlocks() []forwardBlock {
	if len(f.Forward) > 0 {
		return f.Forward
	}
	if len(f.Targets) == 0 {
		return nil
	}
	return []forwardBlock{{Family: branchFamilyRelease, Targets: f.Targets, Ignore: f.Ignore}}
}

func (f releaseBranchForwarding) forwardBlocks() []forwardBlock {
	if len(f.Forward) > 0 {
		return f.Forward
	}
	if len(f.Targets) == 0 {
		return nil
	}
	return []forwardBlock{
		{Family: branchFamilyRelease, Targets: f.Targets, Ignore: f.Ignore},
		{Family: branchFamilyOpenShift, Targets: f.Targets, Ignore: f.Ignore},
	}
}

func validateForwardingMode(field string, forward []forwardBlock, legacyTargets []string, legacyIgnore []ignoreEntry) error {
	if len(forward) == 0 && len(legacyTargets) == 0 {
		return fmt.Errorf("%s must configure forward or legacy targets", field)
	}
	if len(forward) > 0 && len(legacyTargets) > 0 {
		return fmt.Errorf("%s must not configure both forward and legacy targets", field)
	}
	if len(forward) > 0 && len(legacyIgnore) > 0 {
		return fmt.Errorf("%s must not configure top-level ignore with forward; configure ignore per forward block", field)
	}
	return nil
}

func validateForwardBlock(field string, forward forwardBlock, source string) error {
	var errs []error
	if forward.Family != branchFamilyRelease && forward.Family != branchFamilyOpenShift {
		errs = append(errs, fmt.Errorf("%s.family %q must be one of %q or %q", field, forward.Family, branchFamilyRelease, branchFamilyOpenShift))
	}
	if err := validateTargets(field+".targets", forward.Targets, source); err != nil {
		errs = append(errs, err)
	}
	if err := validateSelectors(field+".only", forward.Only); err != nil {
		errs = append(errs, err)
	}
	if err := validateIgnored(field+".ignore", forward.Ignore); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

func validateRelease(field, value string) error {
	if _, err := ocplifecycle.ParseMajorMinor(value); err != nil {
		return fmt.Errorf("%s %q is invalid: %w", field, value, err)
	}
	return nil
}

func validateTargets(field string, targets []string, source string) error {
	if len(targets) == 0 {
		return fmt.Errorf("%s must contain at least one release", field)
	}
	seen := sets.New[string]()
	var errs []error
	for _, target := range targets {
		if err := validateRelease(field, target); err != nil {
			errs = append(errs, err)
		}
		if seen.Has(target) {
			errs = append(errs, fmt.Errorf("%s contains duplicate release %q", field, target))
		}
		if source != "" && target == source {
			errs = append(errs, fmt.Errorf("%s contains source release %q", field, source))
		}
		seen.Insert(target)
	}
	return errors.Join(errs...)
}

func validateIgnored(field string, ignored []ignoreEntry) error {
	return validateSelectors(field, ignored)
}

func validateSelectors(field string, ignored []ignoreEntry) error {
	seen := sets.New[string]()
	var errs []error
	for _, entry := range ignored {
		if entry.Org == "" && entry.Repo == "" && entry.Source == "" && entry.Target == "" {
			errs = append(errs, fmt.Errorf("%s entry must contain at least one selector", field))
		}
		if entry.Repo != "" && entry.Org == "" {
			errs = append(errs, fmt.Errorf("%s entry with repo %q must also set org", field, entry.Repo))
		}
		if entry.Org != "" && !namePattern.MatchString(entry.Org) {
			errs = append(errs, fmt.Errorf("%s org %q is invalid", field, entry.Org))
		}
		if entry.Repo != "" && !namePattern.MatchString(entry.Repo) {
			errs = append(errs, fmt.Errorf("%s repo %q is invalid", field, entry.Repo))
		}
		if entry.Source != "" && !branchPattern.MatchString(entry.Source) {
			errs = append(errs, fmt.Errorf("%s source %q is invalid", field, entry.Source))
		}
		if entry.Target != "" && !branchPattern.MatchString(entry.Target) {
			errs = append(errs, fmt.Errorf("%s target %q is invalid", field, entry.Target))
		}
		key := entry.key()
		if seen.Has(key) {
			errs = append(errs, fmt.Errorf("%s contains duplicate entry %q", field, key))
		}
		seen.Insert(key)
	}
	return errors.Join(errs...)
}

func (e ignoreEntry) key() string {
	return strings.Join([]string{e.Org, e.Repo, e.Source, e.Target}, "\x00")
}

func isIgnored(ignored []ignoreEntry, org, repo, source, target string) bool {
	for _, entry := range ignored {
		if entry.Org != "" && entry.Org != org {
			continue
		}
		if entry.Repo != "" && entry.Repo != repo {
			continue
		}
		if entry.Source != "" && entry.Source != source {
			continue
		}
		if entry.Target != "" && entry.Target != target {
			continue
		}
		if entry.Org != "" || entry.Repo != "" || entry.Source != "" || entry.Target != "" {
			return true
		}
	}
	return false
}

func isIncluded(only []ignoreEntry, org, repo, source, target string) bool {
	if len(only) == 0 {
		return true
	}
	return isIgnored(only, org, repo, source, target)
}
