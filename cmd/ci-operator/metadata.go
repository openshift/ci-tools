package main

import (
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"

	"github.com/sirupsen/logrus"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/junit"
)

// CustomProwMetadata the name of the custom prow metadata file that's expected to be found in the artifacts directory.
const CustomProwMetadata = "custom-prow-metadata.json"

// prowResultMetadata is the set of metadata consumed by testgrid and
// gubernator after a CI run completes. We add work-namespace as our
// target namespace for the job.
//
// Example from k8s:
//
//	"metadata": {
//		"repo-commit": "253f03e0055b6649f8b25e84122748d39a284141",
//		"node_os_image": "cos-stable-65-10323-64-0",
//		"repos": {
//			"k8s.io/kubernetes": "master:1c04caa04325e1f64d9a15714ad61acdd2a81013,71936:353a0b391d6cb0c26e1c0c6b180b300f64039e0e",
//			"k8s.io/release": "master"
//		},
//		"infra-commit": "de7741746",
//		"repo": "k8s.io/kubernetes",
//		"master_os_image": "cos-stable-65-10323-64-0",
//		"job-version": "v1.14.0-alpha.0.1012+253f03e0055b66",
//		"pod": "dd8d320f-ff64-11e8-b091-0a580a6c02ef"
//	}
type prowResultMetadata struct {
	Revision      string            `json:"revision"`
	RepoCommit    string            `json:"repo-commit"`
	Repo          string            `json:"repo"`
	Repos         map[string]string `json:"repos"`
	InfraCommit   string            `json:"infra-commit"`
	JobVersion    string            `json:"job-version"`
	Pod           string            `json:"pod"`
	WorkNamespace string            `json:"work-namespace"`
	Metadata      map[string]string `json:"metadata"`
}

const metadataJSONfile = "metadata.json"

func (o *options) writeMetadataJSON() error {
	artifactDir, set := api.Artifacts()
	if !set {
		return nil
	}

	metadataJSONPath := filepath.Join(artifactDir, metadataJSONfile)

	customProwMetadataFile, err := o.findCustomMetadataFile(artifactDir)

	if err != nil {
		logrus.WithError(err).Error("Could not find custom prow metadata file.")
		return err
	}

	// If the metadata JSON exists and there's no custom prow metadata, then skip the second write.
	_, err = os.Stat(metadataJSONPath)
	if customProwMetadataFile == "" && err == nil {
		logrus.Debug("No custom metadata found and prow metadata already exists. Not updating the metadata.")
		return nil
	}

	var customMetadataErr error

	m := o.generateProwMetadata()
	if customProwMetadataFile != "" {
		m.Metadata, customMetadataErr = o.parseCustomMetadata(customProwMetadataFile)
	}

	if customMetadataErr != nil {
		logrus.WithError(err).Warn("Error parsing custom metadata.")
	}

	data, _ := json.MarshalIndent(m, "", "")
	err = api.SaveArtifact(o.censor, metadataJSONfile, data)

	if err != nil {
		return err
	} else if customMetadataErr != nil {
		return customMetadataErr
	}

	return nil
}

func (o *options) findCustomMetadataFile(artifactDir string) (customProwMetadataFile string, err error) {
	// Try to find the custom prow metadata file. We assume that there's only one. If there's more than one,
	// we'll just use the first one that we find.
	err = filepath.WalkDir(artifactDir, func(path string, info fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}

		if info.Name() == CustomProwMetadata {
			if customProwMetadataFile == "" {
				customProwMetadataFile = path
			} else {
				logrus.Error("Multiple custom prow metadata files found, which are not currently supported by ci-operator.")
			}
			return filepath.SkipDir
		}

		return nil
	})
	return customProwMetadataFile, err
}

// generateProwMetadata generates the normal prow metadata from the arguments passed into ci-operator
func (o *options) generateProwMetadata() (m prowResultMetadata) {
	o.metadataRevision++
	m.Revision = strconv.Itoa(o.metadataRevision)

	if o.jobSpec.Refs != nil {
		m.Repo = fmt.Sprintf("%s/%s", o.jobSpec.Refs.Org, o.jobSpec.Refs.Repo)
		m.Repos = map[string]string{m.Repo: o.jobSpec.Refs.String()}
	}
	if len(o.jobSpec.ExtraRefs) > 0 {
		if m.Repos == nil {
			m.Repos = make(map[string]string)
		}
		for _, ref := range o.jobSpec.ExtraRefs {
			repo := fmt.Sprintf("%s/%s", ref.Org, ref.Repo)
			if _, ok := m.Repos[repo]; ok {
				continue
			}
			m.Repos[repo] = ref.String()
		}
	}

	m.Pod = o.jobSpec.ProwJobID
	m.WorkNamespace = o.namespace

	return m
}

