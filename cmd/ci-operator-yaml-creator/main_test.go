package main

import (
	"io/fs"
	"os"
	"testing"

	"github.com/google/go-cmp/cmp"

	"k8s.io/test-infra/prow/git/localgit"
	"sigs.k8s.io/yaml"

	cioperatorapi "github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/config"
	"github.com/openshift/ci-tools/pkg/github"
)

func TestProcessing(t *testing.T) {
	type processInput struct {
		filter         func(*config.Info) bool
		cfg            *cioperatorapi.ReleaseBuildConfiguration
		metadata       *config.Info
		ciOperatorYaml string
	}

	testCases := []struct {
		name        string
		inputModify func(*processInput)

		// Must only be set when a PR is expected
		expectedUpdatedCiOperatorYaml cioperatorapi.CIOperatorInrepoConfig
		expectedUpdatedReleaseRepoCfg string
	}{

		{
			name: "PR is created",
			expectedUpdatedCiOperatorYaml: cioperatorapi.CIOperatorInrepoConfig{
				BuildRootImage: cioperatorapi.ImageStreamTagReference{
					Namespace: "namespace",
					Name:      "name",
					Tag:       "tag",
				},
			},
		},
		{
			name: "Filter filters out",
			inputModify: func(p *processInput) {
				p.filter = func(*config.Info) bool { return false }
			},
		},
		{
			name: "From repository already set, nothing to do",
			inputModify: func(p *processInput) {
				p.cfg.BuildRootImage = &cioperatorapi.BuildRootImageConfiguration{FromRepository: true}
			},
		},
		{
			name: "Branch not master, nothing to do",
			inputModify: func(p *processInput) {
				p.metadata.Branch = "release-4.0"
			},
		},
		{
			name: "Variant is set, nothing to do",
			inputModify: func(p *processInput) {
				p.metadata.Variant = "OKD"
			},
		},
		{
			name: ".ci-operator.yaml already correct, build_root.from_repository gets set to true",
			inputModify: func(p *processInput) {
				p.ciOperatorYaml = `
build_root_image:
  namespace: namespace
  name: name
  tag: tag
`
			},
			expectedUpdatedReleaseRepoCfg: `build_root:
  from_repository: true
zz_generated_metadata:
  branch: ""
  org: ""
  repo: ""
`,
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			input := processInput{
				filter: func(*config.Info) bool { return true },
				cfg: &cioperatorapi.ReleaseBuildConfiguration{InputConfiguration: cioperatorapi.InputConfiguration{
					BuildRootImage: &cioperatorapi.BuildRootImageConfiguration{ImageStreamTagReference: &cioperatorapi.ImageStreamTagReference{
						Namespace: "namespace", Name: "name", Tag: "tag",
					}},
				}},
				metadata: &config.Info{Filename: "ci-operator-config.yaml", Metadata: cioperatorapi.Metadata{Org: "org", Repo: "repo", Branch: "master"}},
			}

			if tc.inputModify != nil {
				tc.inputModify(&input)
			}

			localgit, clients, err := localgit.NewV2()
			if err != nil {
				t.Fatalf("failed to create localgit: %v", err)
			}
			localgit.InitialBranch = "master"
			defer func() {
				if err := localgit.Clean(); err != nil {
					t.Errorf("localgit cleanup failed: %v", err)
				}
			}()

			if err := localgit.MakeFakeRepo("org", "repo"); err != nil {
				t.Fatalf("makeFakeRepo: %v", err)
			}

			repoFileGetter := func(org, repo, branch string, _ ...github.Opt) github.FileGetter {
				if org != input.metadata.Org {
					t.Errorf("expected org to be %s, was %s", input.metadata.Org, org)
				}
				if repo != input.metadata.Repo {
					t.Errorf("expected repo to be %s, was %s", input.metadata.Repo, repo)
				}
				if branch != input.metadata.Branch {
					t.Errorf("expected branch to be %s, was %s", input.metadata.Branch, branch)
				}
				return func(path string) ([]byte, error) {
					if path != cioperatorapi.CIOperatorInrepoConfigFileName {
						t.Errorf("filename in github filegetter wasn't %s but %s", cioperatorapi.CIOperatorInrepoConfigFileName, path)
					}
					return []byte(input.ciOperatorYaml), nil
				}
			}

			var updatedReleaseRepoConfig string
			writeFile := func(filename string, data []byte, _ fs.FileMode) error {
				if filename != input.metadata.Filename {
					t.Errorf("expected %s as filename when updating the release repo config, but was %s", input.metadata.Filename, filename)
				}
				updatedReleaseRepoConfig = string(data)
				return nil
			}

			var updatedCIOperatorYaml cioperatorapi.CIOperatorInrepoConfig
			createPr := func(localSourceDir, org, repo, targetBranch string) error {
				if org != input.metadata.Org {
					t.Errorf("expected org to be %s, was %s", input.metadata.Org, org)
				}
				if repo != input.metadata.Repo {
					t.Errorf("expected repo to be %s, was %s", input.metadata.Repo, repo)
				}
				if targetBranch != input.metadata.Branch {
					t.Errorf("expected branch to be %s, was %s", input.metadata.Branch, targetBranch)
				}
				raw, err := os.ReadFile(localSourceDir + "/" + cioperatorapi.CIOperatorInrepoConfigFileName)
				if err != nil {
					t.Fatalf("failed to read .ci-operator.yaml: %v", err)
				}
				if err := yaml.Unmarshal(raw, &updatedCIOperatorYaml); err != nil {
					t.Errorf("failed to unmarshal updated .ci-operator.yaml: %v", err)
				}
				return nil
			}

			if err := process(input.filter, repoFileGetter, writeFile, clients, 99, createPr)(input.cfg, input.metadata); err != nil {
				t.Fatalf("process failed: %v", err)
			}

			if diff := cmp.Diff(tc.expectedUpdatedCiOperatorYaml, updatedCIOperatorYaml); diff != "" {
				t.Errorf("expected updated .ci-operator.yaml differs from actual: %s", diff)
			}
			if diff := cmp.Diff(tc.expectedUpdatedReleaseRepoCfg, updatedReleaseRepoConfig); diff != "" {
				t.Errorf("expected updated release repo config differs from actual: %s", diff)
			}

		})
	}
}
