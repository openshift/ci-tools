package api

import (
	"errors"
	"reflect"
	"testing"

	"k8s.io/apimachinery/pkg/util/diff"
)

func TestDeferredParametersMap(t *testing.T) {
	var testCases = []struct {
		purpose  string
		dp       *DeferredParameters
		expected map[string]any
	}{{
		purpose: "values[N]=V, fns[N] is not set, so returned map does not have key 'N'",
		dp: &DeferredParameters{
			values: map[string]any{"K1": "V1"},
			fns:    map[string]func() (any, error){},
		},
		expected: map[string]any{},
	}, {
		purpose: "fns[N] is set, values[N] is not, so returned map has key 'N' set to fns[N]()",
		dp: &DeferredParameters{
			values: map[string]any{},
			fns:    map[string]func() (any, error){"K1": func() (any, error) { return "V1", nil }},
		},
		expected: map[string]any{"K1": "V1"},
	}, {
		purpose: "fns[N] is set, values[N] is set, so returned map has key 'N' set to values[N]",
		dp: &DeferredParameters{
			values: map[string]any{"K1": "V1"},
			fns:    map[string]func() (any, error){"K1": func() (any, error) { return "F1", nil }},
		},
		expected: map[string]any{"K1": "V1"},
	}, {
		purpose: "returned map contains all names",
		dp: &DeferredParameters{
			values: map[string]any{"K1": "V1", "K2": "V2"},
			fns: map[string]func() (any, error){
				"K1": func() (any, error) { return "should not be returned", nil },
				"K2": func() (any, error) { return "should not be returned", nil },
				"K3": func() (any, error) { return "F3", nil },
				"K4": func() (any, error) { return "F4", nil },
			},
		},
		expected: map[string]any{"K1": "V1", "K2": "V2", "K3": "F3", "K4": "F4"},
	}, {
		purpose: "parent values are not returned",
		dp: &DeferredParameters{
			params: &DeferredParameters{
				values: map[string]any{"K1": "V1"},
				fns: map[string]func() (any, error){
					"K2": func() (any, error) { return "V2", nil },
				},
			},
		},
		expected: map[string]any{},
	}}

	for _, tc := range testCases {
		createdMap, _ := tc.dp.Map()

		if !reflect.DeepEqual(tc.expected, createdMap) {
			t.Errorf("%s\n %v.Map() returned different map:\n%s", tc.purpose, tc.dp, diff.ObjectReflectDiff(tc.expected, createdMap))
		}
	}
}

func TestDeferredParametersGetSet(t *testing.T) {
	t.Parallel()

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
			values: map[string]any{"key": "oldValue"},
		},
		name:     "key",
		callSet:  true,
		setValue: "newValue",

		getValue: "oldValue",
		getError: nil,
	}, {
		purpose: "Existing key is not set if lazy evaluation func is set",
		dp: &DeferredParameters{
			fns: map[string]func() (any, error){
				"key": func() (any, error) { return "lazyValue", nil },
			},
			values: map[string]any{},
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
		t.Run(tc.purpose, func(t *testing.T) {
			t.Parallel()

			if tc.callSet {
				tc.dp.Set(tc.name, tc.setValue)
			}
			if value, err := tc.dp.GetString(tc.name); value != tc.getValue || err != tc.getError {
				t.Errorf("Get(%s) returned (%s, %v), expected (%s, %v)", tc.name, value, err, tc.getValue, tc.getError)
			}
		})
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
			values: map[string]any{"K": "expected"},
			fns:    map[string]func() (any, error){},
		},
	}, {
		name: "fns, no parent",
		params: &DeferredParameters{
			values: map[string]any{},
			fns: map[string]func() (any, error){
				"K": func() (any, error) { return "expected", nil },
			},
		},
	}, {
		name: "values, parent",
		params: &DeferredParameters{
			values: map[string]any{"K": "expected"},
			fns:    map[string]func() (any, error){},
			params: &DeferredParameters{
				values: map[string]any{"K": "unexpected"},
			},
		},
	}, {
		name: "fns, parent",
		params: &DeferredParameters{
			values: map[string]any{},
			fns: map[string]func() (any, error){
				"K": func() (any, error) { return "expected", nil },
			},
			params: &DeferredParameters{
				values: map[string]any{"K": "unexpected"},
			},
		},
	}, {
		name: "from parent's values",
		params: &DeferredParameters{
			values: map[string]any{},
			fns:    map[string]func() (any, error){},
			params: &DeferredParameters{
				values: map[string]any{"K": "expected"},
			},
		},
	}, {
		name: "from parent's fns",
		params: &DeferredParameters{
			values: map[string]any{},
			fns:    map[string]func() (any, error){},
			params: &DeferredParameters{
				values: map[string]any{},
				fns: map[string]func() (any, error){
					"K": func() (any, error) { return "expected", nil },
				},
			},
		},
	}} {
		t.Run(tc.name, func(t *testing.T) {
			ret, err := tc.params.GetString("K")
			if err != tc.expectedErr {
				t.Errorf("err: want %v, got %v", tc.expectedErr, err)
			}
			if ret != "expected" {
				t.Errorf("got unexpected value %q", ret)
			}
		})
	}
}

