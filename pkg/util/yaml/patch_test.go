package yaml

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestApplyPatch(t *testing.T) {
	for _, tc := range []struct {
		name       string
		object     string
		patch      Patch
		opts       []ApplyPatchOption
		wantObject string
	}{
		{
			name:       "JsonMerge patch: simple yaml",
			object:     "foo: bar",
			patch:      JsonMergePatch([]byte("foo: super")),
			wantObject: "foo: super\n",
		},
		{
			name:       "JsonMerge patch: nested yaml",
			object:     "foo:\n  bar: super",
			patch:      JsonMergePatch([]byte("foo:\n bar: duper")),
			wantObject: "foo:\n  bar: duper\n",
		},
		{
			name:       "JsonMerge patch: json patch",
			object:     "foo:\n  bar: super",
			patch:      JsonMergePatch([]byte(`{"foo": {"bar": "duper"}}`)),
			wantObject: "foo:\n  bar: duper\n",
		},
		{
			name:       "JsonPatch patch: json patch",
			object:     "foo:\n  bar: super",
			patch:      JsonPatch([]byte(`[{"op": "add", "path": "/foo/bar", "value": "duper"}]`)),
			wantObject: "foo:\n  bar: duper\n",
		},
		{
			name:       "JsonPatch patch: ignore missing key on remove",
			object:     "foo:\n  bar: super",
			patch:      JsonPatch([]byte(`[{"op": "remove", "path": "/foo/bax"}, {"op": "remove", "path": "/foo/bar"}]`)),
			opts:       []ApplyPatchOption{IgnoreMissingKeyOnRemove()},
			wantObject: "foo: {}\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			patched, err := ApplyPatch([]byte(tc.object), tc.patch, tc.opts...)
			if err != nil {
				t.Errorf("unexpected err: %s", err)
				return
			}
			if diff := cmp.Diff(tc.wantObject, string(patched)); diff != "" {
				t.Errorf("unexpected diff: %s", diff)
			}
		})
	}
}
