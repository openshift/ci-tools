package main

import (
	"bytes"
	"embed"
	"fmt"
	"strings"
	"text/template"

	"github.com/sirupsen/logrus"
	"sigs.k8s.io/yaml"

	prowconfig "sigs.k8s.io/prow/pkg/config"

	cioperatorapi "github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/config"
	jc "github.com/openshift/ci-tools/pkg/jobconfig"
	"github.com/openshift/ci-tools/pkg/prowgen"
)

const (
	releaseRepoOrg     = "openshift"
	releaseRepoName    = "release"
	clusterProfilesCfg = "cluster-profiles/cluster-profiles-config.yaml"
)

//go:embed templates
var templateFS embed.FS

var bootstrapTemplates = template.Must(
	template.ParseFS(templateFS, "templates/*.yaml.tmpl"),
)

// --- Config fetching ---

func (s *server) generateAllJobs(org, repo, branch, sha string, logger *logrus.Entry) (*prowconfig.JobConfig, bool, error) {
	configs, useDir, err := s.fetchConfigs(org, repo, sha, logger)
	if err != nil {
		return nil, false, err
	}
	if len(configs) == 0 {
		return nil, false, nil
	}

	allJobs := &prowconfig.JobConfig{
		PresubmitsStatic:  map[string][]prowconfig.Presubmit{},
		PostsubmitsStatic: map[string][]prowconfig.Postsubmit{},
	}
	for filename, configSpec := range configs {
		info := metadataFromFilename(filename, org, repo, branch)
		configSpec.UnresolvedConfigPath = cioperatorapi.CIOperatorInrepoConfigFileName
		generated, err := prowgen.GenerateJobs(configSpec, info, nil)
		if err != nil {
			return nil, false, fmt.Errorf("prowgen failed for %s: %w", filename, err)
		}
		jc.Append(allJobs, generated)
	}
	return allJobs, useDir, nil
}

func (s *server) fetchConfigs(org, repo, sha string, l *logrus.Entry) (map[string]*cioperatorapi.ReleaseBuildConfiguration, bool, error) {
	entries, err := s.ghc.GetDirectory(org, repo, ciOperatorDir, sha)
	if err != nil {
		configs, err := s.fetchSingleConfig(org, repo, sha)
		return configs, false, err
	}

	configs := map[string]*cioperatorapi.ReleaseBuildConfiguration{}
	for _, entry := range entries {
		if entry.Type != "file" {
			continue
		}
		if !strings.HasSuffix(entry.Name, ".yaml") && !strings.HasSuffix(entry.Name, ".yml") {
			continue
		}
		if !strings.HasPrefix(entry.Name, "ci-operator") {
			continue
		}

		content, err := s.ghc.GetFile(org, repo, entry.Path, sha)
		if err != nil {
			return nil, false, fmt.Errorf("could not fetch %s: %w", entry.Path, err)
		}
		if content == nil {
			l.WithField("file", entry.Path).Warn("file not found")
			continue
		}

		var cfg cioperatorapi.ReleaseBuildConfiguration
		if err := yaml.Unmarshal(content, &cfg); err != nil {
			return nil, false, fmt.Errorf("could not parse %s: %w", entry.Path, err)
		}
		configs[entry.Name] = &cfg
	}
	return configs, true, nil
}

func (s *server) fetchSingleConfig(org, repo, sha string) (map[string]*cioperatorapi.ReleaseBuildConfiguration, error) {
	const singleFile = ".ci-operator.yaml"
	content, err := s.ghc.GetFile(org, repo, singleFile, sha)
	if err != nil {
		return nil, fmt.Errorf("could not fetch %s: %w", singleFile, err)
	}
	if content == nil {
		return nil, nil
	}

	var cfg cioperatorapi.ReleaseBuildConfiguration
	if err := yaml.Unmarshal(content, &cfg); err != nil {
		return nil, fmt.Errorf("could not parse %s: %w", singleFile, err)
	}
	return map[string]*cioperatorapi.ReleaseBuildConfiguration{
		"ci-operator.yaml": &cfg,
	}, nil
}

// --- Bootstrap job generation ---

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
