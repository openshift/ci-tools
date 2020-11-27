package api

import (
	"reflect"
	"testing"

	"k8s.io/apimachinery/pkg/util/diff"
)

func TestDeferredParametersMap(t *testing.T) {
	var testCases = []struct {
		purpose  string
		dp       *DeferredParameters
		expected map[string]string
	}{{
		purpose: "values[N]=V, fns[N] is not set, so returned map does not have key 'N'",
		dp: &DeferredParameters{
			values: map[string]string{"K1": "V1"},
			fns:    map[string]func() (string, error){},
		},
		expected: map[string]string{},
	}, {
		purpose: "fns[N] is set, values[N] is not, so returned map has key 'N' set to fns[N]()",
		dp: &DeferredParameters{
			values: map[string]string{},
			fns:    map[string]func() (string, error){"K1": func() (string, error) { return "V1", nil }},
		},
		expected: map[string]string{"K1": "V1"},
	}, {
		purpose: "fns[N] is set, values[N] is set, so returned map has key 'N' set to values[N]",
		dp: &DeferredParameters{
			values: map[string]string{"K1": "V1"},
			fns:    map[string]func() (string, error){"K1": func() (string, error) { return "F1", nil }},
		},
		expected: map[string]string{"K1": "V1"},
	}, {
		purpose: "returned map contains all names",
		dp: &DeferredParameters{
			values: map[string]string{"K1": "V1", "K2": "V2"},
			fns: map[string]func() (string, error){
				"K1": func() (string, error) { return "should not be returned", nil },
				"K2": func() (string, error) { return "should not be returned", nil },
				"K3": func() (string, error) { return "F3", nil },
				"K4": func() (string, error) { return "F4", nil },
			},
		},
		expected: map[string]string{"K1": "V1", "K2": "V2", "K3": "F3", "K4": "F4"},
	}, {
		purpose: "parent values are not returned",
		dp: &DeferredParameters{
			params: &DeferredParameters{
				values: map[string]string{"K1": "V1"},
				fns: map[string]func() (string, error){
					"K2": func() (string, error) { return "V2", nil },
				},
			},
		},
		expected: map[string]string{},
	}}

	for _, tc := range testCases {
		createdMap, _ := tc.dp.Map()

		if !reflect.DeepEqual(tc.expected, createdMap) {
			t.Errorf("%s\n %v.Map() returned different map:\n%s", tc.purpose, tc.dp, diff.ObjectReflectDiff(tc.expected, createdMap))
		}
	}
}

func TestDeferredParametersGetSet(t *testing.T) {
	var testCases = []struct {
		purpose  string
		dp       *DeferredParameters
		name     string
		callSet  bool
		setValue string
		getValue string
		getError error
	}{{
		purpose:  "New key",
		dp:       NewDeferredParameters(nil),
		name:     "key",
		callSet:  true,
		setValue: "newValue",

		getValue: "newValue",
		getError: nil,
	}, {
		purpose: "Existing key is not overwritten",
		dp: &DeferredParameters{
			fns:    make(ParameterMap),
			values: map[string]string{"key": "oldValue"},
		},
		name:     "key",
		callSet:  true,
		setValue: "newValue",

		getValue: "oldValue",
		getError: nil,
	}, {
		purpose: "Existing key is not set if lazy evaluation func is set",
		dp: &DeferredParameters{
			fns: map[string]func() (string, error){
				"key": func() (string, error) { return "lazyValue", nil },
			},
			values: map[string]string{},
		},
		name:     "key",
		callSet:  true,
		setValue: "newValue",

		getValue: "lazyValue",
		getError: nil,
	}, {
		purpose:  "Key that was not added",
		dp:       NewDeferredParameters(nil),
		name:     "key",
		callSet:  false,
		setValue: "THIS SHOULD NOT BE USED",

		getValue: "",
		getError: nil,
	}}
	for _, tc := range testCases {
		if tc.callSet {
			tc.dp.Set(tc.name, tc.setValue)
		}
		if value, err := tc.dp.Get(tc.name); value != tc.getValue || err != tc.getError {
			t.Errorf("%s: Get(%s) returned (%s, %v), expected (%s, %v)", tc.purpose, tc.name, value, err, tc.getValue, tc.getError)
		}
	}
}

func TestDeferredParametersParent(t *testing.T) {
	for _, tc := range []struct {
		name        string
		params      *DeferredParameters
		expectedErr error
	}{{
		name: "values, no parent",
		params: &DeferredParameters{
			values: map[string]string{"K": "expected"},
			fns:    map[string]func() (string, error){},
		},
	}, {
		name: "fns, no parent",
		params: &DeferredParameters{
			values: map[string]string{},
			fns: map[string]func() (string, error){
				"K": func() (string, error) { return "expected", nil },
			},
		},
	}, {
		name: "values, parent",
		params: &DeferredParameters{
			values: map[string]string{"K": "expected"},
			fns:    map[string]func() (string, error){},
			params: &DeferredParameters{
				values: map[string]string{"K": "unexpected"},
			},
		},
	}, {
		name: "fns, parent",
		params: &DeferredParameters{
			values: map[string]string{},
			fns: map[string]func() (string, error){
				"K": func() (string, error) { return "expected", nil },
			},
			params: &DeferredParameters{
				values: map[string]string{"K": "unexpected"},
			},
		},
	}, {
		name: "from parent's values",
		params: &DeferredParameters{
			values: map[string]string{},
			fns:    map[string]func() (string, error){},
			params: &DeferredParameters{
				values: map[string]string{"K": "expected"},
			},
		},
	}, {
		name: "from parent's fns",
		params: &DeferredParameters{
			values: map[string]string{},
			fns:    map[string]func() (string, error){},
			params: &DeferredParameters{
				values: map[string]string{},
				fns: map[string]func() (string, error){
					"K": func() (string, error) { return "expected", nil },
				},
			},
		},
	}} {
		t.Run(tc.name, func(t *testing.T) {
			ret, err := tc.params.Get("K")
			if err != tc.expectedErr {
				t.Errorf("err: want %v, got %v", tc.expectedErr, err)
			}
			if ret != "expected" {
				t.Errorf("got unexpected value %q", ret)
			}
			if !tc.params.Has("K") {
				t.Errorf(`"Has" and "Get" disagree`)
			}
		})
	}
}
