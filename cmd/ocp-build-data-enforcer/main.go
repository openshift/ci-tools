package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/openshift/imagebuilder"
	dockercmd "github.com/openshift/imagebuilder/dockerfile/command"
	"github.com/pmezard/go-difflib/difflib"
	"github.com/sirupsen/logrus"
	"golang.org/x/sync/errgroup"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"sigs.k8s.io/yaml"

	"github.com/openshift/ci-tools/pkg/github"
)

type options struct {
	ocpBuildDataRepoDir string
	majorMinor          majorMinor
}

func gatherOptions() *options {
	o := &options{}
	flag.StringVar(&o.ocpBuildDataRepoDir, "ocp-build-data-repo-dir", "../ocp-build-data", "The directory in which the ocp-build-data reposity is")
	flag.StringVar(&o.majorMinor.minor, "minor", "6", "The minor version to target")
	flag.Parse()
	return o
}
func main() {
	opts := gatherOptions()
	opts.majorMinor.major = "4"

	configsUnverified, err := gatherAllOCPImageConfigs(opts.ocpBuildDataRepoDir, opts.majorMinor)
	if err != nil {
		logrus.WithError(err).Fatal("Failed to gather all ocp image configs")
	}

	streamMap, err := readStreamMap(opts.ocpBuildDataRepoDir, opts.majorMinor)
	if err != nil {
		logrus.WithError(err).Fatal("Failed to read streamMap")
	}

	groupYAML, err := readGroupYAML(opts.ocpBuildDataRepoDir, opts.majorMinor)
	if err != nil {
		logrus.WithError(err).Fatal("Faild to read groupYAML")
	}

	var errs []error
	var configs []ocpImageConfig
	for _, cfg := range configsUnverified {
		dereferenced, err := dereferenceConfig(cfg, configsUnverified, streamMap, groupYAML)
		if err != nil {
			errs = append(errs, fmt.Errorf("failed dereferencing config for %s: %w", cfg.SourceFileName, err))
			continue
		}
		if err := dereferenced.validate(); err != nil {
			errs = append(errs, fmt.Errorf("error validating %s: %w", cfg.SourceFileName, err))
			continue
		}
		configs = append(configs, *dereferenced)
	}

	errGroup := &errgroup.Group{}
	for idx := range configs {
		idx := idx
		errGroup.Go(func() error {
			processDockerfile(configs[idx])
			return nil
		})
	}
	if err := errGroup.Wait(); err != nil {
		logrus.WithError(err).Fatal("Processing failed")
	}

	if err := utilerrors.NewAggregate(errs); err != nil {
		for _, err := range err.Errors() {
			logrus.WithError(err).Error("Encountered error")
		}
		logrus.Fatal("Encountered errors")
	}
	logrus.Infof("Processed %d configs", len(configs))
}

// derefenceMember is used to recursively derefenrence .from.member and .from.builder[idx].member
// attributes. Use an idx < 0 to dereference .from.member
func derefenceMember(memberString string, allConfigs map[string]ocpImageConfig, idx int) (string, error) {
	cfgFile := configFileNamberForMemberString(memberString)
	memberConfig, memberConfigExists := allConfigs[cfgFile]
	if !memberConfigExists {
		return "", fmt.Errorf("no configfile %s found", cfgFile)
	}
	member, stream, err := getFromConfigForIndex(memberConfig, idx)
	if err != nil {
		return "", err
	}
	if stream != "" {
		return stream, nil
	}
	if member == "" {
		return "", fmt.Errorf("configfile %s defines neither .from.stream nor .from.member", cfgFile)
	}
	return derefenceMember(memberConfig.From.Member, allConfigs, idx)
}

// getFromConfigForIndex returns the .from{member,stream} attributes. If index < 0, the base image
// config is returned, otherwise the builder image config
func getFromConfigForIndex(cfg ocpImageConfig, idx int) (memberValue, streamValue string, err error) {
	if idx < 0 {
		return cfg.From.Member, cfg.From.Stream, nil
	}
	if n := len(cfg.From.Builder) - 1; n < idx {
		return "", "", fmt.Errorf("file %s has no from.builder.%d element", cfg.SourceFileName, idx)
	}
	return cfg.From.Builder[idx].Member, cfg.From.Builder[idx].Stream, nil
}

