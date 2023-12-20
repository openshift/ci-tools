package jobrunaggregatorapi

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"cloud.google.com/go/storage"
	"github.com/sirupsen/logrus"
	"google.golang.org/api/iterator"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	prowjobv1 "k8s.io/test-infra/prow/apis/prowjobs/v1"

	"github.com/openshift/ci-tools/pkg/junit"
)

type gcsJobRun struct {
	// retrieval mechanisms
	bkt *storage.BucketHandle

	jobRunGCSBucketRoot string
	jobName             string
	jobRunID            string
	gcsProwJobPath      string
	gcsJunitPaths       []string
	gcsFileNames        []string

	pathToContent map[string][]byte

	jobRunGCSBucket string
}

func NewGCSJobRun(bkt *storage.BucketHandle, jobGCSBucketRoot string, jobName, jobRunID string, jobRunGCSBucket string) JobRunInfo {
	return &gcsJobRun{
		bkt:                 bkt,
		jobRunGCSBucketRoot: path.Join(jobGCSBucketRoot, jobRunID),
		jobName:             jobName,
		jobRunID:            jobRunID,
		jobRunGCSBucket:     jobRunGCSBucket,
	}
}

func (j *gcsJobRun) GetJobName() string {
	return j.jobName
}
func (j *gcsJobRun) GetJobRunID() string {
	return j.jobRunID
}
func (j *gcsJobRun) GetGCSProwJobPath() string {
	return j.gcsProwJobPath
}
func (j *gcsJobRun) GetGCSJunitPaths() []string {
	return j.gcsJunitPaths
}
func (j *gcsJobRun) SetGCSProwJobPath(gcsProwJobPath string) {
	j.gcsProwJobPath = gcsProwJobPath
}
func (j *gcsJobRun) AddGCSJunitPaths(junitPaths ...string) {
	j.gcsJunitPaths = append(j.gcsJunitPaths, junitPaths...)
}
func (j *gcsJobRun) AddGCSProwJobFileNames(fileNames ...string) {
	j.gcsFileNames = append(j.gcsFileNames, fileNames...)
}

func (j *gcsJobRun) WriteCache(ctx context.Context, parentDir string) error {
	if err := j.writeCache(ctx, parentDir); err != nil {
		// attempt to remove the dir so we don't leave half the content serialized out
		_ = os.Remove(parentDir)
		return err
	}

	return nil
}

func (j *gcsJobRun) writeCache(ctx context.Context, parentDir string) error {
	prowJob, err := j.GetProwJob(ctx)
	if err != nil {
		return err
	}
	prowJobBytes, err := SerializeProwJob(prowJob)
	if err != nil {
		return fmt.Errorf("error serializing prowjob for %q: %w", j.GetJobRunID(), err)
	}

	contentMap, err := j.getAllContent(ctx)
	if err != nil {
		return err
	}
	jobRunDir := parentDir
	for path, content := range contentMap {
		currentTargetFilename := filepath.Join(parentDir, path)
		immediateParentDir := filepath.Dir(currentTargetFilename)
		if err := os.MkdirAll(immediateParentDir, 0755); err != nil {
			return fmt.Errorf("error making directory for %q: %w", j.GetJobRunID(), err)
		}
		if err := os.WriteFile(currentTargetFilename, content, 0644); err != nil {
			return fmt.Errorf("error writing file for %q %q: %w", j.GetJobRunID(), currentTargetFilename, err)
		}

		if strings.HasSuffix(currentTargetFilename, "prowjob.json") {
			jobRunDir = immediateParentDir
			if err := os.WriteFile(filepath.Join(immediateParentDir, "prowjob.yaml"), prowJobBytes, 0644); err != nil {
				return err
			}
		}
	}

	testSuites := &junit.TestSuites{}
	for _, junitFile := range j.GetGCSJunitPaths() {
		junitContent, err := j.GetContent(ctx, junitFile)
		if err != nil {
			return fmt.Errorf("error getting content for %q %q: %w", j.GetJobRunID(), junitFile, err)
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
			return fmt.Errorf("error parsing junit for %q %q: %w", j.GetJobRunID(), junitFile, testSuiteErr)
		}
		testSuites.Suites = append(testSuites.Suites, currTestSuite)
	}

	// write aggregated junit as well.
	combinedJunitContent, err := xml.Marshal(testSuites)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(jobRunDir, "junit-combined-testsuites.xml"), combinedJunitContent, 0644); err != nil {
		return err
	}

	return nil
}

