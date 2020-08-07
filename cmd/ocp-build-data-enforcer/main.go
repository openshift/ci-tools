package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"

	"github.com/openshift/imagebuilder"
	dockercmd "github.com/openshift/imagebuilder/dockerfile/command"
	"github.com/pmezard/go-difflib/difflib"
	"github.com/sirupsen/logrus"
	"golang.org/x/sync/errgroup"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"

	"github.com/openshift/ci-tools/pkg/api/ocpbuilddata"
	"github.com/openshift/ci-tools/pkg/github"
)

type options struct {
	ocpBuildDataRepoDir string
	majorMinor          ocpbuilddata.MajorMinor
}

func gatherOptions() *options {
	o := &options{}
	flag.StringVar(&o.ocpBuildDataRepoDir, "ocp-build-data-repo-dir", "../ocp-build-data", "The directory in which the ocp-build-data reposity is")
	flag.StringVar(&o.majorMinor.Minor, "minor", "6", "The minor version to target")
	flag.Parse()
	return o
}
func main() {
	opts := gatherOptions()
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

	logrus.Infof("Processed %d configs", len(configs))
}

func processDockerfile(config ocpbuilddata.OCPImageConfig) {
	log := logrus.WithField("file", config.SourceFileName).WithField("org/repo", config.PublicRepo.String())
	if config.PublicRepo.Org == "openshift-priv" {
		log.Trace("Ignoring repo in openshift-priv org")
		return
	}
	getter := github.FileGetterFactory(config.PublicRepo.Org, config.PublicRepo.Repo, "release-4.6")

	log = log.WithField("dockerfile", config.Dockerfile())
	data, err := getter(config.Dockerfile())
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
