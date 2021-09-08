package testhelper

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/yaml"
)

// WriteToFixture reads an input fixture file and returns the data
func WriteToFixture(t *testing.T, identifier string, data []byte) {
	golden, err := golden(t, &Options{Suffix: identifier})
	if err != nil {
		t.Fatalf("failed to get absolute path to testdata file: %v", err)
	}

	if err := os.MkdirAll(filepath.Dir(golden), 0755); err != nil {
		t.Fatalf("failed to create fixture directory: %v", err)
	}
	if err := ioutil.WriteFile(golden, data, 0644); err != nil {
		t.Fatalf("failed to write testdata file: %v", err)
	}
}

// ReadFromFixture reads an input fixture file and returns the data
func ReadFromFixture(t *testing.T, identifier string) []byte {
	golden, err := golden(t, &Options{Suffix: identifier})
	if err != nil {
		t.Fatalf("failed to get absolute path to testdata file: %v", err)
	}

	data, err := ioutil.ReadFile(golden)
	if err != nil {
		t.Fatalf("failed to read testdata file: %v", err)
	}
	return data
}

type Options struct {
	Prefix    string
	Suffix    string
	Extension string
}

type Option func(*Options)

func WithPrefix(prefix string) Option {
	return func(o *Options) {
		o.Prefix = prefix
	}
}

func WithSuffix(suffix string) Option {
	return func(o *Options) {
		o.Suffix = suffix
	}
}

func WithExtension(extension string) Option {
	return func(o *Options) {
		o.Extension = extension
	}
}

// golden determines the golden file to use
func golden(t *testing.T, opts *Options) (string, error) {
	if opts.Extension == "" {
		opts.Extension = ".yaml"
	}
	return filepath.Abs(filepath.Join("testdata", sanitizeFilename(opts.Prefix+t.Name()+opts.Suffix)) + opts.Extension)
}

// CompareWithFixture will compare output with a test fixture and allows to automatically update them
// by setting the UPDATE env var.
// If output is not a []byte or string, it will get serialized as yaml prior to the comparison.
// The fixtures are stored in $PWD/testdata/prefix${testName}.yaml
func CompareWithFixture(t *testing.T, output interface{}, opts ...Option) {
	t.Helper()
	options := &Options{
		Extension: ".yaml",
	}
	for _, opt := range opts {
		opt(options)
	}

	var serializedOutput []byte
	switch v := output.(type) {
	case []byte:
		serializedOutput = v
	case string:
		serializedOutput = []byte(v)
	default:
		serialized, err := yaml.Marshal(v)
		if err != nil {
			t.Fatalf("failed to yaml marshal output of type %T: %v", output, err)
		}
		serializedOutput = serialized
	}

	golden, err := golden(t, options)
	if err != nil {
		t.Fatalf("failed to get absolute path to testdata file: %v", err)
	}
	if os.Getenv("UPDATE") != "" {
		if err := os.MkdirAll(filepath.Dir(golden), 0755); err != nil {
			t.Fatalf("failed to create fixture directory: %v", err)
		}
		if err := ioutil.WriteFile(golden, serializedOutput, 0644); err != nil {
			t.Fatalf("failed to write updated fixture: %v", err)
		}
	}
	expected, err := ioutil.ReadFile(golden)
	if err != nil {
		t.Fatalf("failed to read testdata file: %v", err)
	}

	if diff := cmp.Diff(string(expected), string(serializedOutput)); diff != "" {
		t.Errorf("got diff between expected and actual result:\nfile: %s\ndiff:\n%s\n\nIf this is expected, re-run the test with `UPDATE=true go test ./...` to update the fixtures.", golden, diff)
	}
}

func sanitizeFilename(s string) string {
	result := strings.Builder{}
	for _, r := range s {
		if (r >= 'a' && r < 'z') || (r >= 'A' && r < 'Z') || r == '_' || r == '.' || (r >= '0' && r <= '9') {
			// The thing is documented as returning a nil error so lets just drop it
			_, _ = result.WriteRune(r)
			continue
		}
		if !strings.HasSuffix(result.String(), "_") {
			result.WriteRune('_')
		}
	}
	return "zz_fixture_" + result.String()
}

var (
	// EquateErrorMessage reports errors to be equal if both are nil
	// or both have the same message.
	EquateErrorMessage = cmp.FilterValues(func(x, y interface{}) bool {
		_, ok1 := x.(error)
		_, ok2 := y.(error)
		return ok1 && ok2
	}, cmp.Comparer(func(x, y interface{}) bool {
		xe := x.(error)
		ye := y.(error)
		if xe == nil || ye == nil {
			return xe == nil && ye == nil
		}
		return xe.Error() == ye.Error()
	}))

	// RuntimeObjectIgnoreRvTypeMeta compares two kubernetes objects, ignoring their resource
	// version and TypeMeta. It is what you want 99% of the time.
	RuntimeObjectIgnoreRvTypeMeta = cmp.Comparer(func(x, y runtime.Object) bool {
		xCopy := x.DeepCopyObject()
		yCopy := y.DeepCopyObject()
		CleanRVAndTypeMeta(xCopy)
		CleanRVAndTypeMeta(yCopy)
		return cmp.Diff(xCopy, yCopy) == ""
	})
)

func CleanRVAndTypeMeta(r runtime.Object) {
	if metaObject, ok := r.(metav1.Object); ok {
		metaObject.SetResourceVersion("")
	}
	if typeObject, ok := r.(interface{ SetGroupVersionKind(schema.GroupVersionKind) }); ok {
		typeObject.SetGroupVersionKind(schema.GroupVersionKind{})
	}
	if _, isList := r.(metav1.ListInterface); isList {
		objects, err := apimeta.ExtractList(r)
		// ExtractList only errors if the list is not a list, so this
		// should never error.
		if err != nil {
			panic(fmt.Sprintf("extract list failed: %v", err))
		}
		for _, item := range objects {
			CleanRVAndTypeMeta(item)
		}
	}
}

func Diff(t *testing.T, name string, x, y interface{}, opts ...cmp.Option) {
	t.Helper()
	if diff := cmp.Diff(x, y, opts...); diff != "" {
		t.Errorf("unexpected %s: %s", name, diff)
	}
}
