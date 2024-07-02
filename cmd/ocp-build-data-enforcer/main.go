package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/pmezard/go-difflib/difflib"
	"github.com/sirupsen/logrus"
	"golang.org/x/sync/errgroup"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	git "sigs.k8s.io/prow/pkg/git/v2"

	"github.com/openshift/imagebuilder"
	dockercmd "github.com/openshift/imagebuilder/dockerfile/command"

	"github.com/openshift/ci-tools/pkg/api/ocpbuilddata"
	"github.com/openshift/ci-tools/pkg/github"
	"github.com/openshift/ci-tools/pkg/github/prcreation"
)

type options struct {
	ocpBuildDataRepoDir string
	majorMinor          ocpbuilddata.MajorMinor
	createPRs           bool
	prCreationCeiling   int
	*prcreation.PRCreationOptions
}

func gatherOptions() (*options, error) {
	o := &options{PRCreationOptions: &prcreation.PRCreationOptions{}}
	o.PRCreationOptions.AddFlags(flag.CommandLine)
	flag.StringVar(&o.ocpBuildDataRepoDir, "ocp-build-data-repo-dir", "../ocp-build-data", "The directory in which the ocp-build-data repository is")
	flag.StringVar(&o.majorMinor.Minor, "minor", "6", "The minor version to target")
	flag.BoolVar(&o.createPRs, "create-prs", false, "If the tool should create PRs")
	flag.IntVar(&o.prCreationCeiling, "pr-creation-ceiling", 5, "The maximum number of PRs to upsert")
	flag.Parse()

	if o.createPRs {
		if err := o.PRCreationOptions.Finalize(); err != nil {
			return nil, fmt.Errorf("failed to finalize pr creation options: %w", err)
		}
	} else {
		o.prCreationCeiling = 0
	}
	o.ocpBuildDataRepoDir = filepath.Clean(o.ocpBuildDataRepoDir)
	return o, nil
}

func main() {
	logrus.StandardLogger().SetFormatter(&logrus.TextFormatter{EnvironmentOverrideColors: true})
	opts, err := gatherOptions()
	if err != nil {
		logrus.WithError(err).Fatal("Failed to gather options")
	}
	opts.majorMinor.Major = "4"

	configs, err := ocpbuilddata.LoadImageConfigs(opts.ocpBuildDataRepoDir, opts.majorMinor)
	if err != nil {
		switch err := err.(type) {
		case utilerrors.Aggregate:
			for _, err := range err.Errors() {
				logrus.WithError(err).Error("Encountered error")
			}
		default:
			logrus.WithError(err).Error("Encountered error")
		}
		logrus.Fatal("Encountered errors")
	}

	clientFactory, err := git.NewClientFactory()
	if err != nil {
		logrus.WithError(err).Fatal("Failed to construct git client factory")
	}
	diffProcessor := diffProcessor{
		maxPRs:         opts.prCreationCeiling,
		gitClient:      clientFactory,
		prCreationOpts: opts.PRCreationOptions,
	}

	errGroup := &errgroup.Group{}
	for idx := range configs {
		idx := idx
		errGroup.Go(func() error {
			return processDockerfile(configs[idx], diffProcessor.addDiff)
		})
	}
	if err := errGroup.Wait(); err != nil {
		logrus.WithError(err).Fatal("Processing failed")
	}

	if err := diffProcessor.process(); err != nil {
		logrus.WithError(err).Fatal("PR creation/diff printing failed")
	}

	logrus.Infof("Successfully processed %d configs", len(configs))
}

type diffProcessorFunc func(l *logrus.Entry, org, repo, branch, path string, oldContent, newContent []byte) error

func processDockerfile(config ocpbuilddata.OCPImageConfig, processor diffProcessorFunc) error {
	log := logrus.WithField("file", config.SourceFileName).WithField("org/repo", config.PublicRepo.String())
	if config.PublicRepo.Org == "openshift-priv" {
		log.Trace("Ignoring repo in openshift-priv org")
		return nil
	}
	getter := github.FileGetterFactory(config.PublicRepo.Org, config.PublicRepo.Repo, "release-4.6")

	log = log.WithField("dockerfile", config.Dockerfile())
	data, err := getter(config.Dockerfile())
	if err != nil {
		return fmt.Errorf("failed to get dockerfile: %w", err)
	}
	if len(data) == 0 {
		log.Info("dockerfile is empty")
		return nil
	}

	updated, hasDiff, err := updateDockerfile(data, config)
	if err != nil {
		return fmt.Errorf("failed to update dockerfile: %w", err)
	}
	if !hasDiff {
		return nil
	}
	branch := "master"
	if config.Content != nil && config.Content.Source.Git != nil && strings.HasPrefix(config.Content.Source.Git.Branch.Taget, "openshift-") {
		branch = config.Content.Source.Git.Branch.Taget
	}
	if err := processor(log, config.PublicRepo.Org, config.PublicRepo.Repo, branch, config.Dockerfile(), data, updated); err != nil {
		return fmt.Errorf("failed to process updated dockerfile: %w", err)
	}

	return nil
}

