package main

import (
	"bytes"
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"

	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/openshift/ci-tools/pkg/config"
)

// updatePipelineControllerConfig persists new release branches alongside existing pipeline settings.
func updatePipelineControllerConfig(path string, outputs []config.DataWithInfo) error {
	if len(outputs) == 0 {
		return nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading pipeline-controller config %s: %w", path, err)
	}
	updated, changed, err := addPipelineBranches(raw, outputs)
	if err != nil {
		return fmt.Errorf("updating pipeline-controller config %s: %w", path, err)
	}
	if !changed {
		return nil
	}
	if err := os.WriteFile(path, updated, 0664); err != nil {
		return fmt.Errorf("writing pipeline-controller config %s: %w", path, err)
	}
	return nil
}

// addPipelineBranches extends explicit branch lists of repos already protected by the controller.
func addPipelineBranches(raw []byte, outputs []config.DataWithInfo) ([]byte, bool, error) {
	branchesByRepo := map[string]sets.Set[string]{}
	for _, output := range outputs {
		if !strings.HasPrefix(output.Info.Branch, "release-") {
			continue
		}
		key := output.Info.Org + "/" + output.Info.Repo
		if branchesByRepo[key] == nil {
			branchesByRepo[key] = sets.New[string]()
		}
		branchesByRepo[key].Insert(output.Info.Branch)
	}
	if len(branchesByRepo) == 0 {
		return raw, false, nil
	}

	var document yaml.Node
	if err := yaml.Unmarshal(raw, &document); err != nil {
		return nil, false, fmt.Errorf("parsing yaml: %w", err)
	}
	if len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return nil, false, fmt.Errorf("expected a pipeline-controller config mapping")
	}
	orgs := mappingValue(document.Content[0], "orgs")
	if orgs == nil || orgs.Kind != yaml.SequenceNode {
		return nil, false, fmt.Errorf("expected orgs to be a sequence")
	}

	changed := false
	for _, org := range orgs.Content {
		if org.Kind != yaml.MappingNode {
			return nil, false, fmt.Errorf("expected an org mapping")
		}
		name := mappingValue(org, "org")
		repos := mappingValue(org, "repos")
		if name == nil || name.Kind != yaml.ScalarNode || repos == nil || repos.Kind != yaml.SequenceNode {
			return nil, false, fmt.Errorf("expected an org name and repo sequence")
		}
		for _, repo := range repos.Content {
			if repo.Kind == yaml.ScalarNode {
				continue
			}
			if repo.Kind != yaml.MappingNode {
				return nil, false, fmt.Errorf("expected a repo name or mapping")
			}
			repoName := mappingValue(repo, "name")
			if repoName == nil || repoName.Kind != yaml.ScalarNode {
				return nil, false, fmt.Errorf("expected a repo name")
			}
			key := name.Value + "/" + repoName.Value
			branches := branchesByRepo[key]
			if name.Value == "openshift-priv" && branches == nil {
				branches = branchesByRepo["openshift/"+repoName.Value]
			}
			branchList := mappingValue(repo, "branches")
			if branches == nil || branchList == nil {
				continue
			}
			if branchList.Kind != yaml.SequenceNode {
				return nil, false, fmt.Errorf("expected branches of %s to be a sequence", key)
			}
			for _, branch := range sets.List(branches) {
				if insertPipelineBranch(branchList, branch) {
					changed = true
				}
			}
		}
	}
	if !changed {
		return raw, false, nil
	}
	var buffer bytes.Buffer
	encoder := yaml.NewEncoder(&buffer)
	encoder.SetIndent(2)
	if err := encoder.Encode(&document); err != nil {
		return nil, false, fmt.Errorf("marshaling yaml: %w", err)
	}
	return buffer.Bytes(), true, nil
}

// mappingValue returns the value associated with a YAML mapping key, if present.
func mappingValue(node *yaml.Node, key string) *yaml.Node {
	for index := 0; index+1 < len(node.Content); index += 2 {
		if node.Content[index].Value == key {
			return node.Content[index+1]
		}
	}
	return nil
}

// insertPipelineBranch adds a branch in lexical order without disturbing existing entries.
func insertPipelineBranch(branches *yaml.Node, branch string) bool {
	insertionIndex := len(branches.Content)
	for index, existing := range branches.Content {
		if existing.Value == branch {
			return false
		}
		if existing.Value > branch && insertionIndex == len(branches.Content) {
			insertionIndex = index
		}
	}
	branches.Content = append(branches.Content, nil)
	copy(branches.Content[insertionIndex+1:], branches.Content[insertionIndex:])
	branches.Content[insertionIndex] = &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: branch}
	return true
}
