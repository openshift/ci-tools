package api

import (
	"reflect"
	"testing"

	"k8s.io/apimachinery/pkg/util/diff"
)

func someStepLink(as string) StepLink {
	return ExternalImageLink(ImageStreamTagReference{
		Cluster:   "cluster.com",
		Namespace: "namespace",
		Name:      "name",
		Tag:       "tag",
		As:        as,
	})
}

func TestDeferredParametersAllLinks(t *testing.T) {
	var testCases = []struct {
		purpose       string
		dp            *DeferredParameters
		expectedItems int
	}{{
		purpose: "AllLinks should return a slice with all links for all names",
		dp: &DeferredParameters{
			links: map[string][]StepLink{
				"K1": {someStepLink("ONE"), someStepLink("TWO")},
				"K2": {someStepLink("THREE")},
			},
		},
		// only compare count of returned items because AllLinks can return links
		// in any order and StepLink instances cannot be sorted unless the interface
		// is extended to provide something by which to sort
		expectedItems: 3,
	}}

	for _, tc := range testCases {
		links := tc.dp.AllLinks()

		if len(links) != tc.expectedItems {
			t.Errorf("%s: %v.AllLinks() returned %v, expected %d items", tc.purpose, tc.dp, links, tc.expectedItems)
		}
	}
}

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
	}}

	for _, tc := range testCases {
		createdMap, _ := tc.dp.Map()

		if !reflect.DeepEqual(tc.expected, createdMap) {
			t.Errorf("%s\n %v.Map() returned different map:\n%s", tc.purpose, tc.dp, diff.ObjectReflectDiff(tc.expected, createdMap))
		}
	}
}

func TestDeferredParametersAddHasLinksGet(t *testing.T) {
	var testCases = []struct {
		purpose string

		dp      *DeferredParameters
		callAdd bool
		name    string
		link    StepLink
		fn      func() (string, error)

		expectedHas   bool
		expectedLinks []StepLink
		expectedGet   string
	}{{
		purpose: "After `Add(key, link, f)`: Has(key)->true, Links(key)->{link}, Get(key)->f()",
		dp:      NewDeferredParameters(),
		callAdd: true,
		name:    "key",
		link:    someStepLink("name"),
		fn:      func() (string, error) { return "value", nil },

		expectedHas:   true,
		expectedLinks: []StepLink{someStepLink("name")},
		expectedGet:   "value",
	}, {
		purpose: "Without Add(): Has(key)->false and Links(key)->nil",
		dp:      NewDeferredParameters(),
		callAdd: false,
		name:    "key",
		link:    nil,
		fn:      nil,

		expectedHas:   false,
		expectedLinks: nil,
		expectedGet:   "",
	}, {
		purpose: "After `Add(key, new-link)` when `key` already present: Has(key)->true and Links(key)->{new-link}",
		dp: &DeferredParameters{
			fns:    ParameterMap{"key": func() (string, error) { return "old", nil }},
			values: map[string]string{},
			links:  map[string][]StepLink{"key": {someStepLink("old-link")}},
		},
		callAdd: true,
		name:    "key",
		link:    someStepLink("new-link"),
		fn:      func() (string, error) { return "new", nil },

		expectedHas:   true,
		expectedLinks: []StepLink{someStepLink("new-link")},
		expectedGet:   "new",
	}}
	for _, tc := range testCases {
		if tc.callAdd {
			tc.dp.Add(tc.name, tc.link, tc.fn)
		}

		if has := tc.dp.Has(tc.name); has != tc.expectedHas {
			t.Errorf("%s\n Has(%s) returned %t, expected %t", tc.purpose, tc.name, has, tc.expectedHas)
		}
		if links := tc.dp.Links(tc.name); !reflect.DeepEqual(tc.expectedLinks, links) {
			t.Errorf("%s\n Links(%s) returned different links:\n%s", tc.purpose, tc.name, diff.ObjectReflectDiff(tc.expectedLinks, links))
		}
		if get, _ := tc.dp.Get(tc.name); get != tc.expectedGet {
			t.Errorf("%s\n Get(%s) returned %s, expected %s", tc.purpose, tc.name, get, tc.expectedGet)
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
		dp:       NewDeferredParameters(),
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
			links:  map[string][]StepLink{},
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
			links:  map[string][]StepLink{},
		},
		name:     "key",
		callSet:  true,
		setValue: "newValue",

		getValue: "lazyValue",
		getError: nil,
	}, {
		purpose:  "Key that was not added",
		dp:       NewDeferredParameters(),
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
