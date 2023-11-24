package modaltesting

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/slack-go/slack"

	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/yaml"

	"github.com/openshift/ci-tools/pkg/slack/modals"
	"github.com/openshift/ci-tools/pkg/testhelper"
)

// ValidateBlockIds ensures that all the fields are present as block identifiers in the view
func ValidateBlockIds(t *testing.T, view slack.ModalViewRequest, fields ...string) {
	expected := sets.New[string](fields...)
	actual := sets.New[string]()
	for _, block := range view.Blocks.BlockSet {
		// Slack's dynamic marshalling makes this really hard to extract
		blockId := reflect.ValueOf(block).Elem().FieldByName("BlockID").String()
		if blockId != "" {
			actual.Insert(blockId)
		}
	}
	if !expected.Equal(actual) {
		if missing := sets.List(expected.Difference(actual)); len(missing) > 0 {
			t.Errorf("view is missing block IDs: %v", missing)
		}
		if extra := sets.List(actual.Difference(expected)); len(extra) > 0 {
			t.Errorf("view has extra block IDs: %v", extra)
		}
	}
}

type ProcessTestCase struct {
	Name          string
	ExpectedTitle string
	ExpectedBody  string
}

func ValidateParameterProcessing(t *testing.T, parameters modals.JiraIssueParameters, testCases []ProcessTestCase) {
	for _, testCase := range testCases {
		t.Run(testCase.Name, func(t *testing.T) {
			var callback slack.InteractionCallback
			ReadCallbackFixture(t, &callback)
			title, body, err := parameters.Process(&callback)
			if diff := cmp.Diff(testCase.ExpectedTitle, title); diff != "" {
				t.Errorf("%s: got incorrect title: %v", testCase.Name, diff)
			}
			if diff := cmp.Diff(testCase.ExpectedBody, body); diff != "" {
				t.Errorf("%s: got incorrect body: %v", testCase.Name, diff)
			}
			if diff := cmp.Diff(nil, err); diff != "" {
				t.Errorf("%s: got incorrect error: %v", testCase.Name, diff)
			}
		})
	}
}

func WriteCallbackFixture(t *testing.T, data []byte) {
	var callback slack.InteractionCallback
	if err := json.Unmarshal(data, &callback); err != nil {
		t.Errorf("failed to unmarshal payload: %v", err)
		return
	}

	data, err := yaml.Marshal(callback)
	if err != nil {
		t.Errorf("failed to marshal payload: %v", err)
		return
	}

	testhelper.WriteToFixture(t, "_callback", data)
}

func ReadCallbackFixture(t *testing.T, callback interface{}) {
	data := testhelper.ReadFromFixture(t, "_callback")
	if err := yaml.Unmarshal(data, callback); err != nil {
		t.Errorf("failed to unmarshal payload: %v", err)
	}
}
