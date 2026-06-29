package main

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"

	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/yaml"

	"github.com/openshift/ci-tools/pkg/api/ocplifecycle"
)

var repositoryPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+(?:/[A-Za-z0-9_.-]+)?$`)

type forwardingConfig struct {
	DefaultBranch   *defaultBranchForwarding  `json:"default_branch,omitempty"`
	ReleaseBranches []releaseBranchForwarding `json:"release_branches,omitempty"`
}

type defaultBranchForwarding struct {
	ConfigsPromotingTo string   `json:"configs_promoting_to"`
	Targets            []string `json:"targets"`
	Ignore             []string `json:"ignore,omitempty"`
}

type releaseBranchForwarding struct {
	Source  string   `json:"source"`
	Targets []string `json:"targets"`
	Ignore  []string `json:"ignore,omitempty"`
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
		if err := validateTargets("default_branch.targets", c.DefaultBranch.Targets, ""); err != nil {
			errs = append(errs, err)
		}
		if err := validateIgnored("default_branch.ignore", c.DefaultBranch.Ignore); err != nil {
			errs = append(errs, err)
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
		if err := validateTargets(prefix+".targets", forwarding.Targets, forwarding.Source); err != nil {
			errs = append(errs, err)
		}
		if err := validateIgnored(prefix+".ignore", forwarding.Ignore); err != nil {
			errs = append(errs, err)
		}
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

func validateIgnored(field string, ignored []string) error {
	seen := sets.New[string]()
	var errs []error
	for _, entry := range ignored {
		if entry != strings.TrimSpace(entry) || !repositoryPattern.MatchString(entry) {
			errs = append(errs, fmt.Errorf("%s entry %q must be an org or org/repo", field, entry))
		}
		if seen.Has(entry) {
			errs = append(errs, fmt.Errorf("%s contains duplicate entry %q", field, entry))
		}
		seen.Insert(entry)
	}
	return errors.Join(errs...)
}

func isIgnored(ignored []string, org, repo string) bool {
	repository := org + "/" + repo
	for _, entry := range ignored {
		if entry == org || entry == repository {
			return true
		}
	}
	return false
}
