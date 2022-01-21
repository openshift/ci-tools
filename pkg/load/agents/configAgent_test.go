package agents

import (
	"encoding/json"
	"sort"
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/config"
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
		reloadConfig: func() error { return nil },
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
		reloadConfig: func() error { return nil },
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
		configs  config.ByOrgRepo
		expected map[string]configIndex
	}{
		{
			name: "single index",
			agent: &configAgent{
				indexFuncs: map[string]IndexFn{
					"index-a": func(_ api.ReleaseBuildConfiguration) []string { return []string{"key-a"} },
				},
			},
			configs:  config.ByOrgRepo{"org": {"repo": []api.ReleaseBuildConfiguration{cfg}}},
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
			configs: config.ByOrgRepo{"org": {"repo": []api.ReleaseBuildConfiguration{cfg}}},
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
			configs:  config.ByOrgRepo{"org": {"repo": []api.ReleaseBuildConfiguration{cfg}}},
			expected: map[string]configIndex{"index-a": {}},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tc.agent.configs = tc.configs
			tc.agent.buildIndexes()
			if diff := cmp.Diff(tc.agent.indexes, tc.expected); diff != "" {
				t.Errorf("indexes are not as expected, diff: %v", diff)
			}
		})
	}
}

func TestConfigAgent_GetMatchingConfig(t *testing.T) {
	var testCases = []struct {
		name        string
		input       config.ByOrgRepo
		meta        api.Metadata
		expected    api.ReleaseBuildConfiguration
		expectedErr bool
	}{
		{
			name:  "no configs in org fails",
			input: config.ByOrgRepo{},
			meta: api.Metadata{
				Org:    "org",
				Repo:   "repo",
				Branch: "branch",
			},
			expectedErr: true,
		},
		{
			name: "no configs in repo fails",
			input: config.ByOrgRepo{
				"org": map[string][]api.ReleaseBuildConfiguration{},
			},
			meta: api.Metadata{
				Org:    "org",
				Repo:   "repo",
				Branch: "branch",
			},
			expectedErr: true,
		},
		{
			name: "no configs for variant fails",
			input: config.ByOrgRepo{
				"org": map[string][]api.ReleaseBuildConfiguration{
					"repo": {{Metadata: api.Metadata{
						Org:    "org",
						Repo:   "repo",
						Branch: "branch",
					}}},
				},
			},
			meta: api.Metadata{
				Org:     "org",
				Repo:    "repo",
				Branch:  "branch",
				Variant: "variant",
			},
			expectedErr: true,
		},
		{
			name: "literal match returns it",
			input: config.ByOrgRepo{
				"org": map[string][]api.ReleaseBuildConfiguration{
					"repo": {{Metadata: api.Metadata{
						Org:    "org",
						Repo:   "repo",
						Branch: "branch",
					}}},
				},
			},
			meta: api.Metadata{
				Org:    "org",
				Repo:   "repo",
				Branch: "branch",
			},
			expected: api.ReleaseBuildConfiguration{Metadata: api.Metadata{
				Org:    "org",
				Repo:   "repo",
				Branch: "branch",
			}},
			expectedErr: false,
		},
		{
			name: "regex match on branch returns it",
			input: config.ByOrgRepo{
				"org": map[string][]api.ReleaseBuildConfiguration{
					"repo": {{Metadata: api.Metadata{
						Org:    "org",
						Repo:   "repo",
						Branch: "branch",
					}}},
				},
			},
			meta: api.Metadata{
				Org:    "org",
				Repo:   "repo",
				Branch: "branch-foo",
			},
			expected: api.ReleaseBuildConfiguration{Metadata: api.Metadata{
				Org:    "org",
				Repo:   "repo",
				Branch: "branch",
			}},
			expectedErr: false,
		},
		{
			name: "regex match on branch with variant returns it",
			input: config.ByOrgRepo{
				"org": map[string][]api.ReleaseBuildConfiguration{
					"repo": {{Metadata: api.Metadata{
						Org:     "org",
						Repo:    "repo",
						Branch:  "branch",
						Variant: "variant",
					}}},
				},
			},
			meta: api.Metadata{
				Org:     "org",
				Repo:    "repo",
				Branch:  "branch-foo",
				Variant: "variant",
			},
			expected: api.ReleaseBuildConfiguration{Metadata: api.Metadata{
				Org:     "org",
				Repo:    "repo",
				Branch:  "branch",
				Variant: "variant",
			}},
			expectedErr: false,
		},
		{
			name: "regex match on branch without variant fails",
			input: config.ByOrgRepo{
				"org": map[string][]api.ReleaseBuildConfiguration{
					"repo": {{Metadata: api.Metadata{
						Org:    "org",
						Repo:   "repo",
						Branch: "branch",
					}}},
				},
			},
			meta: api.Metadata{
				Org:     "org",
				Repo:    "repo",
				Branch:  "branch-foo",
				Variant: "variant",
			},
			expectedErr: true,
		},
		{
			name: "multiple matches fails",
			input: config.ByOrgRepo{
				"org": map[string][]api.ReleaseBuildConfiguration{
					"repo": {{Metadata: api.Metadata{
						Org:    "org",
						Repo:   "repo",
						Branch: "branch",
					}}, {Metadata: api.Metadata{
						Org:    "org",
						Repo:   "repo",
						Branch: "branch-foo",
					}}},
				},
			},
			meta: api.Metadata{
				Org:    "org",
				Repo:   "repo",
				Branch: "branch-foo-bar",
			},
			expectedErr: true,
		},
		{
			name: "no error on simple substring",
			input: config.ByOrgRepo{
				"org": map[string][]api.ReleaseBuildConfiguration{
					"repo": {{Metadata: api.Metadata{
						Org:    "org",
						Repo:   "repo",
						Branch: "release-4.1",
					}}, {Metadata: api.Metadata{
						Org:    "org",
						Repo:   "repo",
						Branch: "release-4.10",
					}}},
				},
			},
			meta: api.Metadata{
				Org:    "org",
				Repo:   "repo",
				Branch: "release-4.10",
			},
			expected: api.ReleaseBuildConfiguration{Metadata: api.Metadata{
				Org:    "org",
				Repo:   "repo",
				Branch: "release-4.10",
			}},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			agent := &configAgent{lock: &sync.RWMutex{}, configs: testCase.input}
			actual, actualErr := agent.GetMatchingConfig(testCase.meta)
			if testCase.expectedErr && actualErr == nil {
				t.Errorf("%s: expected an error but got none", testCase.name)
			}
			if !testCase.expectedErr && actualErr != nil {
				t.Errorf("%s: expected no error but got one: %v", testCase.name, actualErr)
			}

			if diff := cmp.Diff(actual, testCase.expected); !testCase.expectedErr && diff != "" {
				t.Errorf("%s: got incorrect config: %v", testCase.name, diff)
			}
		})
	}
}

