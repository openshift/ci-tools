package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/sirupsen/logrus"

	prowconfig "sigs.k8s.io/prow/pkg/config"

	jc "github.com/openshift/ci-tools/pkg/jobconfig"
)

func writeEphemeralJobs(ephemeralDir, org, repo string, prNumber int, allJobs *prowconfig.JobConfig) error {
	if err := os.MkdirAll(ephemeralDir, os.ModePerm); err != nil {
		return fmt.Errorf("could not create ephemeral directory: %w", err)
	}
	filename := fmt.Sprintf("pr%d-%s-%s-ephemeral.yaml", prNumber, org, repo)
	return jc.WriteToFile(filepath.Join(ephemeralDir, filename), allJobs)
}

func filterNewJobs(allJobs *prowconfig.JobConfig, jobConfigDir, org, repo string, logger *logrus.Entry) ([]string, []prowconfig.Presubmit, []prowconfig.Periodic) {
	existingNames := loadExistingJobNames(jobConfigDir, org, repo, logger)

	var newJobNames []string
	var newPresubmits []prowconfig.Presubmit
	var newPeriodics []prowconfig.Periodic
	orgrepo := fmt.Sprintf("%s/%s", org, repo)
	for _, j := range allJobs.PresubmitsStatic[orgrepo] {
		if !existingNames[j.Name] {
			newJobNames = append(newJobNames, j.Name)
			newPresubmits = append(newPresubmits, j)
		}
	}
	for _, j := range allJobs.PostsubmitsStatic[orgrepo] {
		if !existingNames[j.Name] {
			newJobNames = append(newJobNames, j.Name)
		}
	}
	for _, j := range allJobs.Periodics {
		if !existingNames[j.Name] {
			newJobNames = append(newJobNames, j.Name)
			newPeriodics = append(newPeriodics, j)
		}
	}
	return newJobNames, newPresubmits, newPeriodics
}

func loadExistingJobNames(jobConfigDir, org, repo string, logger *logrus.Entry) map[string]bool {
	names := map[string]bool{}
	permanentPath := filepath.Join(jobConfigDir, org, repo)
	if !dirExists(permanentPath) {
		return names
	}
	existing, err := jc.ReadFromDir(permanentPath)
	if err != nil {
		logger.WithError(err).Warn("could not read existing jobs from EFS")
		return names
	}
	for _, jobs := range existing.PresubmitsStatic {
		for _, j := range jobs {
			names[j.Name] = true
		}
	}
	for _, jobs := range existing.PostsubmitsStatic {
		for _, j := range jobs {
			names[j.Name] = true
		}
	}
	for _, j := range existing.Periodics {
		names[j.Name] = true
	}
	return names
}

// --- Ephemeral GC ---

type ephemeralPRDir struct {
	org, repo string
	prNum     int
	path      string
}

func (s *server) startEphemeralGC(ctx context.Context, interval time.Duration, logger *logrus.Entry) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.gcEphemeralDirs(logger)
		}
	}
}

func (s *server) gcEphemeralDirs(logger *logrus.Entry) {
	ephemeralRoot := filepath.Join(s.jobConfigDir, "ephemeral")
	if !dirExists(ephemeralRoot) {
		return
	}

	for _, entry := range listEphemeralPRDirs(ephemeralRoot, logger) {
		pr, err := s.ghc.GetPullRequest(entry.org, entry.repo, entry.prNum)
		if err != nil {
			logger.WithError(err).WithFields(logrus.Fields{
				"org": entry.org, "repo": entry.repo, "pr": entry.prNum,
			}).Warn("could not check PR state for GC")
			continue
		}
		if pr.State != "closed" {
			continue
		}
		lock := s.repoLock(entry.org, entry.repo)
		lock.Lock()
		err = os.RemoveAll(entry.path)
		lock.Unlock()
		if err != nil {
			logger.WithError(err).WithField("path", entry.path).Warn("could not remove stale ephemeral directory")
		} else {
			logger.WithFields(logrus.Fields{
				"org": entry.org, "repo": entry.repo, "pr": entry.prNum,
			}).Info("GC removed stale ephemeral directory for closed PR")
		}
	}
}

func listEphemeralPRDirs(root string, logger *logrus.Entry) []ephemeralPRDir {
	var result []ephemeralPRDir
	orgs, err := os.ReadDir(root)
	if err != nil {
		logger.WithError(err).Warn("could not list ephemeral directory for GC")
		return nil
	}
	for _, orgEntry := range orgs {
		if !orgEntry.IsDir() {
			continue
		}
		repos, _ := os.ReadDir(filepath.Join(root, orgEntry.Name()))
		for _, repoEntry := range repos {
			if !repoEntry.IsDir() {
				continue
			}
			prDirs, _ := os.ReadDir(filepath.Join(root, orgEntry.Name(), repoEntry.Name()))
			for _, prDir := range prDirs {
				if !prDir.IsDir() || !strings.HasPrefix(prDir.Name(), "PR-") {
					continue
				}
				prNum, err := strconv.Atoi(strings.TrimPrefix(prDir.Name(), "PR-"))
				if err != nil {
					continue
				}
				result = append(result, ephemeralPRDir{
					org:   orgEntry.Name(),
					repo:  repoEntry.Name(),
					prNum: prNum,
					path:  filepath.Join(root, orgEntry.Name(), repoEntry.Name(), prDir.Name()),
				})
			}
		}
	}
	return result
}
