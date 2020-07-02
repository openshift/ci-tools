package main

import (
	"bytes"
	"flag"
	"fmt"
	"io/ioutil"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/sirupsen/logrus"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/yaml"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/config"
)

func main() {
	configDir := flag.String("config-dir", "", "The directory with the ci-operator configs")
	flag.Parse()
	if *configDir == "" {
		logrus.Fatal("--config-dir arg is required")
	}

	var errs []error
	errLock := &sync.Mutex{}
	wg := sync.WaitGroup{}
	if err := config.OperateOnCIOperatorConfigDir(
		*configDir,
		func(config *api.ReleaseBuildConfiguration, info *config.Info) error {
			wg.Add(1)
			go func(filename string) {
				defer wg.Done()
				if err := replacer(
					githubFileGetterFactory,
					func(data []byte) error {
						return ioutil.WriteFile(filename, data, 0644)
					})(config, info); err != nil {
					errLock.Lock()
					errs = append(errs, err)
					errLock.Unlock()
				}
			}(info.Filename)
			return nil
		},
	); err != nil {
		logrus.WithError(err).Fatal("Failed to operate on ci-operator-config")
	}
	wg.Wait()
	if err := utilerrors.NewAggregate(errs); err != nil {
		logrus.WithError(err).Fatal("Encountered errors")
	}
}

var replacementCandidateMatchRegex = regexp.MustCompile(".+_.+_.+")

// replacer ensures replace directives are in place. It fetches the files via http because using git
// en masse easily kills a developer laptop whereas the http calls are cheap and can be parallelized without
// bounds.
func replacer(
	githubFileGetterFactory func(org, repo, branch string) githubFileGetter,
	writer func([]byte) error,
) func(*api.ReleaseBuildConfiguration, *config.Info) error {
	return func(config *api.ReleaseBuildConfiguration, info *config.Info) error {
		if len(config.Images) == 0 {
			return nil
		}

		originalConfig, err := yaml.Marshal(config)
		if err != nil {
			return fmt.Errorf("failed to marshal config for comparison: %w", err)
		}

		deletedBases := sets.NewString()
		for idx := range config.Images {
			for input := range config.Images[idx].Inputs {
				if !replacementCandidateMatchRegex.MatchString(input) {
					continue
				}
				for _, str := range config.Images[idx].Inputs[input].As {
					if !strings.Contains(str, "registry.svc.ci.openshift.org") {
						continue
					}
				}
				delete(config.Images[idx].Inputs, input)
				deletedBases.Insert(input)
			}
		}

		for baseImage := range config.BaseImages {
			if deletedBases.Has(baseImage) {
				delete(config.BaseImages, baseImage)
			}
		}

		//		for idx := range config.Images {
		//			foundTags, err := ensureReplacement(&config.Images[idx], githubFileGetterFactory(info.Org, info.Repo, info.Branch))
		//			if err != nil {
		//				return fmt.Errorf("failed to ensure replacements: %w", err)
		//			}
		//			for _, foundTag := range foundTags {
		//				if config.BaseImages == nil {
		//					config.BaseImages = map[string]api.ImageStreamTagReference{}
		//				}
		//				if _, exists := config.BaseImages[foundTag.String()]; exists {
		//					continue
		//				}
		//				config.BaseImages[foundTag.String()] = api.ImageStreamTagReference{
		//					Namespace: foundTag.org,
		//					Name:      foundTag.repo,
		//					Tag:       foundTag.tag,
		//				}
		//			}
		//		}

		newConfig, err := yaml.Marshal(config)
		if err != nil {
			return fmt.Errorf("failed to marshal new config: %w", err)
		}

		// Avoid filesystem access if possible
		if bytes.Equal(originalConfig, newConfig) {
			return nil
		}

		if err := writer(newConfig); err != nil {
			return fmt.Errorf("faild to write %s: %w", info.Filename, err)
		}

		return nil
	}
}

var registryRegex = regexp.MustCompile(`registry\.svc\.ci\.openshift\.org\/[^\s]+`)

type orgRepoTag struct{ org, repo, tag string }

func (ort orgRepoTag) String() string {
	return ort.org + "_" + ort.repo + "_" + ort.tag
}

func ensureReplacement(image *api.ProjectDirectoryImageBuildStepConfiguration, getter githubFileGetter) ([]orgRepoTag, error) {
	dockerFilePath := "Dockerfile"
	if image.DockerfilePath != "" {
		dockerFilePath = image.DockerfilePath
	}

	data, err := getter(filepath.Join(image.ContextDir, dockerFilePath))
	if err != nil {
		return nil, fmt.Errorf("failed to get dockerfile %s: %w", image.DockerfilePath, err)
	}

	var toReplace []string
	for _, line := range bytes.Split(data, []byte("\n")) {
		if !bytes.Contains(line, []byte("FROM")) {
			continue
		}
		if !bytes.Contains(line, []byte("registry.svc.ci.openshift.org")) {
			continue
		}
		match := registryRegex.Find(line)
		if match == nil {
			continue
		}

		toReplace = append(toReplace, string(match))
	}

	var result []orgRepoTag
	for _, toReplace := range toReplace {
		orgRepoTag, err := orgRepoTagFromPullString(toReplace)
		if err != nil {
			return nil, fmt.Errorf("failed to parse string %s as pullspec: %w", toReplace, err)
		}

		// Assume ppl know what they are doing
		if hasReplacementFor(image, toReplace) {
			continue
		}

		if image.Inputs == nil {
			image.Inputs = map[string]api.ImageBuildInputs{}
		}
		inputs := image.Inputs[orgRepoTag.String()]
		inputs.As = sets.NewString(inputs.As...).Insert(toReplace).List()
		image.Inputs[orgRepoTag.String()] = inputs

		result = append(result, orgRepoTag)
	}

	return result, nil
}

func hasReplacementFor(image *api.ProjectDirectoryImageBuildStepConfiguration, target string) bool {
	for _, input := range image.Inputs {
		if sets.NewString(input.As...).Has(target) {
			return true
		}
	}

	return false
}

func orgRepoTagFromPullString(pullString string) (orgRepoTag, error) {
	res := orgRepoTag{}
	slashSplit := strings.Split(pullString, "/")
	if n := len(slashSplit); n != 3 {
		return res, fmt.Errorf("expected three elements when splitting string %q by '/', got %d", pullString, n)
	}
	res.org = slashSplit[1]
	if repoTag := strings.Split(slashSplit[2], ":"); len(repoTag) == 2 {
		res.repo = repoTag[0]
		res.tag = repoTag[1]
	} else {
		res.repo = slashSplit[2]
		res.tag = "latest"
	}

	return res, nil
}

type githubFileGetter func(path string) ([]byte, error)

func githubFileGetterFactory(org, repo, branch string) githubFileGetter {
	return func(path string) ([]byte, error) {
		url := fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/%s/%s", org, repo, branch, path)
		resp, err := http.DefaultClient.Get(url)
		if err != nil {
			return nil, fmt.Errorf("failed to GET %s: %w", url, err)
		}
		if resp.StatusCode == http.StatusNotFound {
			return nil, nil
		}
		body, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("failed to read response body: %w", err)
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("got unexpected http status code %d, response body: %s", resp.StatusCode, string(body))
		}
		return body, nil
	}
}
