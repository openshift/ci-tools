package main

import (
	"strings"
)

type ocpImageConfig struct {
	Content        ocpImageConfigContent `json:"content"`
	From           ocpImageConfigFrom    `json:"from"`
	Push           ocpImageConfigPush    `json:"push"`
	Name           string                `json:"name"`
	SourceFileName string                `json:"-"`
}

type ocpImageConfigContent struct {
	Source ocpImageConfigSource `json:"source"`
}

type ocpImageConfigSource struct {
	Dockerfile string                  `json:"dockerfile"`
	Git        ocpImageConfigSourceGit `json:"git"`
}

type ocpImageConfigSourceGit struct {
	URL string `json:"url"`
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
	Image string `json:"image"`
	// Ref: https://gist.github.com/jupierce/d337bd555ed2ca9fe4fd5ede24c06b92
	// UpstreamImage contains the UpstreamImage. No omitempty to guarantee non-nilness.
	// It is a pointer so we can manipulate the field even when its a map
	// member
	UpstreamImage *string `json:"upstream_image"`
}

// TODO: Remove, this is a hack
func (sm streamMap) defaultImages() {
	for k, v := range sm {
		if v.UpstreamImage == nil {
			continue
		}
		if *v.UpstreamImage == "" {
			*sm[k].UpstreamImage = v.Image
		}
	}
}