func (j *gcsJobRun) GetJobRunFromGCS(ctx context.Context) error {
	query := &storage.Query{
		// This ends up being the equivalent of:
		// https://gcsweb-ci.apps.ci.l2s4.p1.openshiftapps.com/gcs/test-platform-results/logs/periodic-ci-openshift-release-master-nightly-4.9-upgrade-from-stable-4.8-e2e-metal-ipi-upgrade/1671747590984568832
		// the next directory step is based on some bit of metadata I don't recognize
		Prefix: j.jobRunGCSBucketRoot,

		// TODO this field is apparently missing from this level of go/storage
		// Omit owner and ACL fields for performance
		// Projection: storage.ProjectionNoACL,
	}

	// Only retrieve the name and creation time for performance
	if err := query.SetAttrSelection([]string{"Name", "Created"}); err != nil {
		return err
	}

	// Returns an iterator which iterates over the bucket query results.
	// this will list *all* files with the query prefix.
	it := j.bkt.Objects(ctx, query)

	// Find the query results we're the most interested in.
	for {
		attrs, err := it.Next()
		if err == iterator.Done {
			// we're done adding values
			break
		}
		if err != nil {
			return err
		}

		// if we have a directory then skip
		if len(attrs.Name) == 0 {
			continue
		}

		// add the name
		j.AddGCSProwJobFileNames(attrs.Name)

		// see if it is a junit
		if strings.HasSuffix(attrs.Name, ".xml") && strings.Contains(attrs.Name, "/junit") {
			logrus.Debugf("found %s", attrs.Name)
			j.AddGCSJunitPaths(attrs.Name)
		}
	}

	return nil
}

func (j *gcsJobRun) validateJobRunFromGCS(ctx context.Context) error {
	if nil == j.gcsFileNames {
		return j.GetJobRunFromGCS(ctx)
	}

	return nil
}

func (j *gcsJobRun) GetCombinedJUnitTestSuites(ctx context.Context) (*junit.TestSuites, error) {

	// verifies we have loaded the available file for the job run
	err := j.validateJobRunFromGCS(ctx)

	if err != nil {
		return nil, err
	}

	testSuites := &junit.TestSuites{}
	for _, junitFile := range j.GetGCSJunitPaths() {
		logrus.Debug("getting junit file content content from GCS")
		junitContent, err := j.GetContent(ctx, junitFile)
		if err != nil {
			return nil, fmt.Errorf("error getting content for jobrun/%v/%v %q: %w", j.GetJobName(), j.GetJobRunID(), junitFile, err)
		}
		// if the file was retrieve, but the content was empty, there is no work to be done.
		if len(junitContent) == 0 {
			continue
		}

		// try as testsuites first just in case we are one
		currTestSuites := &junit.TestSuites{}
		testSuitesErr := xml.Unmarshal(junitContent, currTestSuites)
		if testSuitesErr == nil {
			// if this a test suites, add them here
			testSuites.Suites = append(testSuites.Suites, currTestSuites.Suites...)
			continue
		}
		if isParseFloatError(testSuitesErr) {
			// this was a testsuites, but we cannot read the file.  There is no choice to ignore errors so we suppress here
			fmt.Fprintf(os.Stderr, "error parsing testsuites: %v", testSuitesErr)
			continue
		}

		currTestSuite := &junit.TestSuite{}
		if testSuiteErr := xml.Unmarshal(junitContent, currTestSuite); testSuiteErr != nil {
			// If we get an error reading from just one of the junits, don't end the world, just log it.
			fmt.Printf("error parsing junit for jobrun/%v/%v %q: testsuiteError=%v  testsuitesError=%v",
				j.GetJobName(), j.GetJobRunID(), junitFile, testSuiteErr.Error(), testSuitesErr.Error())
			continue
		}
		testSuites.Suites = append(testSuites.Suites, currTestSuite)
	}

	return testSuites, nil
}

func isParseFloatError(err error) bool {
	if err == nil {
		return false
	}
	if strings.HasPrefix(err.Error(), "strconv.ParseFloat:") {
		return true
	}
	return false
}

func (j *gcsJobRun) GetOpenShiftTestsFilesWithPrefix(ctx context.Context, prefix string) (map[string]string, error) {

	// verifies we have loaded the available file for the job run
	err := j.validateJobRunFromGCS(ctx)

	if err != nil {
		return nil, err
	}

	fileNames := j.gcsFileNames

	regex, err := regexp.Compile("/" + prefix + "[^/]*")
	if err != nil {
		return nil, err
	}
	ret := map[string]string{}

	// Find the query results we're the most interested in.
	for _, name := range fileNames {

		if !regex.MatchString(name) {
			continue
		}

		content, err := j.getCurrentContent(ctx, name)
		if err != nil {
			return nil, err
		}
		ret[name] = string(content)
	}

	return ret, nil
}

