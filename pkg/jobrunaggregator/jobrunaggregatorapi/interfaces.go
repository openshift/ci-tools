package jobrunaggregatorapi

import (
	"bytes"
	"context"

	goyaml "gopkg.in/yaml.v2"

	"k8s.io/apimachinery/pkg/util/yaml"
	prowjobv1 "k8s.io/test-infra/prow/apis/prowjobs/v1"

	"github.com/openshift/ci-tools/pkg/junit"
)

// JobRunInfo is a way to interact with JobRuns and gather their junit results.
// The backing store can vary by impl, GCS buckets are the only implementation today.
type JobRunInfo interface {
	IsFinished(ctx context.Context) bool

	GetJobName() string
	GetJobRunID() string

	// GetHumanURL returns prow job URL for this job run.
	GetHumanURL() string

	// GetGCSArtifactURL returns the URL for this job run's artifacts in GCS.
	GetGCSArtifactURL() string

	GetGCSProwJobPath() string
	GetGCSJunitPaths() []string
	SetGCSProwJobPath(gcsProwJobPath string)
	AddGCSJunitPaths(junitPaths ...string)
	AddGCSProwJobFileNames(fileNames ...string)

	GetProwJob(ctx context.Context) (*prowjobv1.ProwJob, error)
	GetJobRunFromGCS(ctx context.Context) error
	GetCombinedJUnitTestSuites(ctx context.Context) (*junit.TestSuites, error)
	// GetOpenShiftTestsFilesWithPrefix checks the datasource for "openshift-e2e-test/artifacts/junit/<prefix>*"
	// and returns that content indexed by local filename.  This is useful for things like back-disruption and alerts.
	GetOpenShiftTestsFilesWithPrefix(ctx context.Context, prefix string) (map[string]string, error)
	GetContent(ctx context.Context, path string) ([]byte, error)
	ClearAllContent()

	WriteCache(ctx context.Context, parentDir string) error
}

func ParseProwJob(prowJobBytes []byte) (*prowjobv1.ProwJob, error) {
	prowJob := &prowjobv1.ProwJob{}
	err := yaml.NewYAMLOrJSONDecoder(bytes.NewBuffer(prowJobBytes), 4096).Decode(&prowJob)
	if err != nil {
		return nil, err
	}
	prowJob.ManagedFields = nil

	return prowJob, nil
}

func SerializeProwJob(prowJob *prowjobv1.ProwJob) ([]byte, error) {
	buf := &bytes.Buffer{}
	prowJobWriter := goyaml.NewEncoder(buf)
	if err := prowJobWriter.Encode(prowJob); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