func TestGet(t *testing.T) {
	t.Parallel()

	// String from values
	wantValueStr := "bar"
	params := NewDeferredParameters(nil)
	params.Set("foo", wantValueStr)

	gotValueStr, err := params.Get("foo")
	if err != nil {
		t.Errorf("get string - unexpected error: %s", err)
	}

	gotValueStrTyped, ok := gotValueStr.(string)
	if !ok {
		t.Errorf("want type %T but got %T", "str", gotValueStrTyped)
	}

	if gotValueStrTyped != wantValueStr {
		t.Errorf("want %s but got %s", wantValueStr, gotValueStr)
	}

	// Int from fns
	wantValueInt := 1
	params = NewDeferredParameters(nil)
	params.Add("foo", func() (any, error) { return wantValueInt, nil })

	gotValueInt, err := params.Get("foo")
	if err != nil {
		t.Errorf("get int - unexpected error: %s", err)
	}

	gotValueIntTyped, ok := gotValueInt.(int)
	if !ok {
		t.Errorf("want type %T but got %T", "str", gotValueIntTyped)
	}

	if gotValueIntTyped != wantValueInt {
		t.Errorf("want %d but got %d", wantValueInt, gotValueIntTyped)
	}

	// Struct from inner params
	wantValueStruct := struct{ data string }{data: "bar"}
	inner := NewDeferredParameters(nil)
	inner.Add("foo", func() (any, error) { return wantValueStruct, nil })
	params = NewDeferredParameters(inner)

	gotValueStruct, err := params.Get("foo")
	if err != nil {
		t.Errorf("get struct - unexpected error: %s", err)
	}

	gotValueStructTyped, ok := gotValueStruct.(struct{ data string })
	if !ok {
		t.Errorf("want type %T but got %T", wantValueStruct, gotValueStructTyped)
	}

	if gotValueStructTyped != wantValueStruct {
		t.Errorf("want %+v but got %+v", wantValueStruct, gotValueStructTyped)
	}

	// Err not found
	params = &DeferredParameters{
		values: make(map[string]any),
	}

	_, err = params.Get("void")
	if err == nil || !errors.Is(err, &ErrParamNotFound{}) {
		t.Errorf("want error %T but got: %T", &ErrParamNotFound{}, err)
	}
}

func TestGetParamTyped(t *testing.T) {
	t.Parallel()

	// Get a string
	wantValue := "bar"
	params := &DeferredParameters{values: map[string]any{"foo": wantValue}}

	gotValue, err := GetParamTyped[string](params, "foo")
	if err != nil {
		t.Errorf("get string - unexpected error: %s", err)
	}

	if gotValue != wantValue {
		t.Errorf("want %s but got %s", wantValue, gotValue)
	}

	// Err type mismatch
	params = &DeferredParameters{values: map[string]any{"foo": "bar"}}

	_, err = GetParamTyped[int](params, "foo")
	if err == nil || !errors.Is(err, &ErrParamTypeMismatch{}) {
		t.Errorf("want error %T but got: %T", &ErrParamTypeMismatch{}, err)
	}

	// Err not Found over type mismatch
	params = &DeferredParameters{values: map[string]any{"foo": "bar"}}

	_, err = GetParamTyped[int](params, "void")
	if err == nil || !errors.Is(err, &ErrParamNotFound{}) {
		t.Errorf("want error %T but got: %T", &ErrParamNotFound{}, err)
	}
}