func dereferenceConfig(config ocpImageConfig, allConfigs map[string]ocpImageConfig, streamMap streamMap, groupYAML groupYAML) (*ocpImageConfig, error) {
	var errs []error

	if config.From.Member != "" {
		streamName, err := derefenceMember(config.From.Member, allConfigs, -1)
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to dereference config.from.member: %s", err))
		} else {
			config.From.Member = ""
			config.From.Stream = streamName
		}
	}
	for blderIdx := range config.From.Builder {
		if config.From.Builder[blderIdx].Member == "" {
			continue
		}
		streamName, err := derefenceMember(config.From.Builder[blderIdx].Member, allConfigs, blderIdx)
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to dereference config.from.builder.%d: %w", blderIdx, err))
			continue
		}
		config.From.Builder[blderIdx].Stream = streamName
		config.From.Builder[blderIdx].Member = ""
	}

	// No point in continuing here if we had an error, it will just result in useless follow-up errors
	if err := utilerrors.NewAggregate(errs); err != nil {
		return nil, err
	}

	fromStreamReplacement, err := getReplacementFromStream(config.From.Stream, streamMap)
	if err != nil {
		errs = append(errs, fmt.Errorf("failed to replace config.from.stream: %w", err))
	} else {
		config.From.Stream = fromStreamReplacement
	}

	for blderIdx := range config.From.Builder {
		builderReplacement, err := getReplacementFromStream(config.From.Builder[blderIdx].Stream, streamMap)
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to derefence config.from.builder.%d.stream: %w", blderIdx, err))
			continue
		}
		config.From.Builder[blderIdx].Stream = builderReplacement
	}

	if config.Content.Source.Alias != "" {
		replacement, err := replaceContentSourceAlias(config.Content.Source.Alias, groupYAML)
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to replace based on config.content.source.alias: %w", err))
		} else {
			config.Content.Source.Git = replacement
			config.Content.Source.Alias = ""
		}
	}

	return &config, utilerrors.NewAggregate(errs)
}

func configFileNamberForMemberString(memberString string) string {
	return "images/" + memberString + ".yml"
}

func getReplacementFromStream(streamName string, streamMap streamMap) (string, error) {
	if streamName == "" {
		return "", errors.New("stream has no name")
	}
	replacement, hasReplacement := streamMap[streamName]
	if !hasReplacement {
		return "", fmt.Errorf("streammap has no entry for %s", streamName)
	}
	if replacement.UpstreamImage == "" {
		return "", fmt.Errorf("streamMap has no upstream_image for entry %s", streamName)
	}
	return replacement.UpstreamImage, nil
}

func replaceContentSourceAlias(alias string, groupYAML groupYAML) (*ocpImageConfigSourceGit, error) {
	replacement, hasReplacement := groupYAML.Sources[alias]
	if !hasReplacement {
		return nil, fmt.Errorf("group.yaml has no entry for %s", alias)
	}
	ret := &ocpImageConfigSourceGit{}
	*ret = replacement
	return ret, nil
}

func processDockerfile(config ocpImageConfig) {
	orgRepo := config.orgRepo()
	log := logrus.WithField("file", config.SourceFileName).WithField("org/repo", orgRepo)
	split := strings.Split(orgRepo, "/")
	if n := len(split); n != 2 {
		log.Errorf("splitting orgRepo didn't yield 2 but %d results", n)
		return
	}
	if split[0] == "openshift-priv" {
		log.Trace("Ignoring repo in openshift-priv org")
		return
	}
	org, repo := split[0], split[1]
	getter := github.FileGetterFactory(org, repo, "release-4.6")

	log = log.WithField("dockerfile", config.dockerfile())
	data, err := getter(config.dockerfile())
	if err != nil {
		log.WithError(err).Error("Failed to get dockerfile")
		return
	}
	if len(data) == 0 {
		log.Error("dockerfile is empty")
		return
	}

	updated, hasDiff, err := updateDockerfile(data, config)
	if err != nil {
		log.WithError(err).Error("Failed to update Dockerfile")
		return
	}
	if !hasDiff {
		return
	}
	diff := difflib.UnifiedDiff{
		A:        difflib.SplitLines(string(data)),
		B:        difflib.SplitLines(string(updated)),
		FromFile: "original",
		ToFile:   "updated",
		Context:  3,
	}
	diffStr, err := difflib.GetUnifiedDiffString(diff)
	if err != nil {
		log.WithError(err).Error("Failed to construct diff")
	}
	log.Infof("Diff:\n---\n%s\n---\n", diffStr)
}

func readStreamMap(ocpBuildDataDir string, majorMinor majorMinor) (streamMap, error) {
	streamMap := &streamMap{}
	return *streamMap, readYAML(filepath.Join(ocpBuildDataDir, "streams.yml"), streamMap, majorMinor)
}

