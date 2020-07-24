package main

import (
	"errors"
	"strings"
)

type ocpImageConfig struct {
	Content        *ocpImageConfigContent `json:"content"`
	From           ocpImageConfigFrom     `json:"from"`
	Push           ocpImageConfigPush     `json:"push"`
	Name           string                 `json:"name"`
	SourceFileName string                 `json:"-"`
}

func (o ocpImageConfig) validate() error {
	if o.Content == nil {
		return nil
	}
	if o.Content.Source.Alias != "" && o.Content.Source.Git != nil {
		return errors.New("both content.source.alias and content.source.git are set")
	}
	return nil
}

type ocpImageConfigContent struct {
	Source ocpImageConfigSource `json:"source"`
}

type ocpImageConfigSource struct {
	Dockerfile string `json:"dockerfile"`
	Alias      string `json:"alias"`
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
	Builder []ocpImageConfigFromStream `json:"builder"`
	Stream  string                     `json:"stream"`
}

type ocpImageConfigFromStream struct {
	Stream string `json:"stream"`
}

type ocpImageConfigPush struct {
	Also           []string `json:"also,omitempty"`
	AdditionalTags []string `json:"additional_tags,omitempty"`
}

func (oic ocpImageConfig) dockerfile() string {
	if oic.Content.Source.Dockerfile != "" {
		return oic.Content.Source.Dockerfile
	}
	return "Dockerfile"
}

func (oic ocpImageConfig) stages() []string {
	var result []string
	for _, builder := range oic.From.Builder {
		result = append(result, builder.Stream)
	}
	return append(result, oic.From.Stream)
}

func (oic ocpImageConfig) orgRepo() string {
	if oic.Content.Source.Git.URL == "" {
		return oic.Name
	}
	return strings.TrimSuffix(strings.TrimPrefix(oic.Content.Source.Git.URL, "git@github.com:"), ".git")
}

type streamMap map[string]streamElement

type streamElement struct {
	Image         string `json:"image"`
	UpstreamImage string `json:"upstream_image"`
}

type groupYAML struct {
	Sources map[string]ocpImageConfigSourceGit `json:"sources"`
}
