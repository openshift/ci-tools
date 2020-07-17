package main

import (
	"bytes"
	"fmt"

	"github.com/openshift/builder/pkg/build/builder/util/dockerfile"
	"github.com/openshift/imagebuilder"
	dockercmd "github.com/openshift/imagebuilder/dockerfile/command"
)

func main() {
	fmt.Println("vim-go")
}

type ocpImageConfig struct {
	Content        ocpImageConfigContent `json:"content"`
	From           ocpImageConfigFrom    `json:"from"`
	SourceFileName string                `json:"-"`
}

type ocpImageConfigContent struct {
	Source ocpImageConfigSource `json:"source"`
}

type ocpImageConfigSource struct {
	Dockerfile string `json:"dockerfile"`
}

type ocpImageConfigFrom struct {
	Builder []ocpImageConfigFromStream `json:"builder"`
	Stream  ocpImageConfigFromStream   `json:"stream"`
}

type ocpImageConfigFromStream struct {
	Stream string `json:"stream"`
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
	return append(result, oic.From.Stream.Stream)
}

func updateDockerfile(initialDockerfile []byte, config ocpImageConfig) ([]byte, bool, error) {
	rootNode, err := imagebuilder.ParseDockerfile(bytes.NewBuffer(initialDockerfile))
	if err != nil {
		return nil, false, fmt.Errorf("failed to parse Dockerfile: %w", err)
	}

	stages, err := imagebuilder.NewStages(rootNode, imagebuilder.NewBuilder(nil))
	if err != nil {
		return nil, false, fmt.Errorf("failed to construct imagebuilder stages: %w", err)
	}

	cfgStages := config.stages()
	if expected := len(cfgStages); expected != len(stages) {
		return nil, false, fmt.Errorf("expected %d stages based on ocp config %s but got %d", expected, config.SourceFileName, len(stages))
	}

	// The parsing strips off comments so we have to track this manually
	var hasChanges bool
	for stageIdx, stage := range stages {
		for _, child := range stage.Node.Children {
			if child.Value != dockercmd.From {
				continue
			}
			if child.Next == nil {
				return nil, false, fmt.Errorf("dockerfile has FROM directive without value on line %d", child.StartLine)
			}
			if child.Next.Value != cfgStages[stageIdx] {
				hasChanges = true
				child.Next.Value = cfgStages[stageIdx]
			}
		}
	}

	updatedDockerfile := dockerfile.Write(rootNode)
	return updatedDockerfile, hasChanges, nil
}