func readGroupYAML(ocpBuildDataDir string, majorMinor majorMinor) (groupYAML, error) {
	groupYAML := &groupYAML{}
	return *groupYAML, readYAML(filepath.Join(ocpBuildDataDir, "group.yml"), groupYAML, majorMinor)
}

type majorMinor struct{ major, minor string }

func readYAML(path string, unmarshalTarget interface{}, majorMinor majorMinor) error {
	data, err := ioutil.ReadFile(path)
	if err != nil {
		return fmt.Errorf("failed to read %s: %w", path, err)
	}
	data = bytes.ReplaceAll(data, []byte("{MAJOR}"), []byte(majorMinor.major))
	data = bytes.ReplaceAll(data, []byte("{MINOR}"), []byte(majorMinor.minor))
	if err := yaml.Unmarshal(data, unmarshalTarget); err != nil {
		return fmt.Errorf("unmarshaling failed: %w", err)
	}
	return nil
}

func gatherAllOCPImageConfigs(ocpBuildDataDir string, majorMinor majorMinor) (map[string]ocpImageConfig, error) {
	result := map[string]ocpImageConfig{}
	resultLock := &sync.Mutex{}
	errGroup := &errgroup.Group{}

	path := filepath.Join(ocpBuildDataDir, "images")
	if err := filepath.Walk(path, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		errGroup.Go(func() error {
			config := &ocpImageConfig{}
			if err := readYAML(path, config, majorMinor); err != nil {
				return err
			}

			// Distgit only repositories
			if config.Content == nil {
				return nil
			}

			config.SourceFileName = strings.TrimPrefix(path, ocpBuildDataDir+"/")
			resultLock.Lock()
			result[config.SourceFileName] = *config
			resultLock.Unlock()

			return nil
		})

		return nil
	}); err != nil {
		return nil, fmt.Errorf("failed to walk")
	}

	if err := errGroup.Wait(); err != nil {
		return nil, fmt.Errorf("failed to read all files: %w", err)
	}

	return result, nil
}

func updateDockerfile(dockerfile []byte, config ocpImageConfig) ([]byte, bool, error) {
	rootNode, err := imagebuilder.ParseDockerfile(bytes.NewBuffer(dockerfile))
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

	// We don't want to strip off comments so we have to do our own "smart" replacement mechanism because
	// this is the basis for PRs we create on ppls repos and we should keep their comments and whitespaces
	var replacements []dockerFileReplacment
	for stageIdx, stage := range stages {

		for _, child := range stage.Node.Children {
			if child.Value != dockercmd.From {
				continue
			}
			if child.Next == nil {
				return nil, false, fmt.Errorf("dockerfile has FROM directive without value on line %d", child.StartLine)
			}
			if cfgStages[stageIdx] == "" {
				return nil, false, errors.New("replacement target is empty")
			}
			if child.Next.Value != cfgStages[stageIdx] {
				replacements = append(replacements, dockerFileReplacment{
					startLineIndex: child.Next.StartLine,
					from:           []byte(child.Next.Value),
					to:             []byte(cfgStages[stageIdx]),
				})
			}

			// Avoid matching anything after the first from was found, otherwise we match
			// copy --from directives
			break
		}
	}

	var errs []error
	lines := bytes.Split(dockerfile, []byte("\n"))
	for _, replacement := range replacements {
		if n := len(lines); n <= replacement.startLineIndex {
			errs = append(errs, fmt.Errorf("found a replacement for line index %d which is not in the Dockerfile (has %d lines). This is a bug in the replacing tool", replacement.startLineIndex, n))
			continue
		}

		// The Node has an EndLine but its always zero. So we just search forward until we replaced something
		// and error if we couldn't replace anything
		var hasReplaced bool
		for candidateLine := replacement.startLineIndex; candidateLine < len(lines); candidateLine++ {
			if replaced := bytes.Replace(lines[candidateLine], replacement.from, replacement.to, 1); !bytes.Equal(replaced, lines[candidateLine]) {
				hasReplaced = true
				lines[candidateLine] = replaced
				break
			}
		}
		if !hasReplaced {
			errs = append(errs, fmt.Errorf("replacement from %s to %s did not match anything in the following Dockerfile snippet:\n%s. This is a bug in the replacing tool", replacement.from, replacement.to, string(dockerfile[replacement.startLineIndex])))
		}
	}

	return bytes.Join(lines, []byte("\n")), len(replacements) > 0, utilerrors.NewAggregate(errs)
}

type dockerFileReplacment struct {
	startLineIndex int
	from           []byte
	to             []byte
}
