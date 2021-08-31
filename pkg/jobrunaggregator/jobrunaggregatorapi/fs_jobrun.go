package jobrunaggregatorapi

import (
	"context"
	"encoding/xml"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"

	"github.com/openshift/ci-tools/pkg/junit"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	prowjobv1 "k8s.io/test-infra/prow/apis/prowjobs/v1"
)

type fsJobRun struct {
	workingDirectory string

	jobName        string
	jobRunID       string
	gcsProwJobPath string
	gcsJunitPaths  []string
}

func NewFilesystemJobRun(workingDirectory, jobName, jobRunID string) (JobRunInfo, error) {
	jobRunGCSPath := filepath.Join("logs", jobName, jobRunID)
	ret := &fsJobRun{
		workingDirectory: workingDirectory,
		jobName:          jobName,
		jobRunID:         jobRunID,
		gcsProwJobPath:   filepath.Join(jobRunGCSPath, "prowjob.json"),
	}

	err := filepath.Walk(filepath.Join(workingDirectory, jobRunGCSPath),
		func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if strings.HasSuffix(path, ".xml") && strings.Contains(path, "/junit") {
				pathParts := strings.Split(path, "/")
				i := len(pathParts) - 1
				for ; i >= 0; i-- {
					if pathParts[i] == jobRunID {
						break
					}
				}
				gcsParts := append([]string{}, jobRunGCSPath)
				gcsParts = append(gcsParts, pathParts[i+1:]...)
				ret.gcsJunitPaths = append(ret.gcsJunitPaths, filepath.Join(gcsParts...))
			}
			return nil
		})
	if err != nil {
		return nil, err
	}

	return ret, nil
}

func (j *fsJobRun) GetJobName() string {
	return j.jobName
}
func (j *fsJobRun) GetJobRunID() string {
	return j.jobRunID
}
func (j *fsJobRun) GetGCSProwJobPath() string {
	return j.gcsProwJobPath
}
func (j *fsJobRun) GetGCSJunitPaths() []string {
	return j.gcsJunitPaths
}
func (j *fsJobRun) SetGCSProwJobPath(gcsProwJobPath string) {
	j.gcsProwJobPath = gcsProwJobPath
}
func (j *fsJobRun) AddGCSJunitPaths(junitPaths ...string) {
	j.gcsJunitPaths = append(j.gcsJunitPaths, junitPaths...)
}

func (j *fsJobRun) WriteCache(ctx context.Context, parentDir string) error {
	if err := j.writeCache(ctx, parentDir); err != nil {
		// attempt to remove the dir so we don't leave half the content serialized out
		_ = os.Remove(parentDir)
		return err
	}

	return nil
}

func (j *fsJobRun) writeCache(ctx context.Context, parentDir string) error {
	prowJob, err := j.GetProwJob(ctx)
	if err != nil {
		return err
	}
	prowJobBytes, err := SerializeProwJob(prowJob)
	if err != nil {
		return err
	}

	contentMap, err := j.GetAllContent(ctx)
	if err != nil {
		return err
	}
	for path, content := range contentMap {
		currentTargetFilename := filepath.Join(parentDir, path)
		immediateParentDir := filepath.Dir(currentTargetFilename)
		if err := os.MkdirAll(immediateParentDir, 0755); err != nil {
			return err
		}
		if err := ioutil.WriteFile(currentTargetFilename, content, 0644); err != nil {
			return err
		}

		if strings.HasSuffix(currentTargetFilename, "prowjob.json") {
			if err := ioutil.WriteFile(filepath.Join(immediateParentDir, "prowjob.yaml"), prowJobBytes, 0644); err != nil {
				return err
			}
		}
	}

	return nil
}

func (j *fsJobRun) GetProwJob(ctx context.Context) (*prowjobv1.ProwJob, error) {
	if len(j.gcsProwJobPath) == 0 {
		return nil, fmt.Errorf("missing prowjob path")
	}
	prowBytes, err := j.GetContent(ctx, j.gcsProwJobPath)
	if err != nil {
		return nil, err
	}
	return ParseProwJob(prowBytes)
}

func (j *fsJobRun) GetCombinedJUnitTestSuites(ctx context.Context) (*junit.TestSuites, error) {
	testSuites := &junit.TestSuites{}
	for _, junitFile := range j.GetGCSJunitPaths() {
		junitContent, err := j.GetContent(ctx, junitFile)
		if err != nil {
			return nil, fmt.Errorf("error getting content for %q %q: %w", j.GetJobRunID(), junitFile, err)
		}

		// try as testsuites first just in case we are one
		currTestSuites := &junit.TestSuites{}
		testSuitesErr := xml.Unmarshal(junitContent, currTestSuites)
		if testSuitesErr == nil {
			// if this a test suites, add them here
			testSuites.Suites = append(testSuites.Suites, currTestSuites.Suites...)
			continue
		}

		currTestSuite := &junit.TestSuite{}
		if testSuiteErr := xml.Unmarshal(junitContent, currTestSuite); testSuiteErr != nil {
			return nil, fmt.Errorf("error parsing junit for %q %q: %v then %w", j.GetJobRunID(), junitFile, testSuitesErr, testSuiteErr)
		}
		testSuites.Suites = append(testSuites.Suites, currTestSuite)
	}

	return testSuites, nil
}

func (j *fsJobRun) GetContent(ctx context.Context, path string) ([]byte, error) {
	if len(path) == 0 {
		return nil, fmt.Errorf("missing path")
	}
	content, originalErr := ioutil.ReadFile(filepath.Join(j.workingDirectory, path))
	if originalErr == nil {
		return content, nil
	}

	content, err := ioutil.ReadFile(filepath.Join(j.workingDirectory, "logs", j.jobName, j.jobRunID, path))
	if err != nil {
		return nil, utilerrors.NewAggregate([]error{originalErr, err})
	}

	return content, nil
}

func (j *fsJobRun) GetAllContent(ctx context.Context) (map[string][]byte, error) {
	errs := []error{}
	ret := map[string][]byte{}

	allPaths := []string{j.gcsProwJobPath}
	allPaths = append(allPaths, j.gcsJunitPaths...)
	for _, path := range allPaths {
		var err error
		ret[path], err = j.GetContent(ctx, path)
		if err != nil {
			errs = append(errs, err)
		}
	}
	err := utilerrors.NewAggregate(errs)
	if err != nil {
		return nil, err
	}

	return ret, nil
}
func (j *fsJobRun) ClearAllContent() {
}

func (j *fsJobRun) GetHumanURL() string {
	return GetHumanURL(j.GetJobName(), j.GetJobRunID())
}

func (j *fsJobRun) GetGCSArtifactURL() string {
	return GetGCSArtifactURL(j.GetJobName(), j.GetJobRunID())
}