func TestBuildIndexDelta(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name     string
		oldIndex configIndex
		newIndex configIndex

		expected []IndexDelta
	}{
		{
			name: "No changes in differently-ordered config",
			oldIndex: map[string][]*api.ReleaseBuildConfiguration{"": {
				{Metadata: api.Metadata{Org: "bar"}, RpmBuildLocation: "/bar"},
				{Metadata: api.Metadata{Org: "foo"}, RpmBuildLocation: "/foo"},
			}},
			newIndex: map[string][]*api.ReleaseBuildConfiguration{"": {
				{Metadata: api.Metadata{Org: "foo"}, RpmBuildLocation: "/foo"},
				{Metadata: api.Metadata{Org: "bar"}, RpmBuildLocation: "/bar"},
			}},
		},
		{
			name: "Config is removed",
			oldIndex: map[string][]*api.ReleaseBuildConfiguration{
				"1": {
					{Metadata: api.Metadata{Org: "bar"}, RpmBuildLocation: "/bar"},
					{Metadata: api.Metadata{Org: "removed"}, RpmBuildLocation: "/removed"},
					{Metadata: api.Metadata{Org: "foo"}, RpmBuildLocation: "/foo"},
				},
				"2": {
					{Metadata: api.Metadata{Org: "bar-2"}, RpmBuildLocation: "/bar"},
					{Metadata: api.Metadata{Org: "removed-2"}, RpmBuildLocation: "/removed-2"},
					{Metadata: api.Metadata{Org: "foo-2"}, RpmBuildLocation: "/foo"},
				},
				"3": {
					{Metadata: api.Metadata{Org: "removed-3"}, RpmBuildLocation: "/removed-3"},
				},
			},
			newIndex: map[string][]*api.ReleaseBuildConfiguration{
				"1": {
					{Metadata: api.Metadata{Org: "bar"}, RpmBuildLocation: "/bar"},
					{Metadata: api.Metadata{Org: "foo"}, RpmBuildLocation: "/foo"},
				},
				"2": {
					{Metadata: api.Metadata{Org: "bar-2"}, RpmBuildLocation: "/bar"},
					{Metadata: api.Metadata{Org: "foo-2"}, RpmBuildLocation: "/foo"},
				},
			},
			expected: []IndexDelta{
				{
					IndexKey: "1",
					Removed:  []*api.ReleaseBuildConfiguration{{Metadata: api.Metadata{Org: "removed"}, RpmBuildLocation: "/removed"}},
				},
				{
					IndexKey: "2",
					Removed:  []*api.ReleaseBuildConfiguration{{Metadata: api.Metadata{Org: "removed-2"}, RpmBuildLocation: "/removed-2"}},
				},
				{
					IndexKey: "3",
					Removed:  []*api.ReleaseBuildConfiguration{{Metadata: api.Metadata{Org: "removed-3"}, RpmBuildLocation: "/removed-3"}},
				},
			},
		},
		{
			name: "Config is added",
			oldIndex: map[string][]*api.ReleaseBuildConfiguration{
				"1": {
					{Metadata: api.Metadata{Org: "bar"}, RpmBuildLocation: "/bar"},
					{Metadata: api.Metadata{Org: "foo"}, RpmBuildLocation: "/foo"},
				},
				"2": {
					{Metadata: api.Metadata{Org: "bar-2"}, RpmBuildLocation: "/bar"},
					{Metadata: api.Metadata{Org: "foo-2"}, RpmBuildLocation: "/foo"},
				},
			},
			newIndex: map[string][]*api.ReleaseBuildConfiguration{
				"1": {
					{Metadata: api.Metadata{Org: "bar"}, RpmBuildLocation: "/bar"},
					{Metadata: api.Metadata{Org: "added"}, RpmBuildLocation: "/added"},
					{Metadata: api.Metadata{Org: "foo"}, RpmBuildLocation: "/foo"},
				},
				"2": {
					{Metadata: api.Metadata{Org: "bar-2"}, RpmBuildLocation: "/bar"},
					{Metadata: api.Metadata{Org: "added-2"}, RpmBuildLocation: "/added-2"},
					{Metadata: api.Metadata{Org: "foo-2"}, RpmBuildLocation: "/foo"},
				},
				"3": {
					{Metadata: api.Metadata{Org: "added-3"}, RpmBuildLocation: "/added-3"},
				},
			},
			expected: []IndexDelta{
				{
					IndexKey: "1",
					Added:    []*api.ReleaseBuildConfiguration{{Metadata: api.Metadata{Org: "added"}, RpmBuildLocation: "/added"}},
				},
				{
					IndexKey: "2",
					Added:    []*api.ReleaseBuildConfiguration{{Metadata: api.Metadata{Org: "added-2"}, RpmBuildLocation: "/added-2"}},
				},
				{
					IndexKey: "3",
					Added:    []*api.ReleaseBuildConfiguration{{Metadata: api.Metadata{Org: "added-3"}, RpmBuildLocation: "/added-3"}},
				},
			},
		},
		{
			name: "Config is changed",
			oldIndex: map[string][]*api.ReleaseBuildConfiguration{
				"1": {
					{Metadata: api.Metadata{Org: "bar"}, RpmBuildLocation: "/bar"},
					{Metadata: api.Metadata{Org: "to-be-updated"}, RpmBuildLocation: "/initial"},
					{Metadata: api.Metadata{Org: "foo"}, RpmBuildLocation: "/foo"},
				},
				"2": {
					{Metadata: api.Metadata{Org: "bar-2"}, RpmBuildLocation: "/bar"},
					{Metadata: api.Metadata{Org: "to-be-updated-2"}, RpmBuildLocation: "/initial"},
					{Metadata: api.Metadata{Org: "foo-2"}, RpmBuildLocation: "/foo"},
				},
			},
			newIndex: map[string][]*api.ReleaseBuildConfiguration{
				"1": {
					{Metadata: api.Metadata{Org: "bar"}, RpmBuildLocation: "/bar"},
					{Metadata: api.Metadata{Org: "foo"}, RpmBuildLocation: "/foo"},
					{Metadata: api.Metadata{Org: "to-be-updated"}, RpmBuildLocation: "/updated"},
				},
				"2": {
					{Metadata: api.Metadata{Org: "bar-2"}, RpmBuildLocation: "/bar"},
					{Metadata: api.Metadata{Org: "foo-2"}, RpmBuildLocation: "/foo"},
					{Metadata: api.Metadata{Org: "to-be-updated-2"}, RpmBuildLocation: "/updated"},
				},
			},
			expected: []IndexDelta{
				{
					IndexKey: "1",
					Added: []*api.ReleaseBuildConfiguration{
						{Metadata: api.Metadata{Org: "to-be-updated"}, RpmBuildLocation: "/updated"},
					},
					Removed: []*api.ReleaseBuildConfiguration{
						{Metadata: api.Metadata{Org: "to-be-updated"}, RpmBuildLocation: "/initial"},
					},
				},
				{
					IndexKey: "2",
					Added: []*api.ReleaseBuildConfiguration{
						{Metadata: api.Metadata{Org: "to-be-updated-2"}, RpmBuildLocation: "/updated"},
					},
					Removed: []*api.ReleaseBuildConfiguration{
						{Metadata: api.Metadata{Org: "to-be-updated-2"}, RpmBuildLocation: "/initial"},
					},
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {

			actual := buildIndexDelta(tc.oldIndex, tc.newIndex)

			// The index constructions uses a lot of maps, so it
			// will output an unordered list of elements.
			sort.Slice(actual, func(i, j int) bool {
				iSerialized, err := json.Marshal(actual[i])
				if err != nil {
					t.Fatalf("failed to serialize element: %v", err)
				}
				jSerialized, err := json.Marshal(actual[j])
				if err != nil {
					t.Fatalf("failed to serialize element: %v", err)
				}
				return string(iSerialized) < string(jSerialized)
			})

			if diff := cmp.Diff(tc.expected, actual); diff != "" {
				t.Errorf("expected delta differs from actual: %s", diff)
			}
		})
	}
}
