package sanitizer

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"k8s.io/apimachinery/pkg/util/sets"
	prowconfig "sigs.k8s.io/prow/pkg/config"
	"sigs.k8s.io/yaml"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/dispatcher"
	"github.com/openshift/ci-tools/pkg/jobconfig"
	"github.com/openshift/ci-tools/pkg/util"
	"github.com/openshift/ci-tools/pkg/util/gzip"
)

const (
	cioperatorLatestImage = "ci-operator:latest"
)

func DeterminizeJobs(prowJobConfigDir string, config *dispatcher.Config, pjs map[string]string) error {
	ch := make(chan string)
	errCh := make(chan error)
	produce := func() error {
		defer close(ch)
		return filepath.WalkDir(prowJobConfigDir, func(path string, info fs.DirEntry, err error) error {
			if err != nil {
				errCh <- fmt.Errorf("failed to walk file/directory %q: %w", path, err)
				return nil
			}
			if info.IsDir() || !strings.HasSuffix(path, ".yaml") {
				return nil
			}
			ch <- path
			return nil
		})
	}
	map_ := func() error {
		for path := range ch {
			data, err := gzip.ReadFileMaybeGZIP(path)
			if err != nil {
				errCh <- fmt.Errorf("failed to read file %q: %w", path, err)
				continue
			}

			jobConfig := &prowconfig.JobConfig{}
			if err := yaml.Unmarshal(data, jobConfig); err != nil {
				errCh <- fmt.Errorf("failed to unmarshal file %q: %w", path, err)
				continue
			}

			if err := defaultJobConfig(jobConfig, path, config, pjs); err != nil {
				errCh <- fmt.Errorf("failed to default job config %q: %w", path, err)
			}

			serialized, err := yaml.Marshal(jobConfig)
			if err != nil {
				errCh <- fmt.Errorf("failed to marshal file %q: %w", path, err)
				continue
			}

			if err := os.WriteFile(path, serialized, 0644); err != nil {
				errCh <- fmt.Errorf("failed to write file %q: %w", path, err)
				continue
			}
		}
		return nil
	}
	if err := util.ProduceMap(0, produce, map_, errCh); err != nil {
		return fmt.Errorf("failed to determinize all Prow jobs: %w", err)
	}
	return nil
}

func defaultJobConfig(jc *prowconfig.JobConfig, path string, config *dispatcher.Config, pjs map[string]string) error {
	for k := range jc.PresubmitsStatic {
		for idx := range jc.PresubmitsStatic[k] {
			cluster, err := determineCluster(jc.PresubmitsStatic[k][idx].JobBase, config, pjs, path)
			if err != nil {
				return err
			}
			jc.PresubmitsStatic[k][idx].JobBase.Cluster = cluster

			if cluster == string(api.ClusterARM01) && isCIOperatorLatest(jc.PresubmitsStatic[k][idx].JobBase.Spec.Containers[0].Image) {
				jc.PresubmitsStatic[k][idx].JobBase.Spec.Containers[0].Image = "ci-operator-arm64:latest"
			}

			// Enforce that even hand-crafted jobs have explicit branch regexes
			// Presubmits are generally expected to hit also on "feature branches",
			// so we generate regexes for both exact match and feature branch patterns
			featureBranches := sets.New[string]()
			for _, branch := range jc.PresubmitsStatic[k][idx].Branches {
				featureBranches.Insert(jobconfig.FeatureBranch(branch))
				featureBranches.Insert(jobconfig.ExactlyBranch(branch))
			}
			jc.PresubmitsStatic[k][idx].Branches = sets.List(featureBranches)
		}
	}
	for k := range jc.PostsubmitsStatic {
		for idx := range jc.PostsubmitsStatic[k] {
			cluster, err := determineCluster(jc.PostsubmitsStatic[k][idx].JobBase, config, pjs, path)
			if err != nil {
				return err
			}
			jc.PostsubmitsStatic[k][idx].JobBase.Cluster = cluster

			if cluster == string(api.ClusterARM01) && isCIOperatorLatest(jc.PostsubmitsStatic[k][idx].JobBase.Spec.Containers[0].Image) {
				jc.PostsubmitsStatic[k][idx].JobBase.Spec.Containers[0].Image = "ci-operator-arm64:latest"
			}

			// Enforce that even hand-crafted jobs have explicit branch regexes
			// Postsubmits are generally expected to only hit on exact match branches
			// so we do not generate a regex for feature branch pattern like we do
			// for presubmits above
			for item := range jc.PostsubmitsStatic[k][idx].Branches {
				jc.PostsubmitsStatic[k][idx].Branches[item] = jobconfig.ExactlyBranch(jc.PostsubmitsStatic[k][idx].Branches[item])
			}
		}
	}
	for idx := range jc.Periodics {
		cluster, err := determineCluster(jc.Periodics[idx].JobBase, config, pjs, path)
		if err != nil {
			return err
		}
		jc.Periodics[idx].JobBase.Cluster = cluster
	}
	return nil
}

func isCIOperatorLatest(image string) bool {
	parts := strings.Split(image, "/")
	lastPart := parts[len(parts)-1]

	return lastPart == cioperatorLatestImage
}

func determineCluster(jb prowconfig.JobBase, config *dispatcher.Config, pjs map[string]string, path string) (string, error) {
	if pjs == nil {
		c, err := config.GetClusterForJob(jb, path)
		if err != nil {
			return "", err
		}
		return string(c), nil
	}
	return pjs[jb.Name], nil

}
