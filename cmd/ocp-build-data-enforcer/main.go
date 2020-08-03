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
	"time"

	"github.com/openshift/imagebuilder"
	dockercmd "github.com/openshift/imagebuilder/dockerfile/command"
	"github.com/pmezard/go-difflib/difflib"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/sirupsen/logrus"
	"golang.org/x/sync/errgroup"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"sigs.k8s.io/yaml"

	"github.com/openshift/ci-tools/pkg/github"
	"github.com/openshift/ci-tools/pkg/load/agents"
)

type options struct {
	ocpBuildDataRepoDir string
	configDir           string
	majorMinor          majorMinor
}

func gatherOptions() *options {
	o := &options{}
	flag.StringVar(&o.ocpBuildDataRepoDir, "ocp-build-data-repo-dir", "../ocp-build-data", "The directory in which the ocp-build-data reposity is")
	flag.StringVar(&o.configDir, "config-dir", "../release/ci-operator/config", "The CI-Operator config directory")
	flag.StringVar(&o.majorMinor.minor, "minor", "6", "The minor version to target")
	flag.Parse()
	return o
}
func main() {
	opts := gatherOptions()
	opts.majorMinor.major = "4"

	configAgent, err := agents.NewConfigAgent(opts.configDir, 2*time.Minute, prometheus.NewCounterVec(prometheus.CounterOpts{}, []string{"error"}))
	if err != nil {
		logrus.WithError(err).Fatal("Failed to construct config agent")
	}
	pullSpecForOrgRepoBranchDockerfileGetter, err := pullSpecForOrgRepoBranchDockerfileFactory(configAgent)
	if err != nil {
		logrus.WithError(err).Fatal("Failed to get pullSpecForOrgRepoBranchDockerfileGetter")
	}

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
		if err := cfg.validate(); err != nil {
			errs = append(errs, fmt.Errorf("error validating %s: %w", cfg.SourceFileName, err))
			continue
		}
		if err := dereferenceConfig(&cfg, fmt.Sprintf("release-%s.%s", opts.majorMinor.major, opts.majorMinor.minor), configsUnverified, streamMap, groupYAML, pullSpecForOrgRepoBranchDockerfileGetter); err != nil {
			errs = append(errs, fmt.Errorf("failed dereferencing config for %s: %w", cfg.SourceFileName, err))
			continue
		}
		configs = append(configs, cfg)
	}

	// No point in continuing, that will only result in weird to understand follow-up errors
	if err := utilerrors.NewAggregate(errs); err != nil {
		for _, err := range err.Errors() {
			logrus.WithError(err).Error("Encountered error")
		}
		logrus.Fatal("Encountered errors")
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

func dereferenceConfig(
	config *ocpImageConfig,
	branch string,
	allConfigs map[string]ocpImageConfig,
	streamMap streamMap,
	groupYAML groupYAML,
	pullSpecGetter pullSpecForOrgRepoBranchDockerfileGetter,
) error {
	var errs []error

	var err error
	if config.From.Stream != "" {
		config.From.Stream, err = replaceStream(config.From.Stream, streamMap)
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to replace .from.stream: %w", err))
		}
	}
	if config.From.Member != "" {
		config.From.Stream, err = streamForMember(config.From.Member, branch, allConfigs, groupYAML, pullSpecGetter)
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to replace .from.member: %w", err))
		}
		config.From.Member = ""
	}
	if config.From.Stream == "" {
		errs = append(errs, errors.New("failed to find replacement for .from.stream"))
	}

	for blder := range config.From.Builder {
		if config.From.Builder[blder].Stream != "" {
			config.From.Builder[blder].Stream, err = replaceStream(config.From.Builder[blder].Stream, streamMap)
			if err != nil {
				errs = append(errs, fmt.Errorf("failed to replace .from.%d.stream: %w", blder, err))
			}
		}
		if config.From.Builder[blder].Member != "" {
			config.From.Builder[blder].Stream, err = streamForMember(config.From.Builder[blder].Member, branch, allConfigs, groupYAML, pullSpecGetter)
			if err != nil {
				errs = append(errs, fmt.Errorf("failed to replace .from.%d.member: %w", blder, err))
			}
			config.From.Builder[blder].Member = ""
		}
		if config.From.Builder[blder].Stream == "" {
			errs = append(errs, fmt.Errorf("failed to dereference from.builder.%d", blder))
		}
	}

	if config.Content.Source.Alias != "" {
		if _, hasReplacement := groupYAML.Sources[config.Content.Source.Alias]; !hasReplacement {
			return fmt.Errorf("groups.yaml has no replacement for alias %s", config.Content.Source.Alias)
		}
		// Create a new pointer and set its value to groupYAML.Sources[config.Content.Source.Alias]
		// rather than directly creating a pointer to the latter.
		config.Content.Source.Git = &ocpImageConfigSourceGit{}
		*config.Content.Source.Git = groupYAML.Sources[config.Content.Source.Alias]
	}

	return utilerrors.NewAggregate(errs)
}

func streamForMember(
	memberName string,
	branch string,
	allConfigs map[string]ocpImageConfig,
	groupYAML groupYAML,
	pullSpecGetter pullSpecForOrgRepoBranchDockerfileGetter,
) (string, error) {
	cfgFile := configFileNamberForMemberString(memberName)
	cfg, cfgExists := allConfigs[cfgFile]
	if !cfgExists {
		return "", fmt.Errorf("no config %s found", cfgFile)
	}
	orgRepo, err := getPublicRepo(cfg.orgRepo(), groupYAML.PublicUpstreams)
	if err != nil {
		return "", fmt.Errorf("failed to replace %s with its public upstream: %w", cfg.orgRepo(), err)
	}
	orgRepoSplit := strings.Split(orgRepo, "/")
	if n := len(orgRepoSplit); n != 2 {
		return "", fmt.Errorf("splitting orgRepo string %s by / did not yield two but %d results. %s", orgRepo, n, cfg.orgRepo())
	}
	result, err := pullSpecGetter(orgRepoSplit[0], orgRepoSplit[1], branch, cfg.dockerfile())
	if err != nil {
		return "", fmt.Errorf("failed to get pullspec for promotiontarget for %s/%s#%s:%s: %w", orgRepoSplit[0], orgRepoSplit[1], branch, cfg.dockerfile(), err)
	}

	return result, nil
}

func configFileNamberForMemberString(memberString string) string {
	return "images/" + memberString + ".yml"
}

func getPublicRepo(orgRepo string, mappings []publicPrivateMapping) (string, error) {
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
		return strings.TrimPrefix(orgRepo, "https://github.com/"), nil
	}

	return strings.TrimPrefix(strings.Replace(orgRepo, replacementFrom, replacementTo, 1), "https://github.com/"), nil
}

func replaceStream(streamName string, streamMap streamMap) (string, error) {
	replacement, hasReplacement := streamMap[streamName]
	if !hasReplacement {
		return "", fmt.Errorf("streamMap has no replacement for stream %s", streamName)
	}
	if replacement.UpstreamImage == "" {
		return "", fmt.Errorf("stream.yml.%s.upstream_image is an empty string", streamName)
	}
	return replacement.UpstreamImage, nil
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
	streamMap := streamMap{}
	return streamMap, readYAML(filepath.Join(ocpBuildDataDir, "streams.yml"), &streamMap, majorMinor)
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

	cfgStages, err := config.stages()
	if err != nil {
		return nil, false, fmt.Errorf("failed to get stages: %w", err)
	}
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
				return nil, false, errors.New("")
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
