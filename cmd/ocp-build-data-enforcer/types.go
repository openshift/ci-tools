package main

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"
)

type ocpImageConfig struct {
	Content        *ocpImageConfigContent `json:"content"`
	From           ocpImageConfigFrom     `json:"from"`
	Push           ocpImageConfigPush     `json:"push"`
	Name           string                 `json:"name"`
	SourceFileName string                 `json:"-"`
}

func (o ocpImageConfig) validate() error {
	var errs []error
	if o.Content != nil && o.Content.Source.Alias != "" && o.Content.Source.Git != nil {
		errs = append(errs, errors.New("both content.source.alias and content.source.git are set"))
	}
	if err := o.From.validate(); err != nil {
		errs = append(errs, fmt.Errorf(".from failed validation: %w", err))
	}
	for idx, cfg := range o.From.Builder {
		if err := cfg.validate(); err != nil {
			errs = append(errs, fmt.Errorf(".from.%d failed validation: %w", idx, err))
		}
	}

	return utilerrors.NewAggregate(errs)
}

type ocpImageConfigContent struct {
	Source ocpImageConfigSource `json:"source"`
}

type ocpImageConfigSource struct {
	Dockerfile string `json:"dockerfile"`
	Alias      string `json:"alias"`
	Path       string `json:"path"`
	// +Optional, mutually exclusive with alias
	Git *ocpImageConfigSourceGit `json:"git,omitempty"`
}

type ocpImageConfigSourceGit struct {
	URL    string                        `json:"url"`
	Branch ocpImageConfigSourceGitBranch `json:"branch"`
}

type ocpImageConfigSourceGitBranch struct {
	Taget string `json:"target"`
}

type ocpImageConfigFrom struct {
	Builder                  []ocpImageConfigFromStream `json:"builder"`
	ocpImageConfigFromStream `json:",inline"`
}

type ocpImageConfigFromStream struct {
	Stream string `json:"stream"`
	Member string `json:"member"`
}

func (icfs ocpImageConfigFromStream) validate() error {
	if icfs.Stream == "" && icfs.Member == "" {
		return errors.New("both stream and member were unset")
	}
	if icfs.Stream != "" && icfs.Member != "" {
		return fmt.Errorf("both stream(%s) and member(%s) were set", icfs.Stream, icfs.Member)
	}
	return nil
}

type ocpImageConfigPush struct {
	Also           []string `json:"also,omitempty"`
	AdditionalTags []string `json:"additional_tags,omitempty"`
}

func (oic ocpImageConfig) dockerfile() string {
	if oic.Content.Source.Dockerfile == "" {
		oic.Content.Source.Dockerfile = "Dockerfile"
	}
	return filepath.Join(oic.Content.Source.Path, oic.Content.Source.Dockerfile)
}

func (oic ocpImageConfig) stages() ([]string, error) {
	var result []string
	var errs []error
	for idx, builder := range oic.From.Builder {
		if builder.Stream == "" {
			errs = append(errs, fmt.Errorf("couldn't dereference from.builder.%d", idx))
		}
		result = append(result, builder.Stream)
	}
	if oic.From.Stream == "" {
		errs = append(errs, errors.New("couldn't dereference from.stream"))
	}
	return append(result, oic.From.Stream), utilerrors.NewAggregate(errs)
}

func (oic ocpImageConfig) orgRepo(mappings []publicPrivateMapping) string {
	var name string
	if oic.Content.Source.Git.URL == "" {
		name = oic.Name
	}
	if name == "" {
		name = strings.TrimSuffix(strings.TrimPrefix(oic.Content.Source.Git.URL, "git@github.com:"), ".git")
	}
	return getPublicRepo(name, mappings)
}

type streamMap map[string]streamElement

type streamElement struct {
	Image         string `json:"image"`
	UpstreamImage string `json:"upstream_image"`
}

type groupYAML struct {
	Sources         map[string]ocpImageConfigSourceGit `json:"sources"`
	PublicUpstreams []publicPrivateMapping             `json:"public_upstreams,omitempty"`
}

type publicPrivateMapping struct {
	Private string `json:"private"`
	Public  string `json:"public"`
}

func getPublicRepo(orgRepo string, mappings []publicPrivateMapping) string {
	orgRepo = "https://github.com/" + orgRepo
	var replacementFrom, replacementTo string
	for _, mapping := range mappings {
		if !strings.HasPrefix(orgRepo, mapping.Private) {
			continue
		}
		if len(replacementFrom) > len(mapping.Private) {
			continue
		}
		replacementFrom = mapping.Private
		replacementTo = mapping.Public
	}

	if replacementTo == "" {
		return strings.TrimPrefix(orgRepo, "https://github.com/")
	}

	return strings.TrimPrefix(strings.Replace(orgRepo, replacementFrom, replacementTo, 1), "https://github.com/")
}