func updateDockerfile(dockerfile []byte, config ocpbuilddata.OCPImageConfig) ([]byte, bool, error) {
	rootNode, err := imagebuilder.ParseDockerfile(bytes.NewBuffer(dockerfile))
	if err != nil {
		return nil, false, fmt.Errorf("failed to parse Dockerfile: %w", err)
	}

	stages, err := imagebuilder.NewStages(rootNode, imagebuilder.NewBuilder(nil))
	if err != nil {
		return nil, false, fmt.Errorf("failed to construct imagebuilder stages: %w", err)
	}

	cfgStages, err := config.Stages()
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

type diff struct {
	log        *logrus.Entry
	org        string
	repo       string
	path       string
	branch     string
	oldContent []byte
	newContent []byte
}

type diffProcessor struct {
	lock           sync.Mutex
	maxPRs         int
	gitClient      git.ClientFactory
	prCreationOpts *prcreation.PRCreationOptions
	diffs          []diff
}

func (dp *diffProcessor) addDiff(l *logrus.Entry, org, repo, branch, path string, oldContent, newContent []byte) error {
	dp.lock.Lock()
	defer dp.lock.Unlock()
	dp.diffs = append(dp.diffs, diff{log: l, org: org, repo: repo, branch: branch, path: path, oldContent: oldContent, newContent: newContent})
	return nil
}

func (dp *diffProcessor) process() error {
	// In order to be able to make use of the ceiling setting, we need to sort the diffs first
	sort.Slice(dp.diffs, func(i, j int) bool {
		return dp.diffs[i].org+dp.diffs[i].repo+dp.diffs[i].repo < dp.diffs[j].org+dp.diffs[j].repo+dp.diffs[j].repo
	})

	logrus.Infof("diffs: %d", len(dp.diffs))
	for _, d := range dp.diffs {
		// Closure so we can use defer to clean up the git client
		if err := func(d diff) error {
			// Just print the diff
			if dp.maxPRs == 0 {
				diff := difflib.UnifiedDiff{
					A:        difflib.SplitLines(string(d.oldContent)),
					B:        difflib.SplitLines(string(d.newContent)),
					FromFile: "original",
					ToFile:   "updated",
					Context:  3,
				}
				diffStr, err := difflib.GetUnifiedDiffString(diff)
				if err != nil {
					return fmt.Errorf("failed to construct diff: %w", err)
				}
				d.log.Infof("Diff:\n---\n%s\n---\n", diffStr)
				return nil
			}

			// Create PR
			dp.maxPRs--
			gitClient, err := dp.gitClient.ClientFor(d.org, d.repo)
			if err != nil {
				return fmt.Errorf("Failed to get git client: %w", err)
			}
			defer func() {
				if err := gitClient.Clean(); err != nil {
					d.log.WithError(err).Error("Gitclient clean failed")
				}
			}()

			if err := gitClient.Checkout(d.branch); err != nil {
				return fmt.Errorf("failed to checkout %s branch: %w", d.branch, err)
			}
			if err := os.WriteFile(filepath.Join(gitClient.Directory(), d.path), d.newContent, 0644); err != nil {
				return fmt.Errorf("failed to write updated Dockerfile into repo: %w", err)
			}
			if err := dp.prCreationOpts.UpsertPR(
				gitClient.Directory(),
				d.org,
				d.repo,
				d.branch,
				fmt.Sprintf("Updating %s baseimages to match ocp-build-data config", d.path),
				prcreation.PrBody(strings.Join([]string{
					"This PR is autogenerated by the [ocp-build-data-enforcer][1].",
					"It updates the base images in the Dockerfile used for promotion in order to ensure it",
					"matches the configuration in the [ocp-build-data repository][2] used",
					"for producing release artifacts.",
					"",
					"Instead of merging this PR you can also create an alternate PR that includes the changes found here.",
					"",
					"If you believe the content of this PR is incorrect, please contact the dptp team in",
					"#aos-art.",
					"",
					"[1]: https://github.com/openshift/ci-tools/tree/master/cmd/ocp-build-data-enforcer",
					"[2]: https://github.com/openshift/ocp-build-data/tree/openshift-4.6/images",
				}, "\n")),
			); err != nil {
				return fmt.Errorf("failed to create PR: %w", err)
			}

			return nil
		}(d); err != nil {
			return err
		}
	}

	return nil
}
