package sanitizer

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	prowconfig "sigs.k8s.io/prow/pkg/config"
	"sigs.k8s.io/yaml"

	"github.com/openshift/ci-tools/pkg/dispatcher"
	"github.com/openshift/ci-tools/pkg/jobconfig"
	"github.com/openshift/ci-tools/pkg/util/gzip"
)

// ApplyDefaultClusterAssignments applies the configured cluster rules atomically.
func ApplyDefaultClusterAssignments(prowJobConfigDir string, config *dispatcher.Config, blocked sets.Set[string], cm dispatcher.ClusterMap) error {
	return applyClusterAssignmentsToFiles(prowJobConfigDir, func(path string, jobConfig *prowconfig.JobConfig) (bool, error) {
		return applyDefaultClusterAssignments(path, jobConfig, config, blocked, cm)
	})
}

func applyClusterAssignmentsToFiles(prowJobConfigDir string, apply func(string, *prowconfig.JobConfig) (bool, error)) error {
	var errs []error
	if err := filepath.WalkDir(prowJobConfigDir, func(path string, info fs.DirEntry, err error) error {
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to walk file/directory %q: %w", path, err))
			return nil
		}
		if info.IsDir() || !strings.HasSuffix(path, ".yaml") {
			return nil
		}

		data, err := gzip.ReadFileMaybeGZIP(path)
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to read file %q: %w", path, err))
			return nil
		}

		jobConfig := &prowconfig.JobConfig{}
		if err := yaml.Unmarshal(data, jobConfig); err != nil {
			errs = append(errs, fmt.Errorf("failed to unmarshal file %q: %w", path, err))
			return nil
		}

		changed, err := apply(path, jobConfig)
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to apply cluster assignments to file %q: %w", path, err))
			return nil
		}
		if !changed {
			return nil
		}

		if err := jobconfig.WriteToFileAtomic(path, jobConfig); err != nil {
			errs = append(errs, fmt.Errorf("failed to atomically write file %q: %w", path, err))
		}
		return nil
	}); err != nil {
		return fmt.Errorf("failed to walk Prow job configs: %w", err)
	}
	return utilerrors.NewAggregate(errs)
}

func applyDefaultClusterAssignments(path string, jobConfig *prowconfig.JobConfig, config *dispatcher.Config, blocked sets.Set[string], cm dispatcher.ClusterMap) (bool, error) {
	mostUsedCluster := dispatcher.FindMostUsedCluster(jobConfig)
	changed := false

	for orgRepo := range jobConfig.PresubmitsStatic {
		for idx := range jobConfig.PresubmitsStatic[orgRepo] {
			jobChanged, err := applyDefaultClusterAssignment(&jobConfig.PresubmitsStatic[orgRepo][idx].JobBase, path, config, mostUsedCluster, blocked, cm)
			if err != nil {
				return false, err
			}
			changed = changed || jobChanged
		}
	}
	for orgRepo := range jobConfig.PostsubmitsStatic {
		for idx := range jobConfig.PostsubmitsStatic[orgRepo] {
			jobChanged, err := applyDefaultClusterAssignment(&jobConfig.PostsubmitsStatic[orgRepo][idx].JobBase, path, config, mostUsedCluster, blocked, cm)
			if err != nil {
				return false, err
			}
			changed = changed || jobChanged
		}
	}
	for idx := range jobConfig.Periodics {
		jobChanged, err := applyDefaultClusterAssignment(&jobConfig.Periodics[idx].JobBase, path, config, mostUsedCluster, blocked, cm)
		if err != nil {
			return false, err
		}
		changed = changed || jobChanged
	}
	return changed, nil
}

func applyDefaultClusterAssignment(jobBase *prowconfig.JobBase, path string, config *dispatcher.Config, mostUsedCluster string, blocked sets.Set[string], cm dispatcher.ClusterMap) (bool, error) {
	cluster, err := determineCluster(*jobBase, config, nil, path, mostUsedCluster, blocked, cm)
	if err != nil {
		return false, err
	}
	if jobBase.Cluster == cluster {
		return false, nil
	}
	jobBase.Cluster = cluster
	return true, nil
}
