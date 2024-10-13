package manifest

import "testing"

func TestShouldApply(t *testing.T) {
	for _, tc := range []struct {
		name      string
		p         Patch
		manifest  map[string]interface{}
		wantMatch bool
	}{
		{
			name: "Empty patch matches",
			p: Patch{
				Matches: []Match{{}},
			},
			manifest:  map[string]interface{}{"kind": "MachineSet"},
			wantMatch: true,
		},
		{
			name: "Match kind",
			p: Patch{
				Matches: []Match{{Kind: "MachineSet"}},
			},
			manifest:  map[string]interface{}{"kind": "MachineSet"},
			wantMatch: true,
		},
		{
			name: "Kind doesn't match",
			p: Patch{
				Matches: []Match{{Kind: "MachineSet"}},
			},
			manifest: map[string]interface{}{"kind": "Pod"},
		},
		{
			name: "Match name",
			p: Patch{
				Matches: []Match{{Name: "foo.+"}},
			},
			manifest: map[string]interface{}{
				"metadata": map[string]interface{}{"name": "foobar"},
			},
			wantMatch: true,
		},
		{
			name: "Name doesn't match",
			p: Patch{
				Matches: []Match{{Name: "super.+"}},
			},
			manifest: map[string]interface{}{
				"metadata": map[string]interface{}{"name": "foobar"},
			},
		},
		{
			name: "Match namespace",
			p: Patch{
				Matches: []Match{{Namespace: "foo.+"}},
			},
			manifest: map[string]interface{}{
				"metadata": map[string]interface{}{"namespace": "foobar"},
			},
			wantMatch: true,
		},
		{
			name: "Namespace doesn't match",
			p: Patch{
				Matches: []Match{{Namespace: "super.+"}},
			},
			manifest: map[string]interface{}{
				"metadata": map[string]interface{}{"namespace": "foobar"},
			},
		},
		{
			name: "Match labels",
			p: Patch{
				Matches: []Match{{Labels: map[string]string{
					"label1": "value1",
					"label2": "value2",
				}}},
			},
			manifest: map[string]interface{}{
				"metadata": map[string]interface{}{
					"labels": map[string]string{
						"label1": "value1",
						"label2": "value2",
					},
				},
			},
			wantMatch: true,
		},
		{
			name: "Labels don't match",
			p: Patch{
				Matches: []Match{{Labels: map[string]string{
					"label1": "value1",
				}}},
			},
			manifest: map[string]interface{}{
				"metadata": map[string]interface{}{
					"labels": map[string]string{
						"label1": "value1",
						"label2": "value2",
					},
				},
			},
			wantMatch: true,
		},
		{
			name: "Complex match",
			p: Patch{
				Matches: []Match{{
					Name:      "foo.+bar",
					Namespace: "superduper.+",
					Labels: map[string]string{
						"label1": "value1",
					},
				}},
			},
			manifest: map[string]interface{}{
				"metadata": map[string]interface{}{
					"name":      "foo-1-bar",
					"namespace": "superduper-test",
					"labels": map[string]string{
						"label1": "value1",
					},
				},
			},
			wantMatch: true,
		},
		{
			name: "Multiple matches are or-ed",
			p: Patch{
				Matches: []Match{{
					Namespace: "does-not-match",
				}, {
					Name: "match",
				}},
			},
			manifest: map[string]interface{}{
				"metadata": map[string]interface{}{
					"name":      "a-match",
					"namespace": "super-duper",
				},
			},
			wantMatch: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isMatch, err := shouldApplyPatch(tc.manifest, tc.p)
			if err != nil {
				t.Errorf("unexpected err: %s", err)
				return
			}
			if tc.wantMatch != isMatch {
				t.Errorf("want %t but got %t", tc.wantMatch, isMatch)
			}
		})
	}
}
