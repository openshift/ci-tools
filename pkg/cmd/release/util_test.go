package release

import (
	"testing"

	"github.com/openshift/ci-tools/pkg/testhelper"
)

func TestArgsWithPrefixes(t *testing.T) {
	for _, tc := range []struct {
		name, root, def, path, arg, expected string
	}{{
		name: "empty",
	}, {
		name:     "argument only",
		arg:      "arg",
		expected: "arg",
	}, {
		name:     "def + argument are joined",
		def:      "def",
		arg:      "arg",
		expected: "def/arg",
	}, {
		name:     "path + argument are joined",
		path:     "path",
		arg:      "arg",
		expected: "path/arg",
	}, {
		name:     "root + argument are joined",
		root:     "root",
		arg:      "arg",
		expected: "root/arg",
	}, {
		name:     "root + def + argument are joined",
		root:     "root",
		def:      "def",
		arg:      "arg",
		expected: "root/def/arg",
	}, {
		name:     "root + path + argument are joined",
		root:     "root",
		path:     "path",
		arg:      "arg",
		expected: "root/path/arg",
	}, {
		name:     "path overrides def",
		def:      "def",
		path:     "path",
		arg:      "arg",
		expected: "path/arg",
	}, {
		name:     "absolute path overrides root",
		root:     "root",
		path:     "/path",
		arg:      "arg",
		expected: "/path/arg",
	}} {
		t.Run(tc.name, func(t *testing.T) {
			o := options{rootPath: tc.root}
			ret := o.argsWithPrefixes(tc.def, tc.path, []string{tc.arg})
			testhelper.Diff(t, "result", []string{tc.expected}, ret)
		})
	}
}