func (j *gcsJobRun) GetProwJob(ctx context.Context) (*prowjobv1.ProwJob, error) {
	if len(j.gcsProwJobPath) == 0 {
		return nil, fmt.Errorf("missing prowjob path to GCS content for jobrun/%v/%v", j.GetJobName(), j.GetJobRunID())
	}
	logrus.Infof("Fetching latest prowjob content from gcs: %s", j.gcsProwJobPath)
	prowBytes, err := j.getCurrentContent(ctx, j.gcsProwJobPath)
	if err != nil {
		return nil, err
	}
	return ParseProwJob(prowBytes)
}

func (j *gcsJobRun) GetContent(ctx context.Context, path string) ([]byte, error) {
	if len(path) == 0 {
		return nil, fmt.Errorf("missing path to GCS content for jobrun/%v/%v", j.GetJobName(), j.GetJobRunID())
	}
	if content, ok := j.pathToContent[path]; ok {
		return content, nil
	}

	newContent, err := j.getCurrentContent(ctx, path)
	if err != nil {
		return nil, err
	}
	if j.pathToContent == nil {
		j.pathToContent = map[string][]byte{}
	}
	j.pathToContent[path] = newContent

	return newContent, nil
}

func (j *gcsJobRun) getCurrentContent(ctx context.Context, path string) ([]byte, error) {
	// Get an Object handle for the path
	obj := j.bkt.Object(path)

	// use the object attributes to try to get the latest generation to try to retrieve the data without getting a cached
	// version of data that does not match the latest content.  I don't know if this will work, but in the easy case
	// it doesn't seem to fail.
	objAttrs, err := obj.Attrs(ctx)
	if err != nil {
		return nil, fmt.Errorf("error reading GCS attributes for jobrun/%v/%v at %q: %w", j.GetJobName(), j.GetJobRunID(), path, err)
	}
	obj = obj.Generation(objAttrs.Generation)

	// Get an io.Reader for the object.
	gcsReader, err := obj.NewReader(ctx)
	if err != nil {
		return nil, fmt.Errorf("error reading GCS content for jobrun/%v/%v at %q: %w", j.GetJobName(), j.GetJobRunID(), path, err)
	}
	defer gcsReader.Close()

	return io.ReadAll(gcsReader)

}

func (j *gcsJobRun) getAllContent(ctx context.Context) (map[string][]byte, error) {
	if len(j.pathToContent) > 0 {
		return j.pathToContent, nil
	}

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

	j.pathToContent = ret

	return ret, nil
}

func (j *gcsJobRun) ClearAllContent() {
	j.pathToContent = nil
}

func (j *gcsJobRun) GetHumanURL() string {
	return GetHumanURLForLocation(j.jobRunGCSBucketRoot, j.jobRunGCSBucket)
}

func (j *gcsJobRun) GetGCSArtifactURL() string {
	return GetGCSArtifactURLForLocation(j.jobRunGCSBucketRoot, j.jobRunGCSBucket)
}

func (j *gcsJobRun) IsFinished(ctx context.Context) bool {
	content, err := j.GetContent(ctx, fmt.Sprintf("%s/finished.json", j.jobRunGCSBucketRoot))
	if err != nil {
		return false
	}
	if len(content) == 0 {
		return false
	}

	return true
}

func GetHumanURLForLocation(jobRunGCSBucketRoot, jobRunGCSBucket string) string {
	// https://prow.ci.openshift.org/view/gs/test-platform-results/logs/periodic-ci-openshift-release-master-ci-4.8-e2e-gcp-upgrade/1429691282619371520
	return fmt.Sprintf("https://prow.ci.openshift.org/view/gs/%s/%s", jobRunGCSBucket, jobRunGCSBucketRoot)
}

func GetGCSArtifactURLForLocation(jobRunGCSBucketRoot, jobRunGCSBucket string) string {
	// https://gcsweb-ci.apps.ci.l2s4.p1.openshiftapps.com/gcs/test-platform-results/logs/periodic-ci-openshift-release-master-ci-4.9-e2e-gcp-upgrade/1420676206029705216/artifacts/e2e-gcp-upgrade/
	return fmt.Sprintf("https://gcsweb-ci.apps.ci.l2s4.p1.openshiftapps.com/gcs/%s/%s/artifacts", jobRunGCSBucket, jobRunGCSBucketRoot)
}