// parseCustomMetadata parses metadata from the custom prow metadata file
func (o *options) parseCustomMetadata(customProwMetadataFile string) (customMetadata map[string]string, err error) {
	logrus.Info("Found custom prow metadata.")

	if customJSONFile, readingError := os.ReadFile(customProwMetadataFile); readingError != nil {
		logrus.WithError(readingError).Error("Failed to read custom prow metadata.")
	} else {
		err = json.Unmarshal(customJSONFile, &customMetadata)
		if err != nil {
			logrus.WithError(err).Error("Failed to unmarshal custom prow metadata.")
		}
	}

	censoredMetadata := map[string]string{}
	for key, value := range customMetadata {
		rawKey, rawValue := []byte(key), []byte(value)
		o.censor.Censor(&rawKey)
		o.censor.Censor(&rawValue)
		censoredMetadata[string(rawKey)] = string(rawValue)
	}

	return censoredMetadata, err
}

// errWroteJUnit indicates that this error is covered by existing JUnit output and writing
// another JUnit file is not necessary (in writeFailingJUnit)
type errWroteJUnit struct {
	wrapped error
}

// Error makes an errWroteJUnit an error
func (e *errWroteJUnit) Error() string {
	return e.wrapped.Error()
}

// Unwrap allows nesting of errors
func (e *errWroteJUnit) Unwrap() error {
	return e.wrapped
}

// Is allows us to say we are an errWroteJUnit
func (e *errWroteJUnit) Is(target error) bool {
	_, is := target.(*errWroteJUnit)
	return is
}

func sortSuite(suite *junit.TestSuite) {
	sort.Slice(suite.Properties, func(i, j int) bool {
		return suite.Properties[i].Name < suite.Properties[j].Name
	})
	sort.Slice(suite.Children, func(i, j int) bool {
		return suite.Children[i].Name < suite.Children[j].Name
	})
	sort.Slice(suite.TestCases, func(i, j int) bool {
		return suite.TestCases[i].Name < suite.TestCases[j].Name
	})
	for i := range suite.Children {
		sortSuite(suite.Children[i])
	}
}

// writeFailingJUnit attempts to write a JUnit artifact when the graph could not be
// initialized in order to capture the result for higher level automation.
func (o *options) writeFailingJUnit(errs []error) {
	var testCases []*junit.TestCase
	for _, err := range errs {
		if errors.Is(err, &errWroteJUnit{}) {
			continue
		}
		testCases = append(testCases, &junit.TestCase{
			Name: "initialize",
			FailureOutput: &junit.FailureOutput{
				Output: err.Error(),
			},
		})
	}
	if len(testCases) == 0 {
		return
	}
	suites := &junit.TestSuites{
		Suites: []*junit.TestSuite{
			{
				Name:      "job",
				NumTests:  uint(len(errs)),
				NumFailed: uint(len(errs)),
				TestCases: testCases,
			},
		},
	}
	if err := o.writeJUnit(suites, "job"); err != nil {
		logrus.Trace("Unable to write top level failing JUnit artifact")
	}
}

func (o *options) writeJUnit(suites *junit.TestSuites, name string) error {
	if suites == nil {
		return nil
	}
	sort.Slice(suites.Suites, func(i, j int) bool {
		return suites.Suites[i].Name < suites.Suites[j].Name
	})
	for i := range suites.Suites {
		junit.CensorTestSuite(o.censor, suites.Suites[i])
		sortSuite(suites.Suites[i])
	}
	out, err := xml.MarshalIndent(suites, "", "  ")
	if err != nil {
		return fmt.Errorf("could not marshal jUnit XML: %w", err)
	}
	return api.SaveArtifact(o.censor, fmt.Sprintf("junit_%s.xml", name), out)
}
