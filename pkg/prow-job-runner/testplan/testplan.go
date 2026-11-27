// Package testplan reads and validates ordered ci-operator test coordinates.
package testplan

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/sirupsen/logrus"

	prowio "sigs.k8s.io/prow/pkg/io"
	"sigs.k8s.io/yaml"

	"github.com/openshift/ci-tools/pkg/api"
)

// identifier matches a single component of a ci-operator test coordinate.
var identifier = regexp.MustCompile(`^[\w.-]+$`)

// Reader loads test plans from the local filesystem or GCS. Its zero value uses Prow's storage opener.
type Reader struct {
	ReadObject func(context.Context, string) ([]byte, error)
}

// entry identifies a test in a ci-operator configuration.
type entry struct {
	Organization string `json:"organization"`
	Repository   string `json:"repository"`
	Branch       string `json:"branch"`
	Variant      string `json:"variant,omitempty"`
	Test         string `json:"test"`
}

// source validates the entry before its fields can be used in checkout paths or job names.
func (e entry) source() (api.MetadataWithTest, error) {
	for _, field := range []struct{ name, value string }{
		{"organization", e.Organization}, {"repository", e.Repository}, {"branch", e.Branch}, {"test", e.Test}, {"variant", e.Variant},
	} {
		if field.value == "" {
			if field.name == "variant" {
				continue
			}
			return api.MetadataWithTest{}, fmt.Errorf("%s is required", field.name)
		}
		if !identifier.MatchString(field.value) || field.value == "." || field.value == ".." {
			return api.MetadataWithTest{}, fmt.Errorf("invalid %s %q", field.name, field.value)
		}
	}
	return api.MetadataWithTest{Metadata: api.Metadata{Org: e.Organization, Repo: e.Repository, Branch: e.Branch, Variant: e.Variant}, Test: e.Test}, nil
}

// readGCSObject reads a GCS object using Prow's storage opener.
func readGCSObject(ctx context.Context, path string) ([]byte, error) {
	opener, err := prowio.NewOpener(ctx, "", "")
	if err != nil {
		return nil, fmt.Errorf("initializing GCS reader: %w", err)
	}
	data, err := prowio.ReadContent(ctx, logrus.WithField("test_plan", path), opener, path)
	if err != nil {
		return nil, fmt.Errorf("reading GCS object: %w", err)
	}
	return data, nil
}

// Read reads a local or GCS YAML file and returns its validated tests in order.
func (r Reader) Read(ctx context.Context, path string) ([]api.MetadataWithTest, error) {
	logrus.WithField("test_plan", path).Info("Reading test plan")
	data, err := r.readContent(ctx, path)
	if err != nil {
		return nil, err
	}
	return decode(data)
}

// readContent selects a storage backend based on the plan path.
func (r Reader) readContent(ctx context.Context, path string) ([]byte, error) {
	if strings.HasPrefix(path, "gs://") {
		if r.ReadObject != nil {
			return r.ReadObject(ctx, path)
		}
		return readGCSObject(ctx, path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading local file: %w", err)
	}
	return data, nil
}

// decode parses a YAML test plan and validates every entry before returning any tests.
func decode(data []byte) ([]api.MetadataWithTest, error) {
	var plan struct {
		Tests []entry `json:"tests"`
	}
	if err := yaml.UnmarshalStrict(data, &plan); err != nil {
		return nil, fmt.Errorf("decoding YAML: %w", err)
	}
	if len(plan.Tests) == 0 {
		return nil, errors.New("expected a non-empty tests list")
	}
	sources := make([]api.MetadataWithTest, 0, len(plan.Tests))
	for i, entry := range plan.Tests {
		source, err := entry.source()
		if err != nil {
			return nil, fmt.Errorf("tests[%d]: %w", i, err)
		}
		sources = append(sources, source)
	}
	return sources, nil
}
