package yaml_test

import (
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"

	k8syaml "sigs.k8s.io/yaml"

	citoolsyaml "github.com/openshift/ci-tools/pkg/util/yaml"
)

func TestMarshalMultidoc(t *testing.T) {
	for _, tc := range []struct {
		name          string
		objs          []interface{}
		marshaler     citoolsyaml.Marshaler
		wantYamlBytes []byte
		wantErr       error
	}{
		{
			name: "k8syaml: multiple objects success",
			objs: []interface{}{
				map[string]interface{}{"foo": "bar"},
				map[string]interface{}{"super": "duper"},
			},
			marshaler:     k8syaml.Marshal,
			wantYamlBytes: []byte("foo: bar\n---\nsuper: duper\n"),
		},
		{
			name:          "k8syaml: single object success",
			objs:          []interface{}{map[string]interface{}{"foo": "bar"}},
			marshaler:     k8syaml.Marshal,
			wantYamlBytes: []byte("foo: bar\n"),
		},
		{
			name:      "k8syaml: failure",
			objs:      []interface{}{map[string]interface{}{"foo": "bar"}},
			marshaler: func(i interface{}) ([]byte, error) { return nil, errors.New("fake") },
			wantErr:   errors.New("fake"),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			yamlBytes, err := citoolsyaml.MarshalMultidoc(tc.marshaler, tc.objs...)
			if err != nil && tc.wantErr == nil {
				t.Errorf("want err nil but got: %v", err)
			}
			if err == nil && tc.wantErr != nil {
				t.Errorf("want err %v but got nil", tc.wantErr)
			}
			if err != nil && tc.wantErr != nil {
				if tc.wantErr.Error() != err.Error() {
					t.Errorf("expect error %q but got %q", tc.wantErr.Error(), err.Error())
				}
				return
			}
			if diff := cmp.Diff(tc.wantYamlBytes, yamlBytes); diff != "" {
				t.Errorf("unexpected diff:\n%s", diff)
			}
		})
	}
}
