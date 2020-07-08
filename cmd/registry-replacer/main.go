package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/openshift/builder/pkg/build/builder/util/dockerfile"
	"github.com/openshift/imagebuilder"
	"github.com/sirupsen/logrus"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/test-infra/experiment/autobumper/bumper"
	"k8s.io/test-infra/prow/config/secret"
	"k8s.io/test-infra/prow/flagutil"
	"k8s.io/test-infra/prow/github"
	"k8s.io/test-infra/prow/labels"
	"sigs.k8s.io/yaml"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/config"
)

type options struct {
	configDir      string
	createPR       bool
	githubUserName string
	selfApprove    bool
	flagutil.GitHubOptions
}

func gatherOptions() (*options, error) {
	o := &options{}
	o.AddFlags(flag.CommandLine)
	flag.StringVar(&o.configDir, "config-dir", "", "The directory with the ci-operator configs")
	flag.BoolVar(&o.createPR, "create-pr", false, "If the tool should automatically create a PR. Requires --token-file")
	flag.StringVar(&o.githubUserName, "github-user-name", "openshift-bot", "Name of the github user. Required when --create-pr is set. Does nothing otherwise")
	flag.BoolVar(&o.selfApprove, "self-approve", false, "If the bot should self-approve its PR.")
	flag.Parse()

	var errs []error
	if o.configDir == "" {
		errs = append(errs, errors.New("--config-dir is mandatory"))
	}

	if o.createPR {
		if o.githubUserName == "" {
			errs = append(errs, errors.New("--github-user-name was unset, it is required when --create-pr is set"))
		}
		errs = append(errs, o.GitHubOptions.Validate(false))
	}

	return o, utilerrors.NewAggregate(errs)
}

func main() {
	opts, err := gatherOptions()
	if err != nil {
		logrus.WithError(err).Fatal("failed to gather options")
	}

	// Already create the client here if needed to make sure we fail asap if there is an issue
	var githubClient github.Client
	if opts.createPR {
		secretAgent := &secret.Agent{}
		var err error
		githubClient, err = opts.GitHubClient(secretAgent, false)
		if err != nil {
			logrus.WithError(err).Fatal("Failed to construct githubClient")
		}
		if err := secretAgent.Start(nil); err != nil {
			logrus.WithError(err).Fatal("Failed to load github token")
		}
	}

	var errs []error
	errLock := &sync.Mutex{}
	wg := sync.WaitGroup{}
	if err := config.OperateOnCIOperatorConfigDir(
		opts.configDir,
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

	if !opts.createPR {
		return
	}

	if err := upsertPR(githubClient, opts.configDir, opts.githubUserName, opts.TokenPath, opts.selfApprove); err != nil {
		logrus.WithError(err).Fatal("Failed to create PR")
	}
}

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

		for idx := range config.Images {
			foundTags, err := ensureReplacement(&config.Images[idx], githubFileGetterFactory(info.Org, info.Repo, info.Branch))
			if err != nil {
				return fmt.Errorf("failed to ensure replacements: %w", err)
			}
			for _, foundTag := range foundTags {
				if config.BaseImages == nil {
					config.BaseImages = map[string]api.ImageStreamTagReference{}
				}
				if _, exists := config.BaseImages[foundTag.String()]; exists {
					continue
				}
				config.BaseImages[foundTag.String()] = api.ImageStreamTagReference{
					Namespace: foundTag.org,
					Name:      foundTag.repo,
					Tag:       foundTag.tag,
				}
			}
		}

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

	data, err = applyReplacementsToDockerfile(data, image)
	if err != nil {
		return nil, fmt.Errorf("failed to apply replacements to Dockerfile: %w", err)
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

func upsertPR(gc github.Client, dir, githubUsername, tokenFilePath string, selfApprove bool) error {
	if err := os.Chdir(dir); err != nil {
		return fmt.Errorf("failed to chdir into %s: %w", dir, err)
	}

	changed, err := bumper.HasChanges()
	if err != nil {
		return fmt.Errorf("failed to check for changes: %w", err)
	}

	if !changed {
		logrus.Info("No changes, not upserting PR")
		return nil
	}

	token, err := ioutil.ReadFile(tokenFilePath)
	if err != nil {
		return fmt.Errorf("failed to read tokenfile from %s: %w", tokenFilePath, err)
	}

	censor := censor{secret: token}
	stdout := bumper.HideSecretsWriter{Delegate: os.Stdout, Censor: &censor}
	stderr := bumper.HideSecretsWriter{Delegate: os.Stderr, Censor: &censor}

	const targetBranch = "registry-replacer"
	if err := bumper.GitCommitAndPush(
		fmt.Sprintf("https://%s:%s@github.com/%s/release.git", githubUsername, string(token), githubUsername),
		targetBranch,
		githubUsername,
		fmt.Sprintf("%s@users.noreply.github.com", githubUsername),
		"Registry-replacer autocommit",
		stdout,
		stderr,
	); err != nil {
		return fmt.Errorf("failed to push changes: %w", err)
	}

	var labelsToAdd []string
	if selfApprove {
		logrus.Infof("Self-aproving PR by adding the %q and %q labels", labels.Approved, labels.LGTM)
		labelsToAdd = append(labelsToAdd, labels.Approved, labels.LGTM)
	}
	if err := bumper.UpdatePullRequestWithLabels(
		gc,
		"openshift",
		"release",
		prTitle,
		"",
		prTitle,
		githubUsername+":"+targetBranch,
		"master",
		true,
		labelsToAdd,
	); err != nil {
		return fmt.Errorf("failed to create PR: %w", err)
	}

	return nil
}

const prTitle = "Registry-Replacer autoupdate"

type censor struct {
	secret []byte
}

func (c *censor) Censor(data []byte) []byte {
	return bytes.ReplaceAll(data, c.secret, []byte("<< REDACTED >>"))
}

// applyReplacementsToDockerfile duplicates what the build tools would do
func applyReplacementsToDockerfile(in []byte, image *api.ProjectDirectoryImageBuildStepConfiguration) ([]byte, error) {
	node, err := imagebuilder.ParseDockerfile(bytes.NewBuffer(in))
	if err != nil {
		return nil, fmt.Errorf("failed to parse Dockerfile: %w", err)
	}

	if image.From != "" {
		// https://github.com/openshift/builder/blob/6a52122d21e0528fbf014097d70770429fbc4448/pkg/build/builder/common.go#L402
		replaceLastFrom(node, string(image.From), "")
	}

	// We do not need to expand the inputs because they are forced already to point to a
	// base_image which must be in the same cluster.
	return dockerfile.Write(node), nil
}
