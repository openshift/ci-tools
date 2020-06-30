package api

import (
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/sirupsen/logrus"
)

// IsComplete returns an error if at least one of Org, Repo, Branch members is
// empty, otherwise it returns nil
func (m *Metadata) IsComplete() error {
	var missing []string
	for item, value := range map[string]string{
		"organization": m.Org,
		"repository":   m.Repo,
		"branch":       m.Branch,
	} {
		if value == "" {
			missing = append(missing, item)
		}
	}
	sort.Strings(missing)

	if len(missing) > 0 {
		s := ""
		if len(missing) > 1 {
			s = "s"
		}
		return fmt.Errorf("missing item%s: %s", s, strings.Join(missing, ", "))
	}

	return nil
}

// TestNameFromJobName returns the name of the test from a given job name and prefix
func (m *Metadata) TestNameFromJobName(jobName, prefix string) string {
	return strings.TrimPrefix(jobName, m.JobName(prefix, ""))
}

// TestName returns a short name of a test defined in this file, including
// variant, if present
func (m *Metadata) TestName(testName string) string {
	if m.Variant == "" {
		return testName
	}
	return fmt.Sprintf("%s-%s", m.Variant, testName)
}

// JobName returns a full name of a job corresponding to a test defined in this
// file, including variant, if present
func (m *Metadata) JobName(prefix, name string) string {
	return fmt.Sprintf("%s-ci-%s-%s-%s-%s", prefix, m.Org, m.Repo, m.Branch, m.TestName(name))
}

// Basename returns the unique name for this file in the config
func (m *Metadata) Basename() string {
	basename := strings.Join([]string{m.Org, m.Repo, m.Branch}, "-")
	if m.Variant != "" {
		basename = fmt.Sprintf("%s__%s", basename, m.Variant)
	}
	return fmt.Sprintf("%s.yaml", basename)
}

// RelativePath returns the path to the config under the root config dir
func (m *Metadata) RelativePath() string {
	return path.Join(m.Org, m.Repo, m.Basename())
}

// ConfigMapName returns the configmap in which we expect this file to be uploaded
func (m *Metadata) ConfigMapName() string {
	return fmt.Sprintf("ci-operator-%s-configs", FlavorForBranch(m.Branch))
}

var ciOPConfigRegex = regexp.MustCompile(`^ci-operator-.+-configs$`)

// IsCiopConfigCM returns true if a given name is a valid ci-operator config ConfigMap
func IsCiopConfigCM(name string) bool {
	return ciOPConfigRegex.MatchString(name)
}

var threeXBranches = regexp.MustCompile(`^(release|enterprise|openshift)-3\.[0-9]+$`)
var fourXBranches = regexp.MustCompile(`^(release|enterprise|openshift)-(4\.[0-9]+)$`)

func FlavorForBranch(branch string) string {
	var flavor string
	if branch == "master" || branch == "main" {
		flavor = "master"
	} else if threeXBranches.MatchString(branch) {
		flavor = "3.x"
	} else if fourXBranches.MatchString(branch) {
		matches := fourXBranches.FindStringSubmatch(branch)
		flavor = matches[2] // the 4.x release string
	} else {
		flavor = "misc"
	}
	return flavor
}

func LogFieldsFor(metadata Metadata) logrus.Fields {
	return logrus.Fields{
		"org":     metadata.Org,
		"repo":    metadata.Repo,
		"branch":  metadata.Branch,
		"variant": metadata.Variant,
	}
}
