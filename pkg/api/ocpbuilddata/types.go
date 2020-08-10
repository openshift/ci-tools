package ocpbuilddata

import (
	"bytes"
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"golang.org/x/sync/errgroup"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"sigs.k8s.io/yaml"
)

type OCPImageConfig struct {
	Content        *OCPImageConfigContent `json:"content"`
	From           OCPImageConfigFrom     `json:"from"`
	Push           OCPImageConfigPush     `json:"push"`
	Name           string                 `json:"name"`
	SourceFileName string                 `json:"-"`
	Version        MajorMinor             `json:"-"`
	PublicRepo     OrgRepo                `json:"-"`
}

func (o OCPImageConfig) validate() error {
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

func (o OCPImageConfig) PromotesTo() string {
	return fmt.Sprintf("registry.svc.ci.openshift.org/ocp/%s.%s:%s", o.Version.Major, o.Version.Minor, strings.TrimPrefix(o.Name, "openshift/ose-"))
}

type OCPImageConfigContent struct {
	Source OCPImageConfigSource `json:"source"`
}

type OrgRepo struct {
	Org  string
	Repo string
}

func (o OrgRepo) String() string {
	return o.Org + "/" + o.Repo
}

type OCPImageConfigSource struct {
	Dockerfile string `json:"dockerfile"`
	Alias      string `json:"alias"`
	Path       string `json:"path"`
	// +Optional, mutually exclusive with alias
	Git *OCPImageConfigSourceGit `json:"git,omitempty"`
}

type OCPImageConfigSourceGit struct {
	URL    string                        `json:"url"`
	Branch OCPImageConfigSourceGitBRanch `json:"branch"`
}

type OCPImageConfigSourceGitBRanch struct {
	Taget string `json:"target"`
}

type OCPImageConfigFrom struct {
	Builder                  []OCPImageConfigFromStream `json:"builder"`
	OCPImageConfigFromStream `json:",inline"`
}

type OCPImageConfigFromStream struct {
	Stream string `json:"stream"`
	Member string `json:"member"`
}

func (icfs OCPImageConfigFromStream) validate() error {
	if icfs.Stream == "" && icfs.Member == "" {
		return errors.New("both stream and member were unset")
	}
	if icfs.Stream != "" && icfs.Member != "" {
		return fmt.Errorf("both stream(%s) and member(%s) were set", icfs.Stream, icfs.Member)
	}
	return nil
}

type OCPImageConfigPush struct {
	Also           []string `json:"also,omitempty"`
	AdditionalTags []string `json:"additional_tags,omitempty"`
}

func (oic *OCPImageConfig) Dockerfile() string {
	if oic.Content.Source.Dockerfile == "" {
		oic.Content.Source.Dockerfile = "Dockerfile"
	}
	return filepath.Join(oic.Content.Source.Path, oic.Content.Source.Dockerfile)
}

func (oic *OCPImageConfig) Stages() ([]string, error) {
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

func (oic *OCPImageConfig) setPublicOrgRepo(mappings []PublicPrivateMapping) {
	var name string
	if oic.Content == nil || oic.Content.Source.Git == nil || oic.Content.Source.Git.URL == "" {
		name = oic.Name
	}
	if name == "" && oic.Content != nil && oic.Content.Source.Git != nil {
		name = strings.TrimSuffix(strings.TrimPrefix(oic.Content.Source.Git.URL, "git@github.com:"), ".git")
	}

	oic.PublicRepo.Org = publicRepo(name, mappings)
	if split := strings.Split(oic.PublicRepo.Org, "/"); len(split) == 2 {
		oic.PublicRepo.Org = split[0]
		oic.PublicRepo.Repo = split[1]
	}
}

type StreamMap map[string]StreamElement

type StreamElement struct {
	Image         string `json:"image"`
	UpstreamImage string `json:"upstream_image"`
}

type GroupYAML struct {
	Sources         map[string]OCPImageConfigSourceGit `json:"sources"`
	PublicUpstreams []PublicPrivateMapping             `json:"public_upstreams,omitempty"`
}

type PublicPrivateMapping struct {
	Private string `json:"private"`
	Public  string `json:"public"`
}

func publicRepo(orgRepo string, mappings []PublicPrivateMapping) string {
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

// LoadImageConfigs loads and dereferences all image configs from the provided ocp-build-data repo root
func LoadImageConfigs(ocpBuildDataDir string, majorMinor MajorMinor) ([]OCPImageConfig, error) {
	configsUnverified, err := gatherAllOCPImageConfigs(ocpBuildDataDir, majorMinor)
	if err != nil {
		return nil, fmt.Errorf("failed to read all image configs: %w", err)
	}
	streamMap, err := readStreamMap(ocpBuildDataDir, majorMinor)
	if err != nil {
		return nil, fmt.Errorf("failed to read streams file: %w", err)
	}

	groupYAML, err := readGroupYAML(ocpBuildDataDir, majorMinor)
	if err != nil {
		return nil, fmt.Errorf("failed to read group file: %w", err)
	}

	var errs []error
	var configs []OCPImageConfig
	for _, cfg := range configsUnverified {
		if err := cfg.validate(); err != nil {
			errs = append(errs, fmt.Errorf("error validating %s: %w", cfg.SourceFileName, err))
			continue
		}
		if err := dereferenceConfig(&cfg, configsUnverified, streamMap, groupYAML); err != nil {
			errs = append(errs, fmt.Errorf("failed dereferencing config for %s: %w", cfg.SourceFileName, err))
			continue
		}
		configs = append(configs, cfg)
	}

	return configs, utilerrors.NewAggregate(errs)
}

func dereferenceConfig(
	config *OCPImageConfig,
	allConfigs map[string]OCPImageConfig,
	streamMap StreamMap,
	groupYAML GroupYAML,
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
		config.From.Stream, err = streamForMember(config.From.Member, allConfigs)
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
			config.From.Builder[blder].Stream, err = streamForMember(config.From.Builder[blder].Member, allConfigs)
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
		config.Content.Source.Git = &OCPImageConfigSourceGit{}
		*config.Content.Source.Git = groupYAML.Sources[config.Content.Source.Alias]
	}

	config.setPublicOrgRepo(groupYAML.PublicUpstreams)

	return utilerrors.NewAggregate(errs)
}

func replaceStream(streamName string, streamMap StreamMap) (string, error) {
	replacement, hasReplacement := streamMap[streamName]
	if !hasReplacement {
		return "", fmt.Errorf("streamMap has no replacement for stream %s", streamName)
	}
	if replacement.UpstreamImage == "" {
		return "", fmt.Errorf("stream.yml.%s.upstream_image is an empty string", streamName)
	}
	return replacement.UpstreamImage, nil
}

func configFileNamberForMemberString(memberString string) string {
	return "images/" + memberString + ".yml"
}

func streamForMember(
	memberName string,
	allConfigs map[string]OCPImageConfig,
) (string, error) {
	cfgFile := configFileNamberForMemberString(memberName)
	cfg, cfgExists := allConfigs[cfgFile]
	if !cfgExists {
		return "", fmt.Errorf("no config %s found", cfgFile)
	}
	return cfg.PromotesTo(), nil
}

func readStreamMap(ocpBuildDataDir string, majorMinor MajorMinor) (StreamMap, error) {
	streamMap := StreamMap{}
	return streamMap, readYAML(filepath.Join(ocpBuildDataDir, "streams.yml"), &streamMap, majorMinor)
}

func readGroupYAML(ocpBuildDataDir string, majorMinor MajorMinor) (GroupYAML, error) {
	groupYAML := GroupYAML{}
	return groupYAML, readYAML(filepath.Join(ocpBuildDataDir, "group.yml"), &groupYAML, majorMinor)
}

type MajorMinor struct {
	Major string
	Minor string
}

func (mm MajorMinor) String() string {
	return mm.Major + "." + mm.Minor
}

func gatherAllOCPImageConfigs(ocpBuildDataDir string, majorMinor MajorMinor) (map[string]OCPImageConfig, error) {
	result := map[string]OCPImageConfig{}
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
			config := OCPImageConfig{}
			if err := readYAML(path, &config, majorMinor); err != nil {
				return err
			}

			// Distgit only repositories
			if config.Content == nil {
				return nil
			}

			config.SourceFileName = strings.TrimPrefix(path, ocpBuildDataDir+"/")
			config.Version = majorMinor
			resultLock.Lock()
			result[config.SourceFileName] = config
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

func readYAML(path string, unmarshalTarget interface{}, majorMinor MajorMinor) error {
	data, err := ioutil.ReadFile(path)
	if err != nil {
		return fmt.Errorf("failed to read %s: %w", path, err)
	}
	data = bytes.ReplaceAll(data, []byte("{MAJOR}"), []byte(majorMinor.Major))
	data = bytes.ReplaceAll(data, []byte("{MINOR}"), []byte(majorMinor.Minor))
	if err := yaml.Unmarshal(data, unmarshalTarget); err != nil {
		return fmt.Errorf("unmarshaling failed: %w", err)
	}
	return nil
}
