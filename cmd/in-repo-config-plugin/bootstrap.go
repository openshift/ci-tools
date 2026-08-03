package main

import (
	"bytes"
	"embed"
	"fmt"
	"text/template"

	"sigs.k8s.io/yaml"

	prowconfig "sigs.k8s.io/prow/pkg/config"

	"github.com/openshift/ci-tools/pkg/config"
	jc "github.com/openshift/ci-tools/pkg/jobconfig"
)

const (
	releaseRepoOrg       = "openshift"
	releaseRepoName      = "release"
	clusterProfilesCfg   = "cluster-profiles/cluster-profiles-config.yaml"
)

//go:embed templates
var templateFS embed.FS

var bootstrapTemplates = template.Must(
	template.ParseFS(templateFS, "templates/*.yaml.tmpl"),
)

type bootstrapParams struct {
	Org              string
	Repo             string
	Branch           string
	BranchRegex      string
	UseDir           bool
	CheckconfigImage string
	ProwgenImage     string
	ConfigDirPath    string
	ConfigFilePath   string
	RegistryPath     string
	ProfilesPath     string
}

func newBootstrapParams(org, repo, branch string, useDir bool, prowgenImage, checkconfigImage string) bootstrapParams {
	repoPath := fmt.Sprintf("/home/prow/go/src/github.com/%s/%s", org, repo)
	releasePath := fmt.Sprintf("/home/prow/go/src/github.com/%s/%s", releaseRepoOrg, releaseRepoName)

	return bootstrapParams{
		Org:              org,
		Repo:             repo,
		Branch:           branch,
		BranchRegex:      jc.ExactlyBranch(branch),
		UseDir:           useDir,
		CheckconfigImage: checkconfigImage,
		ProwgenImage:     prowgenImage,
		ConfigDirPath:    fmt.Sprintf("%s/%s", repoPath, ciOperatorDir),
		ConfigFilePath:   fmt.Sprintf("%s/.ci-operator.yaml", repoPath),
		RegistryPath:     fmt.Sprintf("%s/%s", releasePath, config.RegistryPath),
		ProfilesPath:     fmt.Sprintf("%s/%s/%s", releasePath, config.RegistryPath, clusterProfilesCfg),
	}
}

func generateBootstrapJobs(params bootstrapParams) (*prowconfig.JobConfig, error) {
	orgrepo := fmt.Sprintf("%s/%s", params.Org, params.Repo)

	var presubmit prowconfig.Presubmit
	if err := renderTemplate("config-check-presubmit.yaml.tmpl", params, &presubmit); err != nil {
		return nil, fmt.Errorf("could not render config-check presubmit template: %w", err)
	}

	var postsubmit prowconfig.Postsubmit
	if err := renderTemplate("prowgen-postsubmit.yaml.tmpl", params, &postsubmit); err != nil {
		return nil, fmt.Errorf("could not render prowgen postsubmit template: %w", err)
	}

	return &prowconfig.JobConfig{
		PresubmitsStatic: map[string][]prowconfig.Presubmit{
			orgrepo: {presubmit},
		},
		PostsubmitsStatic: map[string][]prowconfig.Postsubmit{
			orgrepo: {postsubmit},
		},
	}, nil
}

func renderTemplate(name string, params bootstrapParams, out any) error {
	var buf bytes.Buffer
	if err := bootstrapTemplates.ExecuteTemplate(&buf, name, params); err != nil {
		return fmt.Errorf("could not execute template %s: %w", name, err)
	}
	if err := yaml.Unmarshal(buf.Bytes(), out); err != nil {
		return fmt.Errorf("could not unmarshal rendered template %s: %w", name, err)
	}
	return nil
}
