package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/sirupsen/logrus"
	"golang.org/x/sync/semaphore"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/prow/cmd/generic-autobumper/bumper"
	"sigs.k8s.io/prow/pkg/config/secret"
	"sigs.k8s.io/prow/pkg/flagutil"
	pgithub "sigs.k8s.io/prow/pkg/github"
	"sigs.k8s.io/prow/pkg/labels"
	"sigs.k8s.io/yaml"

	"github.com/openshift/builder/pkg/build/builder/util/dockerfile"
	"github.com/openshift/imagebuilder"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/api/ocpbuilddata"
	"github.com/openshift/ci-tools/pkg/config"
	cidockerfile "github.com/openshift/ci-tools/pkg/dockerfile"
	"github.com/openshift/ci-tools/pkg/github"
	"github.com/openshift/ci-tools/pkg/load"
	"github.com/openshift/ci-tools/pkg/registry"
	"github.com/openshift/ci-tools/pkg/steps/release"
)

type options struct {
	configDir                                    string
	createPR                                     bool
	githubUserName                               string
	selfApprove                                  bool
	ensureCorrectPromotionDockerfile             bool
	maxConcurrency                               int
	ocpBuildDataRepoDir                          string
	currentRelease                               ocpbuilddata.MajorMinor
	pruneUnusedReplacements                      bool
	pruneOCPBuilderReplacements                  bool
	pruneUnusedBaseImages                        bool
	applyReplacements                            bool
	ensureCorrectPromotionDockerfileIngoredRepos *flagutil.Strings
	ignoreRepos                                  *flagutil.Strings
	registryPath                                 string
	flagutil.GitHubOptions
}

func gatherOptions() (*options, error) {
	o := &options{
		ensureCorrectPromotionDockerfileIngoredRepos: &flagutil.Strings{},
		ignoreRepos: &flagutil.Strings{},
	}
	o.AddFlags(flag.CommandLine)
	flag.StringVar(&o.configDir, "config-dir", "", "The directory with the ci-operator configs")
	flag.BoolVar(&o.createPR, "create-pr", false, "If the tool should automatically create a PR. Requires --token-file")
	flag.StringVar(&o.githubUserName, "github-user-name", "openshift-bot", "Name of the github user. Required when --create-pr is set. Does nothing otherwise")
	flag.BoolVar(&o.selfApprove, "self-approve", false, "If the bot should self-approve its PR.")
	flag.BoolVar(&o.ensureCorrectPromotionDockerfile, "ensure-correct-promotion-dockerfile", false, "If Dockerfiles used for promotion should get updated to match whats in the ocp-build-data repo")
	flag.Var(o.ensureCorrectPromotionDockerfileIngoredRepos, "ensure-correct-promotion-dockerfile-ignored-repos", "Repos that are being ignored when ensuring the correct promotion dockerfile in org/repo notation. Can be passed multiple times.")
	flag.Var(o.ignoreRepos, "ignore-repos", "Repos that registry-replacer should completely skip in org/repo notation. Useful for repos using dockerfile-inputs feature. Can be passed multiple times.")
	flag.IntVar(&o.maxConcurrency, "concurrency", 500, "Maximum number of concurrent in-flight goroutines to handle files.")
	flag.StringVar(&o.ocpBuildDataRepoDir, "ocp-build-data-repo-dir", "../ocp-build-data", "The directory in which the ocp-build-data repository is")
	flag.StringVar(&o.currentRelease.Major, "current-release-major", "4", "The major version of the current release that is getting forwarded to from the master branch")
	flag.StringVar(&o.currentRelease.Minor, "current-release-minor", "6", "The minor version of the current release that is getting forwarded to from the master branch")
	flag.BoolVar(&o.pruneUnusedReplacements, "prune-unused-replacements", false, "If replacements that match nothing should get pruned from the config. Note that if --apply-replacements is set to false pruning will not take place.")
	flag.BoolVar(&o.pruneUnusedBaseImages, "prune-unused-base-images", false, "If base images that match nothing should get pruned from the config")
	flag.BoolVar(&o.applyReplacements, "apply-replacements", true, "If we should apply Dockerfile image replacements. You will probably always leave this as the default, and it's mostly used by tests that validate that base image pruning doesn't botch things. Note: If not applying replacements we will also skip unused replacement pruning.")
	flag.BoolVar(&o.pruneOCPBuilderReplacements, "prune-ocp-builder-replacements", false, "If all replacements that target the ocp/builder imagestream should be removed")
	flag.StringVar(&o.registryPath, "registry", "", "Path to the step registry directory")
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

	if o.ensureCorrectPromotionDockerfile {
		if o.ocpBuildDataRepoDir == "" {
			errs = append(errs, errors.New("--ocp-build-data-repo-dir must be set when --ensure-correct-promotion-dockerfile is set"))
		}
		if o.currentRelease.Minor == "" {
			errs = append(errs, errors.New("--current-release-minor must be set when --ensure-correct-promotion-dockerfile is set"))
		}
		if o.currentRelease.Major == "" {
			errs = append(errs, errors.New("--current-release-major must be set when --ensure-correct-promotion-dockerfile is set"))
		}
	}

	return o, utilerrors.NewAggregate(errs)
}

