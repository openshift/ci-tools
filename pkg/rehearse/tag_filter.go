package rehearse

import (
	"regexp"

	"github.com/sirupsen/logrus"

	"sigs.k8s.io/prow/pkg/config"

	ci_config "github.com/openshift/ci-tools/pkg/config"
)

// FilterPresubmitsByTag filters the given presubmits based on the selectors for the requested tag.
func FilterPresubmitsByTag(presubmits ci_config.Presubmits, periodics []config.Periodic, tagConfig *RehearsalTagConfig, requestedTag string) ci_config.Presubmits {
	filtered := make(ci_config.Presubmits)
	var targetTag *Tag
	for i := range tagConfig.Tags {
		if tagConfig.Tags[i].Name == requestedTag {
			targetTag = &tagConfig.Tags[i]
			break
		}
	}

	if targetTag == nil {
		return filtered
	}

	for repo, jobs := range presubmits {
		for _, job := range jobs {
			if jobMatchesTag(job.JobBase, targetTag, repo) {
				if _, ok := filtered[repo]; !ok {
					filtered[repo] = []config.Presubmit{}
				}
				filtered[repo] = append(filtered[repo], job)
			}
		}
	}

	for _, periodic := range periodics {
		if jobMatchesTag(periodic.JobBase, targetTag, "") {
			logrus.WithField("job", periodic.Name).Warn("Periodic jobs cannot be rehearsed by tag, skipping.")
		}
	}

	return filtered
}

func jobMatchesTag(job config.JobBase, tag *Tag, repo string) bool {
	for _, selector := range tag.Selectors {
		// Check job name pattern
		if selector.JobNamePattern != "" {
			re, err := regexp.Compile(selector.JobNamePattern)
			if err != nil {
				logrus.WithError(err).Warnf("Invalid regex in rehearsal tag selector: %s", selector.JobNamePattern)
				continue
			}
			if re.MatchString(job.Name) {
				return true
			}
		}

		// Check exact job name match
		if selector.JobName != "" {
			if job.Name == selector.JobName {
				return true
			}
		}

		// Check cluster profile (stored in job labels)
		if selector.ClusterProfile != "" {
			if clusterProfile, ok := job.Labels["ci-operator.openshift.io/cloud-cluster-profile"]; ok {
				if clusterProfile == selector.ClusterProfile {
					return true
				}
			}
		}

		// Check file path pattern (matches against the repository path)
		if selector.FilePathPattern != "" && repo != "" {
			re, err := regexp.Compile(selector.FilePathPattern)
			if err != nil {
				logrus.WithError(err).Warnf("Invalid regex in rehearsal tag selector: %s", selector.FilePathPattern)
				continue
			}
			// The repo string is in format "org/repo-name", we can match against this
			if re.MatchString(repo) {
				return true
			}
		}
	}
	return false
}

// RehearsalTagConfig contains the mapping of tags to jobs
