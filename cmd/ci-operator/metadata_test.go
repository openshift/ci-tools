package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/google/go-cmp/cmp"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/results"
	"github.com/openshift/ci-tools/pkg/secrets"
	"github.com/openshift/ci-tools/pkg/testhelper"

	prowapi "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	"sigs.k8s.io/prow/pkg/pod-utils/downwardapi"
)

func TestProwMetadata(t *testing.T) {
	tests := []struct {
		id             string
		jobSpec        *api.JobSpec
		namespace      string
		customMetadata map[string]string
	}{
		{
			id: "generate metadata",
			jobSpec: &api.JobSpec{
				JobSpec: downwardapi.JobSpec{
					Refs: &prowapi.Refs{
						Org:  "some-org",
						Repo: "some-repo",
					},
					ExtraRefs: []prowapi.Refs{
						{
							Org:  "some-extra-org",
							Repo: "some-extra-repo",
						},
					},
					ProwJobID: "some-prow-job-id",
				},
			},
			namespace:      "some-namespace",
			customMetadata: nil,
		},
		{
			id: "generate metadata with a custom metadata file",
			jobSpec: &api.JobSpec{
				JobSpec: downwardapi.JobSpec{
					Refs: &prowapi.Refs{
						Org:  "another-org",
						Repo: "another-repo",
					},
					ExtraRefs: []prowapi.Refs{
						{
							Org:  "another-extra-org",
							Repo: "another-extra-repo",
						},
						{
							Org:  "another-extra-org2",
							Repo: "another-extra-repo2",
						},
					},
					ProwJobID: "another-prow-job-id",
				},
			},
			namespace: "another-namespace",
			customMetadata: map[string]string{
				"custom-field1": "custom-value1",
				"custom-field2": "custom-value2",
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.id, func(t *testing.T) {
			err := verifyMetadata(tc.jobSpec, tc.namespace, tc.customMetadata)
			if err != nil {
				t.Fatalf("error while running test: %v", err)
			}
		})
	}
}

func verifyMetadata(jobSpec *api.JobSpec, namespace string, customMetadata map[string]string) error {
	tempDir, err := os.MkdirTemp("", "")
	if err != nil {
		return fmt.Errorf("unable to create temporary directory: %w", err)
	}
	defer os.RemoveAll(tempDir)
	if err := os.Setenv("ARTIFACTS", tempDir); err != nil {
		return err
	}

	metadataFile := filepath.Join(tempDir, "metadata.json")

	// Verify without custom metadata
	c := secrets.NewDynamicCensor()
	o := &options{
		jobSpec:   jobSpec,
		namespace: namespace,
		censor:    &c,
	}

	if err := o.writeMetadataJSON(); err != nil {
		return fmt.Errorf("error while writing metadata JSON: %w", err)
	}

	metadataFileContents, err := os.ReadFile(metadataFile)
	if err != nil {
		return fmt.Errorf("error reading metadata file: %w", err)
	}

	var writtenMetadata prowResultMetadata
	if err := json.Unmarshal(metadataFileContents, &writtenMetadata); err != nil {
		return fmt.Errorf("error parsing prow metadata: %w", err)
	}

	expectedMetadata := prowResultMetadata{
		Revision:      "1",
		Repo:          fmt.Sprintf("%s/%s", jobSpec.Refs.Org, jobSpec.Refs.Repo),
		Repos:         map[string]string{fmt.Sprintf("%s/%s", jobSpec.Refs.Org, jobSpec.Refs.Repo): ""},
		Pod:           jobSpec.ProwJobID,
		WorkNamespace: namespace,
	}

	for _, extraRef := range jobSpec.ExtraRefs {
		expectedMetadata.Repos[fmt.Sprintf("%s/%s", extraRef.Org, extraRef.Repo)] = ""
	}

	if !reflect.DeepEqual(expectedMetadata, writtenMetadata) {
		return fmt.Errorf("written metadata does not match expected metadata: %s", cmp.Diff(expectedMetadata, writtenMetadata))
	}

	testArtifactDirectory := filepath.Join(tempDir, "test-artifact-directory")
	if os.Mkdir(testArtifactDirectory, os.FileMode(0755)) != nil {
		return fmt.Errorf("unable to create artifact directory under temporary directory")
	}

	if len(customMetadata) > 0 {
		testJSON, err := json.MarshalIndent(customMetadata, "", "")
		if err != nil {
			return fmt.Errorf("error marshalling custom metadata: %w", err)
		}
		err = os.WriteFile(filepath.Join(testArtifactDirectory, "custom-prow-metadata.json"), testJSON, os.FileMode(0644))
		if err != nil {
			return fmt.Errorf("unable to create custom metadata file: %w", err)
		}
	}

	// Write a bunch of empty files that should be ignored
	var errs []error
	errs = append(errs, os.WriteFile(filepath.Join(testArtifactDirectory, "a-ignore1.txt"), []byte(``), os.FileMode(0644)))
	errs = append(errs, os.WriteFile(filepath.Join(testArtifactDirectory, "b-ignore1.txt"), []byte(`{"invalid-field1": "invalid-value1"}`), os.FileMode(0644)))
	errs = append(errs, os.WriteFile(filepath.Join(testArtifactDirectory, "d-ignore1.txt"), []byte(``), os.FileMode(0644)))
	errs = append(errs, os.WriteFile(filepath.Join(testArtifactDirectory, "e-ignore1.txt"), []byte(`{"invalid-field2": "invalid-value2"}`), os.FileMode(0644)))
	if err := utilerrors.NewAggregate(errs); err != nil {
		return fmt.Errorf("one or more of the empty *ignore files failed to write: %w", err)
	}

	if err := o.writeMetadataJSON(); err != nil {
		return fmt.Errorf("error while writing metadata JSON: %w", err)
	}

	metadataFileContents, err = os.ReadFile(metadataFile)
	if err != nil {
		return fmt.Errorf("error reading metadata file (second revision): %w", err)
	}

	if err = json.Unmarshal(metadataFileContents, &writtenMetadata); err != nil {
		return fmt.Errorf("error parsing prow metadata (second revision): %w", err)
	}

	revision := "1"
	if len(customMetadata) > 0 {
		revision = "2"
	}

	expectedMetadata.Revision = revision
	expectedMetadata.Metadata = customMetadata
	if !reflect.DeepEqual(expectedMetadata, writtenMetadata) {
		return fmt.Errorf("written metadata does not match expected metadata (second revision): %s", cmp.Diff(expectedMetadata, writtenMetadata))
	}

	return nil
}

func TestErrWroteJUnit(t *testing.T) {
	// this simulates the error chain bubbling up to the top of the call chain
	rootCause := errors.New("failure")
	reasonedErr := results.ForReason("something").WithError(rootCause).Errorf("couldn't do it: %v", rootCause)
	withJunit := &errWroteJUnit{wrapped: reasonedErr}
	defaulted := results.DefaultReason(withJunit)

	if !errors.Is(defaulted, &errWroteJUnit{}) {
		t.Error("expected the top-level error to still expose that we wrote jUnit")
	}
	testhelper.Diff(t, "reasons", results.Reasons(defaulted), []string{"something"})
}
