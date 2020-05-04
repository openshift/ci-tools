package agents

import (
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/load"
)

func TestGetFromIndex(t *testing.T) {
	indexName := "index-a"
	indexKey := "index-key"

	testCases := []struct {
		name           string
		agent          *configAgent
		expectedResult []*api.ReleaseBuildConfiguration
		expectedError  string
	}{
		{
			name:          "Given index does not exist",
			agent:         &configAgent{lock: &sync.RWMutex{}},
			expectedError: "no index index-a configured",
		},
		{
			name: "Happy path",
			agent: &configAgent{
				lock: &sync.RWMutex{},
				indexes: map[string]configIndex{
					indexName: {indexKey: []*api.ReleaseBuildConfiguration{{TestBinaryBuildCommands: "make test"}}},
				},
			},
			expectedResult: []*api.ReleaseBuildConfiguration{{TestBinaryBuildCommands: "make test"}},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			errMsg := ""
			res, err := tc.agent.GetFromIndex(indexName, indexKey)
			if err != nil {
				errMsg = err.Error()
			}
			if tc.expectedError != errMsg {
				t.Fatalf("got error %q expected error %q", errMsg, tc.expectedError)
			}
			if diff := cmp.Diff(tc.expectedResult, res); diff != "" {
				t.Errorf("expected result does not match actual result, diff: %v", diff)
			}
		})
	}
}
func TestGetFromIndex_threadsafety(t *testing.T) {
	agent := &configAgent{
		lock: &sync.RWMutex{},
		indexes: map[string]configIndex{
			"index": {"key": []*api.ReleaseBuildConfiguration{{TestBinaryBuildCommands: "make test"}}},
		},
	}

	wg := &sync.WaitGroup{}
	for i := 0; i < 2; i++ {
		wg.Add(2)

		go func() { _, _ = agent.GetFromIndex("bla", "blub"); wg.Done() }()
		go func() {
			_ = agent.AddIndex("foo", func(_ api.ReleaseBuildConfiguration) []string {
				return []string{"bar"}
			})
			wg.Done()
		}()
	}
	wg.Wait()

}

func TestAddIndex(t *testing.T) {
	agent := &configAgent{
		lock: &sync.RWMutex{},
		indexFuncs: map[string]IndexFn{
			"exists": func(_ api.ReleaseBuildConfiguration) []string { return nil },
		},
	}
	testCases := []struct {
		name          string
		indexFnName   string
		expectedError string
	}{
		{
			name:        "Happy path",
			indexFnName: "new",
		},
		{
			name:          "Index already exists",
			indexFnName:   "exists",
			expectedError: `there is already an index named "exists"`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tc := tc
			// Run in parallel so race detector can do its job
			t.Parallel()

			errMsg := ""
			err := agent.AddIndex(tc.indexFnName, func(_ api.ReleaseBuildConfiguration) []string { return nil })
			if err != nil {
				errMsg = err.Error()
			}

			if errMsg != tc.expectedError {
				t.Errorf("expected error %s, got error %s", tc.expectedError, errMsg)
			}
		})
	}
}

func TestBuildIndexes(t *testing.T) {

	cfg := api.ReleaseBuildConfiguration{TestBinaryBuildCommands: "make test"}
	testCases := []struct {
		name     string
		agent    *configAgent
		configs  load.FilenameToConfig
		expected map[string]configIndex
	}{
		{
			name: "single index",
			agent: &configAgent{
				indexFuncs: map[string]IndexFn{
					"index-a": func(_ api.ReleaseBuildConfiguration) []string { return []string{"key-a"} },
				},
			},
			configs:  load.FilenameToConfig{"myfile.yaml": cfg},
			expected: map[string]configIndex{"index-a": {"key-a": []*api.ReleaseBuildConfiguration{&cfg}}},
		},
		{
			name: "multiple indexes",
			agent: &configAgent{
				indexFuncs: map[string]IndexFn{
					"index-a": func(_ api.ReleaseBuildConfiguration) []string { return []string{"key-a"} },
					"index-b": func(_ api.ReleaseBuildConfiguration) []string { return []string{"key-b"} },
				},
			},
			configs: load.FilenameToConfig{"myfile.yaml": cfg},
			expected: map[string]configIndex{
				"index-a": {"key-a": []*api.ReleaseBuildConfiguration{&cfg}},
				"index-b": {"key-b": []*api.ReleaseBuildConfiguration{&cfg}},
			},
		},
		{
			name: "no result indexer",
			agent: &configAgent{
				indexFuncs: map[string]IndexFn{
					"index-a": func(_ api.ReleaseBuildConfiguration) []string { return nil },
				},
			},
			configs:  load.FilenameToConfig{"myfile.yaml": cfg},
			expected: map[string]configIndex{},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tc.agent.indexes = tc.agent.buildIndexes(tc.configs)
			if diff := cmp.Diff(tc.agent.indexes, tc.expected); diff != "" {
				t.Errorf("indexes are not as expected, diff: %v", diff)
			}
		})
	}
}
