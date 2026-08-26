package jobrunaggregatorapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"cloud.google.com/go/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/api/option"
)

type fakeGCSObjectServer struct {
	mu       sync.Mutex
	objects  map[string][]byte
	requests map[string]int
}

func (s *fakeGCSObjectServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	const objectPath = "/b/test-bucket/o/"
	objectPathIndex := strings.Index(r.URL.Path, objectPath)
	mediaRequest := false
	encodedName := ""
	if objectPathIndex != -1 {
		encodedName = r.URL.Path[objectPathIndex+len(objectPath):]
	} else if strings.HasPrefix(r.URL.Path, "/test-bucket/") {
		// The storage client uses the XML path-style endpoint for media reads.
		encodedName = strings.TrimPrefix(r.URL.Path, "/test-bucket/")
		mediaRequest = true
	}
	if r.Method != http.MethodGet || encodedName == "" {
		http.NotFound(w, r)
		return
	}

	name, err := url.PathUnescape(encodedName)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	content, found := s.objects[name]
	s.requests[name]++
	s.mu.Unlock()
	if !found {
		http.Error(w, `{"error":{"code":404,"message":"not found"}}`, http.StatusNotFound)
		return
	}

	if mediaRequest || r.URL.Query().Get("alt") == "media" {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(content)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]string{
		"bucket":     "test-bucket",
		"name":       name,
		"generation": "1",
		"size":       "1",
	}); err != nil {
		http.Error(w, "failed to encode fake GCS response", http.StatusInternalServerError)
	}
}

func (s *fakeGCSObjectServer) set(name string, content []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.objects[name] = content
}

func (s *fakeGCSObjectServer) requestCount(name string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.requests[name]
}

func newTestGCSJobRun(t *testing.T, objects map[string][]byte) (*gcsJobRun, *fakeGCSObjectServer) {
	t.Helper()
	fakeGCS := &fakeGCSObjectServer{objects: objects, requests: map[string]int{}}
	server := httptest.NewServer(fakeGCS)
	t.Cleanup(server.Close)

	client, err := storage.NewClient(context.Background(), option.WithEndpoint(server.URL), option.WithoutAuthentication())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, client.Close()) })

	return &gcsJobRun{
		bkt:                 client.Bucket("test-bucket"),
		jobRunGCSBucketRoot: "logs/job/123",
		jobName:             "job",
		jobRunID:            "123",
		jobRunGCSBucket:     "test-bucket",
	}, fakeGCS
}

func TestGetCombinedJUnitTestSuitesBypassesContentCache(t *testing.T) {
	const junitPath = "logs/job/123/artifacts/junit.xml"
	jobRun, fakeGCS := newTestGCSJobRun(t, map[string][]byte{
		junitPath: []byte(`<testsuite name="fresh"><testcase name="test"/></testsuite>`),
	})
	jobRun.gcsFileNames = []string{junitPath}
	jobRun.gcsJunitPaths = []string{junitPath}
	jobRun.pathToContent = map[string][]byte{
		junitPath: []byte(`<testsuite name="stale"/>`),
	}

	testSuites, err := jobRun.GetCombinedJUnitTestSuites(context.Background())
	require.NoError(t, err)
	require.Len(t, testSuites.Suites, 1)
	assert.Equal(t, "fresh", testSuites.Suites[0].Name)
	assert.Equal(t, []byte(`<testsuite name="stale"/>`), jobRun.pathToContent[junitPath], "direct JUnit reads must neither consult nor replace cached raw content")
	firstRequestCount := fakeGCS.requestCount(junitPath)
	assert.GreaterOrEqual(t, firstRequestCount, 2, "a current read gets object attributes and content")

	fakeGCS.set(junitPath, []byte(`<testsuite name="newer"/>`))
	testSuites, err = jobRun.GetCombinedJUnitTestSuites(context.Background())
	require.NoError(t, err)
	require.Len(t, testSuites.Suites, 1)
	assert.Equal(t, "newer", testSuites.Suites[0].Name)
	assert.Greater(t, fakeGCS.requestCount(junitPath), firstRequestCount)
	assert.Equal(t, []byte(`<testsuite name="stale"/>`), jobRun.pathToContent[junitPath])
}

func TestGetAllContentReconstructsPartialCache(t *testing.T) {
	const (
		prowJobPath = "logs/job/123/prowjob.json"
		junitPath   = "logs/job/123/artifacts/junit.xml"
	)
	jobRun, fakeGCS := newTestGCSJobRun(t, map[string][]byte{
		prowJobPath: []byte("prowjob from GCS"),
		junitPath:   []byte("junit from GCS"),
	})
	jobRun.gcsProwJobPath = prowJobPath
	jobRun.gcsJunitPaths = []string{junitPath}
	jobRun.pathToContent = map[string][]byte{
		prowJobPath:                  []byte("cached prowjob"),
		"logs/job/123/finished.json": []byte("true"),
	}

	got, err := jobRun.getAllContent(context.Background())
	require.NoError(t, err)
	assert.Equal(t, map[string][]byte{
		prowJobPath: []byte("cached prowjob"),
		junitPath:   []byte("junit from GCS"),
	}, got)
	assert.Equal(t, got, jobRun.pathToContent, "the cache should be rebuilt from exactly the required files")
	assert.Zero(t, fakeGCS.requestCount(prowJobPath), "already cached required content should be reused")
	assert.GreaterOrEqual(t, fakeGCS.requestCount(junitPath), 2, "missing required content should be fetched")
}