func main() {
	opts, err := gatherOptions()
	if err != nil {
		logrus.WithError(err).Fatal("failed to gather options")
	}
	logrus.WithField("maxConcurrency", opts.maxConcurrency).Info("set up the max concurrency")

	// Already create the client here if needed to make sure we fail asap if there is an issue
	var githubClient pgithub.Client
	if opts.TokenPath != "" {
		if err := secret.Add(opts.TokenPath); err != nil {
			logrus.WithError(err).Fatal("Failed to load github token")
		}
	}
	if opts.createPR {
		var err error
		githubClient, err = opts.GitHubClient(false)
		if err != nil {
			logrus.WithError(err).Fatal("Failed to construct githubClient")
		}
	}

	var promotionTargetToDockerfileMapping map[string]dockerfileLocation
	if opts.ensureCorrectPromotionDockerfile {
		var err error
		promotionTargetToDockerfileMapping, err = getPromotionTargetToDockerfileMapping(opts.ocpBuildDataRepoDir, opts.currentRelease)
		if err != nil {
			logrus.WithError(err).Fatal("Failed to construct promotion target to dockerfile mapping")
		}
	}

	var credentials *usernameToken
	if opts.TokenPath != "" {
		credentials = &usernameToken{
			username: opts.githubUserName,
			token:    string(secret.GetSecret(opts.TokenPath)),
		}
	}

	resolver, err := loadResolver(opts.registryPath)
	if err != nil {
		logrus.WithError(err).Fatal("failed to load resolver")
	}

	var errs []error
	errLock := &sync.Mutex{}
	sem := semaphore.NewWeighted(int64(opts.maxConcurrency))
	ctx := context.TODO()
	if err := config.OperateOnCIOperatorConfigDir(
		opts.configDir,
		func(config *api.ReleaseBuildConfiguration, info *config.Info) error {
			if err := sem.Acquire(ctx, 1); err != nil {
				return fmt.Errorf("failed to acquire semaphore: %w", err)
			}
			go func(filename string) {
				defer sem.Release(1)
				if err := replacer(
					github.FileGetterFactory,
					func(data []byte) error {
						return os.WriteFile(filename, data, 0644)
					},
					opts.pruneUnusedReplacements,
					opts.pruneOCPBuilderReplacements,
					opts.pruneUnusedBaseImages,
					opts.applyReplacements,
					opts.ensureCorrectPromotionDockerfile,
					sets.New(opts.ensureCorrectPromotionDockerfileIngoredRepos.Strings()...),
					sets.New(opts.ignoreRepos.Strings()...),
					promotionTargetToDockerfileMapping,
					opts.currentRelease,
					credentials,
					func(config api.ReleaseBuildConfiguration) (api.ReleaseBuildConfiguration, error) {
						return registry.ResolveConfig(resolver, config)
					},
				)(config, info); err != nil {
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
	if err := sem.Acquire(ctx, int64(opts.maxConcurrency)); err != nil {
		logrus.WithError(err).Fatal("failed to acquire semaphore while wating all workers to finish")
	}
	if err := utilerrors.NewAggregate(errs); err != nil {
		logrus.WithError(err).Fatal("Encountered errors")
	}

	if !opts.createPR {
		return
	}

	if err := upsertPR(githubClient, opts.configDir, opts.githubUserName, secret.GetSecret(opts.TokenPath), opts.selfApprove, opts.pruneUnusedReplacements, opts.ensureCorrectPromotionDockerfile); err != nil {
		logrus.WithError(err).Fatal("Failed to create PR")
	}
}

func loadResolver(path string) (registry.Resolver, error) {
	if path == "" {
		return nil, nil
	}
	refs, chains, workflows, _, _, _, observers, err := load.Registry(path, load.RegistryFlag(0))
	if err != nil {
		return nil, err
	}
	return registry.NewResolver(refs, chains, workflows, observers), nil
}

type usernameToken struct {
	username string
	token    string
}

// replacer ensures replace directives are in place. It fetches the files via http because using git
// en masse easily kills a developer laptop whereas the http calls are cheap and can be parallelized without
// bounds.
func replacer(
	githubFileGetterFactory func(org, repo, branch string, opts ...github.Opt) github.FileGetter,
	writer func([]byte) error,
	pruneUnusedReplacementsEnabled bool,
	pruneOCPBuilderReplacementsEnabled bool,
	pruneUnusedBaseImagesEnabled bool,
	applyReplacements bool,
	ensureCorrectPromotionDockerfile bool,
	ensureCorrectPromotionDockerfileIgnoredrepos sets.Set[string],
	ignoreRepos sets.Set[string],
	promotionTargetToDockerfileMapping map[string]dockerfileLocation,
	majorMinor ocpbuilddata.MajorMinor,
	credentials *usernameToken,
	configResolver func(config api.ReleaseBuildConfiguration) (api.ReleaseBuildConfiguration, error),
) func(*api.ReleaseBuildConfiguration, *config.Info) error {
	return func(config *api.ReleaseBuildConfiguration, info *config.Info) error {
		// Skip repos that should use dockerfile-inputs feature
		if ignoreRepos.Has(info.Org + "/" + info.Repo) {
			logrus.WithField("org", info.Org).WithField("repo", info.Repo).Debug("Skipping repo (in ignore-repos list)")
			return nil
		}

		if len(config.Images) == 0 {
			return nil
		}

		originalConfig, err := yaml.Marshal(config)
		if err != nil {
			return fmt.Errorf("failed to marshal config for comparison: %w", err)
		}

		// We have to do this first because the result of the following operations might
		// change based on what we do here.
		if ensureCorrectPromotionDockerfile {
			updateDockerfilesToMatchOCPBuildData(config, promotionTargetToDockerfileMapping, majorMinor.String(), ensureCorrectPromotionDockerfileIgnoredrepos)
		}

		var getter github.FileGetter
		if credentials == nil {
			getter = githubFileGetterFactory(info.Org, info.Repo, info.Branch)
		} else {
			getter = githubFileGetterFactory(info.Org, info.Repo, info.Branch, github.WithAuthentication(credentials.username, credentials.token))
		}
		allReplacementCandidates := sets.Set[string]{}

		if applyReplacements {
			// We have to skip pruning if we only get empty dockerfiles because it might mean
			// that we do not have the appropriate permissions.
			var hasNonEmptyDockerfile bool

			for idx, image := range config.Images {
				var dockerfile []byte
				if image.DockerfileLiteral != nil {
					dockerfile = []byte(*image.DockerfileLiteral)
				} else {
					dockerFilePath := "Dockerfile"
					if image.DockerfilePath != "" {
						dockerFilePath = image.DockerfilePath
					}

					var err error
					dockerfile, err = getter(filepath.Join(image.ContextDir, dockerFilePath))
					if err != nil {
						return fmt.Errorf("failed to get dockerfile %s: %w", image.DockerfilePath, err)
					}
				}

				hasNonEmptyDockerfile = hasNonEmptyDockerfile || len(dockerfile) > 0

				dockerfile, err = applyReplacementsToDockerfile(dockerfile, &image)
				if err != nil {
					return fmt.Errorf("failed to apply replacements to Dockerfile in %s/%s@%s: %w", info.Org, info.Repo, info.Branch, err)
				}

				foundTags, err := ensureReplacement(&config.Images[idx], dockerfile)
				if err != nil {
					return fmt.Errorf("failed to ensure replacements in %s/%s@%s: %w", info.Org, info.Repo, info.Branch, err)
				}
				for _, foundTag := range foundTags {
					if config.BaseImages == nil {
						config.BaseImages = map[string]api.ImageStreamTagReference{}
					}
					if _, exists := config.BaseImages[foundTag.String()]; exists {
						continue
					}
					config.BaseImages[foundTag.String()] = api.ImageStreamTagReference{
						Namespace: foundTag.Org,
						Name:      foundTag.Repo,
						Tag:       foundTag.Tag,
					}
				}

				replacementCandidates, err := extractReplacementCandidatesFromDockerfile(dockerfile)
				if err != nil {
					return fmt.Errorf("failed to extract source images from dockerfile in %s/%s@%s: %w", info.Org, info.Repo, info.Branch, err)
				}
				allReplacementCandidates.Insert(replacementCandidates.UnsortedList()...)
			}

			if pruneUnusedReplacementsEnabled && hasNonEmptyDockerfile {
				if err := pruneUnusedReplacements(config, allReplacementCandidates); err != nil {
					return fmt.Errorf("failed to prune unused replacements in %s/%s@%s: %w", info.Org, info.Repo, info.Branch, err)
				}
			} else if pruneUnusedReplacementsEnabled {
				logrus.WithField("org", info.Org).WithField("repo", info.Repo).WithField("branch", info.Branch).Info("Not purging unused replacements because we got an empty dockerfile")
			}
		}

		if pruneOCPBuilderReplacementsEnabled {
			if err := pruneOCPBuilderReplacements(config); err != nil {
				return fmt.Errorf("failed to prune ocp builder replacements: %w", err)
			}
		}

		if pruneUnusedBaseImagesEnabled {
			// to prune base images we'll need the fully-resolved config.
			resolvedConfig, err := configResolver(*config)
			if err != nil {
				// TODO this should stop execution rather than just logging an error, but since we want a smooth transition with
				// the new registry-replacer enhancements we'll add that in later, after this has been rolled out.
				logrus.WithError(err).Error("failed to resolve config. This means that base image pruning will not function as expected and will therefore be skipped")
			} else {
				if err := pruneUnusedBaseImages(config, &resolvedConfig); err != nil {
					return fmt.Errorf("failed to prune unused base images: %w", err)
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

func ensureReplacement(image *api.ProjectDirectoryImageBuildStepConfiguration, dockerfile []byte) ([]cidockerfile.OrgRepoTag, error) {
	toReplace := cidockerfile.ExtractRegistryReferences(dockerfile, "")
	var result []cidockerfile.OrgRepoTag
	for _, toReplace := range toReplace {
		orgRepoTag, err := cidockerfile.OrgRepoTagFromPullString(toReplace)
		if err != nil {
			return nil, fmt.Errorf("failed to parse string %s as pullspec: %w", toReplace, err)
		}

		// Assume ppl know what they are doing
		if cidockerfile.HasManualReplacementFor(image.Inputs, toReplace) {
			continue
		}

		if image.Inputs == nil {
			image.Inputs = map[string]api.ImageBuildInputs{}
		}
		inputs := image.Inputs[orgRepoTag.String()]
		inputs.As = sets.List(sets.New(inputs.As...).Insert(toReplace))
		image.Inputs[orgRepoTag.String()] = inputs

		result = append(result, orgRepoTag)
	}

	return result, nil
}

func upsertPR(gc pgithub.Client, dir, githubUsername string, token []byte, selfApprove, pruneUnusedReplacements, ensureCorrectPromotionDockerfile bool) error {
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

	censor := censor{secret: token}
	stdout := bumper.HideSecretsWriter{Delegate: os.Stdout, Censor: censor.Censor}
	stderr := bumper.HideSecretsWriter{Delegate: os.Stderr, Censor: censor.Censor}

	const targetBranch = "registry-replacer"
	if err := bumper.GitCommitAndPush(
		fmt.Sprintf("https://%s:%s@github.com/%s/release.git", githubUsername, string(token), githubUsername),
		targetBranch,
		githubUsername,
		fmt.Sprintf("%s@users.noreply.github.com", githubUsername),
		"Registry-replacer autocommit",
		stdout,
		stderr,
		false,
	); err != nil {
		return fmt.Errorf("failed to push changes: %w", err)
	}

	var labelsToAdd []string
	if selfApprove {
		logrus.Infof("Self-aproving PR by adding the %q and %q labels", labels.Approved, labels.LGTM)
		labelsToAdd = append(labelsToAdd, labels.Approved, labels.LGTM)
	}

	prBody := `This PR:
* Adds a replacement of all FROM registry.ci.openshift.org/anything directives found in any Dockerfile
  to make sure all images are pulled from the build cluster registry`

	if pruneUnusedReplacements {
		prBody += "\n* Prunes existing replacements that do not match any FROM directive in the Dockerfile"
	}
	if ensureCorrectPromotionDockerfile {
		prBody += "\n* Ensures the Dockerfiles used for promotion jobs matches the ones configured in [ocp-build-data](https://github.com/openshift/ocp-build-data/tree/openshift-4.6/images)"
	}
	repo, err := gc.GetRepo("openshift", "release")
	if err != nil {
		return fmt.Errorf("Error retrieving repository data: %w", err)
	}
	if err := bumper.UpdatePullRequestWithLabels(
		gc,
		"openshift",
		"release",
		prTitle,
		prBody,
		githubUsername+":"+targetBranch,
		repo.DefaultBranch,
		targetBranch,
		true,
		labelsToAdd,
		false,
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
	if image.From == "" {
		return in, nil
	}
	node, err := imagebuilder.ParseDockerfile(bytes.NewBuffer(in))
	if err != nil {
		return nil, fmt.Errorf("failed to parse Dockerfile: %w", err)
	}

	// https://github.com/openshift/builder/blob/6a52122d21e0528fbf014097d70770429fbc4448/pkg/build/builder/common.go#L402
	replaceLastFrom(node, string(image.From), "")

	// We do not need to expand the inputs because they are forced already to point to a
	// base_image which must be in the same cluster.
	return dockerfile.Write(node), nil
}

func extractReplacementCandidatesFromDockerfile(dockerfile []byte) (sets.Set[string], error) {
	replacementCandidates := sets.Set[string]{}
	node, err := imagebuilder.ParseDockerfile(bytes.NewBuffer(dockerfile))
	if err != nil {
		return nil, fmt.Errorf("failed to parse Dockerfile: %w", err)
	}

	// copied from https://github.com/openshift/builder/blob/1205194b1d67f2b68c163add5ae17e4b81962ec3/pkg/build/builder/common.go#L472-L497
	// only difference: We collect the replacement source values rather than doing the replacements
	names := make(map[string]string)
	stages, err := imagebuilder.NewStages(node, imagebuilder.NewBuilder(make(map[string]string)))
	if err != nil {
		return nil, fmt.Errorf("failed to construct imagebuilder stages: %w", err)
	}
	for _, stage := range stages {
		for _, child := range stage.Node.Children {
			switch {
			case child.Value == "from" && child.Next != nil:
				image := child.Next
				replacementCandidates.Insert(image.Value)
				names[stage.Name] = image.Value
				if alias := image.Next; alias != nil && alias.Value == "AS" && alias.Next != nil {
					replacementCandidates.Insert(alias.Next.Value)
				}
			case child.Value == "copy":
				if ref, ok := nodeHasFromRef(child); ok {
					if len(ref) > 0 {
						if _, ok := names[ref]; !ok {
							replacementCandidates.Insert(ref)
						}
					}
				}
			}
		}
	}

	return replacementCandidates, nil
}

func pruneUnusedReplacements(config *api.ReleaseBuildConfiguration, replacementCandidates sets.Set[string]) error {
	return pruneReplacements(config, func(asDirective string, _ string) (bool, error) {
		return replacementCandidates.Has(asDirective), nil
	})
}

func pruneOCPBuilderReplacements(config *api.ReleaseBuildConfiguration) error {
	return pruneReplacements(config, func(asDirective string, imageKey string) (bool, error) {
		orgRepoTag, err := cidockerfile.OrgRepoTagFromPullString(asDirective)
		if err != nil {
			return false, fmt.Errorf("failed to extract org and tag from pull spec %s: %w", asDirective, err)
		}
		if orgRepoTag.Org != "ocp" || orgRepoTag.Repo != "builder" {
			return true, nil
		}

		// If a config does not promote to ocp, it is not a configuration we want to hold
		// accountable to this rule. It could be a variant defined solely for testing something
		// exotic.
		promotesToOCP := false
		for _, promotedTag := range release.PromotedTags(config) {
			if promotedTag.Namespace == "ocp" {
				promotesToOCP = true
				break
			}
		}
		if !promotesToOCP {
			return true, nil
		}

		imagestreamTagReference, imageStreamTagReferenceExists := config.BaseImages[imageKey]
		if !imageStreamTagReferenceExists {
			return false, nil
		}

		// Fun special case: We set up a replacement for this ourselves to prevent direct references to api.ci
		if imagestreamTagReference.Namespace == orgRepoTag.Org && imagestreamTagReference.Name == orgRepoTag.Repo && imagestreamTagReference.Tag == orgRepoTag.Tag {
			return true, nil
		}

		return false, nil
	})
}

type asDirectiveFilter func(asDirectiveValue string, inputKey string) (keep bool, err error)

func pruneReplacements(config *api.ReleaseBuildConfiguration, filter asDirectiveFilter) error {
	var prunedImages []api.ProjectDirectoryImageBuildStepConfiguration
	var errs []error

	for _, image := range config.Images {
		for k, sourceImage := range image.Inputs {
			var newAs []string
			for _, sourceImage := range sourceImage.As {
				keep, err := filter(sourceImage, k)
				if err != nil {
					errs = append(errs, err)
					continue
				}
				if keep {
					newAs = append(newAs, sourceImage)
				}
			}
			if len(newAs) == 0 && len(sourceImage.Paths) == 0 {
				delete(image.Inputs, k)
			} else {
				copy := image.Inputs[k]
				copy.As = newAs
				image.Inputs[k] = copy
			}
		}
		if len(image.Inputs) > 0 || image.From != "" || image.To != "" {
			prunedImages = append(prunedImages, image)
		}
	}

	config.Images = prunedImages

	return utilerrors.NewAggregate(errs)
}

// pruneUnusedBaseImages uses the fully-resolved config to make sure an image is not used directly in  the config, or within any of the tests.
// If it is not, then we prune it.
func pruneUnusedBaseImages(config *api.ReleaseBuildConfiguration, resolvedConfig *api.ReleaseBuildConfiguration) error {
	usedBaseImages := sets.New[string]()

	getOperatorImages(config, usedBaseImages)
	for _, step := range resolvedConfig.RawSteps {
		switch {
		case step.BundleSourceStepConfiguration != nil:
			getBundleSourceImages(config, &usedBaseImages, step.BundleSourceStepConfiguration)
		case step.IndexGeneratorStepConfiguration != nil && step.IndexGeneratorStepConfiguration.BaseIndex != "":
			_, name, _ := config.DependencyParts(api.StepDependency{Name: step.IndexGeneratorStepConfiguration.BaseIndex}, nil)
			usedBaseImages.Insert(name)
		case step.OutputImageTagStepConfiguration != nil:
			usedBaseImages.Insert(string(step.OutputImageTagStepConfiguration.From))
		case step.PipelineImageCacheStepConfiguration != nil:
			usedBaseImages.Insert(string(step.PipelineImageCacheStepConfiguration.From))
		case step.ProjectDirectoryImageBuildInputs != nil:
			getImageBuildInputImages(config, step.ProjectDirectoryImageBuildInputs.Inputs, &usedBaseImages)
		case step.ProjectDirectoryImageBuildStepConfiguration != nil:
			getImageBuildInputImages(config, step.ProjectDirectoryImageBuildStepConfiguration.Inputs, &usedBaseImages)
		case step.RPMImageInjectionStepConfiguration != nil:
			usedBaseImages.Insert(string(step.RPMImageInjectionStepConfiguration.From))
		case step.SourceStepConfiguration != nil:
			usedBaseImages.Insert(string(step.SourceStepConfiguration.From))
		case step.TestStepConfiguration != nil:
			getTestStepImages(resolvedConfig, &usedBaseImages, step.TestStepConfiguration)
		case step.ReleaseImagesTagStepConfiguration != nil || step.ResolvedReleaseImagesStepConfiguration != nil || step.RPMServeStepConfiguration != nil:
			// no op
		default:
			return fmt.Errorf("unsupported step configuration provided when pruning base images")
		}
	}

	for _, test := range resolvedConfig.Tests {
		getTestStepImages(resolvedConfig, &usedBaseImages, &test)
	}

	for _, image := range resolvedConfig.Images {
		usedBaseImages.Insert(string(image.From))
		for input := range image.Inputs {
			usedBaseImages.Insert(input)
		}
	}

	pruneImage := func(images *map[string]api.ImageStreamTagReference, sourceImage string) error {
		var keep bool
		for candidate := range usedBaseImages {
			orgRepoTag, err := cidockerfile.OrgRepoTagFromPullString(candidate)
			if err != nil {
				return fmt.Errorf("failed to parse string %s as pullspec: %w", candidate, err)
			}

			// consider it a match if either the orgRepoTag matches, or the sourceImage matches directly. Depending on
			// where the image was sourced from it might be in pull string format, or it might just be the image name.
			keep = orgRepoTag.String() == sourceImage || candidate == sourceImage

			if keep {
				break
			}
		}
		if !keep {
			delete(*images, sourceImage)
		}

		return nil
	}

	for sourceImage := range config.BaseImages {
		if err := pruneImage(&config.BaseImages, sourceImage); err != nil {
			return err
		}
	}

	for sourceImage := range config.BaseRPMImages {
		if err := pruneImage(&config.BaseRPMImages, sourceImage); err != nil {
			return err
		}
	}

	return nil
}

func getOperatorImages(config *api.ReleaseBuildConfiguration, usedBaseImages sets.Set[string]) {
	if config.Operator != nil {
		for _, substitution := range config.Operator.Substitutions {
			_, name, _ := config.DependencyParts(api.StepDependency{Name: substitution.With}, nil)
			usedBaseImages.Insert(name)
		}
		for _, bundle := range config.Operator.Bundles {
			_, name, _ := config.DependencyParts(api.StepDependency{Name: bundle.BaseIndex}, nil)
			usedBaseImages.Insert(name)
		}
	}
}

func getBundleSourceImages(config *api.ReleaseBuildConfiguration, images *sets.Set[string], step *api.BundleSourceStepConfiguration) {
	for _, substitution := range step.Substitutions {
		_, name, _ := config.DependencyParts(api.StepDependency{Name: substitution.With}, nil)
		images.Insert(name)
	}
}

func getImageBuildInputImages(config *api.ReleaseBuildConfiguration, inputs map[string]api.ImageBuildInputs, images *sets.Set[string]) {
	for input := range inputs {
		_, name, _ := config.DependencyParts(api.StepDependency{Name: input}, nil)
		images.Insert(name)
	}
}

func getTestStepImages(config *api.ReleaseBuildConfiguration, images *sets.Set[string], step *api.TestStepConfiguration) {
	if step.MultiStageTestConfigurationLiteral != nil {
		getTestImages(config, images, step.MultiStageTestConfigurationLiteral.Pre)
		getTestImages(config, images, step.MultiStageTestConfigurationLiteral.Test)
		getTestImages(config, images, step.MultiStageTestConfigurationLiteral.Post)
	} else if step.ContainerTestConfiguration != nil {
		images.Insert(string(step.ContainerTestConfiguration.From))
	}
}

func getTestImages(config *api.ReleaseBuildConfiguration, images *sets.Set[string], steps []api.LiteralTestStep) {
	for _, step := range steps {
		if step.From != "" {
			_, name, _ := config.DependencyParts(api.StepDependency{Name: step.From}, nil)
			images.Insert(name)
		}
		if step.FromImage != nil {
			images.Insert(step.FromImage.ISTagName())
		}

		// see if we have any pipeline dependency images that also need to be included.
		for _, dependency := range step.Dependencies {
			if dependency.Name == "" {
				continue
			}
			_, name, explicit := config.DependencyParts(dependency, nil)
			if explicit {
				images.Insert(name)
			}
		}
	}
}

type dockerfileLocation struct {
	contextDir string
	dockerfile string
}

func getPromotionTargetToDockerfileMapping(ocpBuildDataDir string, majorMinor ocpbuilddata.MajorMinor) (map[string]dockerfileLocation, error) {
	configs, err := ocpbuilddata.LoadImageConfigs(ocpBuildDataDir, majorMinor)
	if err != nil {
		return nil, fmt.Errorf("failed to read image configs from ocp-build-data: %w", err)
	}
	result := map[string]dockerfileLocation{}
	for _, config := range configs {
		result[config.PromotesTo()] = dockerfileLocation{contextDir: config.Content.Source.Path, dockerfile: config.Content.Source.Dockerfile}
	}
	return result, nil
}

func updateDockerfilesToMatchOCPBuildData(
	config *api.ReleaseBuildConfiguration,
	promotionTargetToDockerfileMapping map[string]dockerfileLocation,
	majorMinorVersion string,
	ignoredRepos sets.Set[string],
) {

	// The tool only works for the current release
	if config.Metadata.Branch != "main" && config.Metadata.Branch != "master" {
		return
	}
	if ignoredRepos.Has(config.Metadata.Org + "/" + config.Metadata.Repo) {
		return
	}

	// Configs indexed by tag
	promotedTags := map[string]api.ImageStreamTagReference{}
	for _, promotedTag := range release.PromotedTags(config) {
		if promotedTag.Namespace != "ocp" || promotedTag.Name != majorMinorVersion {
			continue
		}
		promotedTags[promotedTag.Tag] = promotedTag
	}
	if len(promotedTags) == 0 {
		return
	}

	for idx, image := range config.Images {
		promotionTarget, ok := promotedTags[string(image.To)]
		if !ok {
			continue
		}
		stringifiedPromotionTarget := fmt.Sprintf("registry.ci.openshift.org/%s", promotionTarget.ISTagName())
		dockerfilePath, ok := promotionTargetToDockerfileMapping[stringifiedPromotionTarget]
		if !ok {
			logrus.WithField("promotiontarget", stringifiedPromotionTarget).Info("Ignoring promotion target for which we have no ocp-build-data config")
			continue
		}
		if image.ContextDir != dockerfilePath.contextDir {
			config.Images[idx].ContextDir = dockerfilePath.contextDir
		}
		if image.DockerfilePath != dockerfilePath.dockerfile {
			config.Images[idx].DockerfilePath = dockerfilePath.dockerfile
		}
	}
}
