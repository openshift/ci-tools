package main

import (
	"context"
	"flag"
	"fmt"
	"io/ioutil"
	"path/filepath"
	"sync"

	"golang.org/x/sync/semaphore"
	"sigs.k8s.io/yaml"

	"k8s.io/test-infra/prow/config/secret"
	"k8s.io/test-infra/prow/git"

	"github.com/google/go-cmp/cmp"
	cioperatorapi "github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/config"
	"github.com/openshift/ci-tools/pkg/github"
	"github.com/openshift/ci-tools/pkg/github/prcreation"
	"github.com/sirupsen/logrus"
)

type opts struct {
	prcreation.PRCreationOptions
	ciOperatorConfigDir string
	maxConcurrency      int64
	cloneCeiling        int
}

func main() {
	o := opts{}
	o.PRCreationOptions.AddFlags(flag.CommandLine)
	flag.StringVar(&o.ciOperatorConfigDir, "ci-operator-config-dir", "", "Basepath of the ci-operator config")
	flag.Int64Var(&o.maxConcurrency, "max-concurrency", 4, "The max concurrency")
	flag.IntVar(&o.cloneCeiling, "clone-ceiling", 1, "Max number of repos to clone")
	flag.Parse()

	if err := o.GitHubOptions.Validate(false); err != nil {
		logrus.WithError(err).Fatal("Failed to validate GitHub options")
	}

	var lock sync.Mutex
	var errs []error
	sema := semaphore.NewWeighted(o.maxConcurrency)
	ctx := context.Background()

	if err := o.PRCreationOptions.Finalize(); err != nil {
		logrus.WithError(err).Fatal("failed to set up pr creation options")
	}
	sa := &secret.Agent{}
	if err := sa.Start(nil); err != nil {
		logrus.WithError(err).Fatal("failed to start secret agent")
	}
	gc, err := o.GitHubOptions.GitClient(sa, false)
	if err != nil {
		logrus.WithError(err).Fatal("failed to construct git client")
	}
	defer func() {
		if err := gc.Clean(); err != nil {
			logrus.WithError(err).Error("git client failed to clean")
		}
	}()

	process := process(
		func(info *config.Info) bool { return info.Org == "openshift" },
		gc.Clone,
		o.cloneCeiling,
		func(localSourceDir, org, repo, targetBranch string) error {
			return o.PRCreationOptions.UpsertPR(localSourceDir, org, repo, targetBranch, "Upserting .ci-operator.yaml", prcreation.SkipPRCreation(),
				prcreation.PrBody(`TODO: Write`),
			)
		},
	)
	err = config.OperateOnCIOperatorConfigDir(o.ciOperatorConfigDir, func(cfg *cioperatorapi.ReleaseBuildConfiguration, metadata *config.Info) error {
		if err := sema.Acquire(ctx, 1); err != nil {
			return fmt.Errorf("failed to acquire semaphore: %w", err)
		}
		go func() {
			defer sema.Release(1)
			if err := process(cfg, metadata); err != nil {
				lock.Lock()
				errs = append(errs, err)
				lock.Unlock()
			}
		}()

		return nil
	})
	if err != nil {
		errs = append(errs, err)
	}
	if err := sema.Acquire(ctx, o.maxConcurrency); err != nil {
		logrus.WithError(err).Fatal("failed to wait for walking to finish")
	}

	for _, err := range errs {
		logrus.WithError(err).Error("Encountered error")
	}
	if len(errs) > 0 {
		logrus.Fatal("Encountered errors")
	}
}

func process(
	filter func(*config.Info) bool,
	clone func(org, repo string) (*git.Repo, error),
	cloneCeiling int,
	createPr func(localSourceDir, org, repo, targetBranch string) error,
) func(cfg *cioperatorapi.ReleaseBuildConfiguration, metadata *config.Info) error {

	var clonesDone int
	var mutex sync.Mutex

	return func(cfg *cioperatorapi.ReleaseBuildConfiguration, metadata *config.Info) error {
		if !filter(metadata) {
			return nil
		}
		if cfg.BuildRootImage == nil || cfg.BuildRootImage.FromRepository || (metadata.Metadata.Branch != "master" && metadata.Metadata.Branch != "main") {
			return nil
		}

		if cfg.BuildRootImage.ImageStreamTagReference == nil {
			// TODO: What to do about these?
			return nil
		}

		data, err := github.FileGetterFactory(metadata.Org, metadata.Repo, metadata.Branch)(cioperatorapi.CIOperatorInrepoConfigFileName)
		if err != nil {
			return fmt.Errorf("failed to get %s/%s#%s:%s: %w", metadata.Org, metadata.Repo, metadata.Branch, cioperatorapi.CIOperatorInrepoConfigFileName)
		}

		var inrepoconfig cioperatorapi.CIOperatorInrepoConfig
		if err := yaml.Unmarshal(data, &inrepoconfig); err != nil {
			return fmt.Errorf("failed to unmarshal %s/%s#%s:%s: %w", metadata.Org, metadata.Repo, metadata.Branch, cioperatorapi.CIOperatorInrepoConfigFileName)
		}

		expected := cioperatorapi.CIOperatorInrepoConfig{
			BuildRootImage: *cfg.BuildRootImage.ImageStreamTagReference,
		}

		if diff := cmp.Diff(inrepoconfig, expected); diff == "" {
			return nil
		}
		l := logrus.WithFields(logrus.Fields{"org": metadata.Org, "repo": metadata.Repo, "branch": metadata.Branch})
		l.Info(".ci-operator.yaml needs updating")

		expectedSerialized, err := yaml.Marshal(expected)
		if err != nil {
			return fmt.Errorf("failed to marshal %s for %s/%s: %w", cioperatorapi.CIOperatorInrepoConfigFileName, metadata.Org, metadata.Repo, err)
		}

		mutex.Lock()
		if clonesDone >= cloneCeiling {
			//l.Info("Reached clone ceiling, not cloning repo")
			mutex.Unlock()
			return nil
		}
		clonesDone++
		mutex.Unlock()

		repo, err := clone(metadata.Org, metadata.Repo)
		if err != nil {
			return fmt.Errorf("failed to clone %s/%s: %w", metadata.Org, metadata.Repo)
		}
		defer func() {
			if err := repo.Clean(); err != nil {
				l.WithError(err).Error("failed to clean local repo")
			}
		}()
		if err := repo.Checkout(metadata.Branch); err != nil {
			return fmt.Errorf("failed to checkout %s in %s/%s: %w", metadata.Branch, metadata.Org, metadata.Repo, err)
		}

		path := filepath.Join(repo.Directory(), cioperatorapi.CIOperatorInrepoConfigFileName)
		if err := ioutil.WriteFile(path, expectedSerialized, 0644); err != nil {
			return fmt.Errorf("falled to write %s for %s/%s: %w", path, metadata.Org, metadata.Repo, err)
		}
		l.WithField("path", path).Info("Wrote .ci-operator.yaml")

		return createPr(repo.Directory(), metadata.Org, metadata.Repo, metadata.Branch)
	}
}
