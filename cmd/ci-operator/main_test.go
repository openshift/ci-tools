package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/google/go-cmp/cmp"

	prowapi "k8s.io/test-infra/prow/apis/prowjobs/v1"
	"k8s.io/test-infra/prow/pod-utils/downwardapi"
	"k8s.io/utils/diff"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/load"
)

func TestSanitizeMessage(t *testing.T) {
	tests := []struct {
		name     string
		message  string
		expected string
	}{{
		name:     "pod name",
		message:  "...pod ci-op-4fg72pn0/unit...",
		expected: "...pod <PODNAME>/unit...",
	}, {
		name:     "ci-operator duration seconds",
		message:  "...after 39s (failed...",
		expected: "...after <DURATION> (failed...",
	}, {
		name:     "seconds-like pattern not replaced inside words",
		message:  "some hash is 'h4sh'",
		expected: "some hash is 'h4sh'",
	}, {
		name:     "ci-operator duration minutes",
		message:  "...after 1m39s (failed...",
		expected: "...after <DURATION> (failed...",
	}, {
		name:     "ci-operator duration hours",
		message:  "...after 69h1m39s (failed...",
		expected: "...after <DURATION> (failed...",
	}, {
		name:     "seconds duration",
		message:  "...PASS: TestRegistryProviderGet (2.83s)...",
		expected: "...PASS: TestRegistryProviderGet (<DURATION>)...",
	}, {
		name:     "ms duration",
		message:  "...PASS: TestRegistryProviderGet 510ms...",
		expected: "...PASS: TestRegistryProviderGet <DURATION>...",
	}, {
		name:     "spaced duration",
		message:  "...exited with code 1 after 00h 17m 40s...",
		expected: "...exited with code 1 after <DURATION>...",
	}, {
		name:     "ISO time",
		message:  "...time=\"2019-05-21T15:31:35Z\"...",
		expected: "...time=\"<ISO-DATETIME>\"...",
	}, {
		name:     "ISO DATE",
		message:  "...date=\"2019-05-21\"...",
		expected: "...date=\"<ISO-DATETIME>\"...",
	}, {
		name:     "UUID",
		message:  "...UUID:\"8f4e0db5-86a8-11e9-8c0a-12bbdc8a555a\"...",
		expected: "...UUID:\"<UUID>\"...",
	},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := sanitizeMessage(tc.message); got != tc.expected {
				t.Errorf("sanitizeMessage('%s') = '%s', expected '%s'", tc.message, got, tc.expected)
			}
		})
	}
}

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
	tempDir, err := ioutil.TempDir("", "")
	if err != nil {
		return fmt.Errorf("Unable to create temporary directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	metadataFile := filepath.Join(tempDir, "metadata.json")

	// Verify without custom metadata
	o := &options{
		artifactDir: tempDir,
		jobSpec:     jobSpec,
		namespace:   namespace,
	}

	if err := o.writeMetadataJSON(); err != nil {
		return fmt.Errorf("error while writing metadata JSON: %v", err)
	}

	metadataFileContents, err := ioutil.ReadFile(metadataFile)
	if err != nil {
		return fmt.Errorf("error reading metadata file: %v", err)
	}

	var writtenMetadata prowResultMetadata
	if err := json.Unmarshal(metadataFileContents, &writtenMetadata); err != nil {
		return fmt.Errorf("error parsing prow metadata: %v", err)
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
			return fmt.Errorf("error marshalling custom metadata: %v", err)
		}
		err = ioutil.WriteFile(filepath.Join(testArtifactDirectory, "custom-prow-metadata.json"), []byte(testJSON), os.FileMode(0644))
		if err != nil {
			return fmt.Errorf("unable to create custom metadata file: %v", err)
		}
	}

	// Write a bunch of empty files that should be ignored
	err = ioutil.WriteFile(filepath.Join(testArtifactDirectory, "a-ignore1.txt"), []byte(``), os.FileMode(0644))
	err = ioutil.WriteFile(filepath.Join(testArtifactDirectory, "b-ignore1.txt"), []byte(`{"invalid-field1": "invalid-value1"}`), os.FileMode(0644))
	err = ioutil.WriteFile(filepath.Join(testArtifactDirectory, "d-ignore1.txt"), []byte(``), os.FileMode(0644))
	err = ioutil.WriteFile(filepath.Join(testArtifactDirectory, "e-ignore1.txt"), []byte(`{"invalid-field2": "invalid-value2"}`), os.FileMode(0644))
	if err != nil {
		return fmt.Errorf("one or more of the empty *ignore files failed to write: %v", err)
	}

	if err := o.writeMetadataJSON(); err != nil {
		return fmt.Errorf("error while writing metadata JSON: %v", err)
	}

	metadataFileContents, err = ioutil.ReadFile(metadataFile)
	if err != nil {
		return fmt.Errorf("error reading metadata file (second revision): %v", err)
	}

	if err = json.Unmarshal(metadataFileContents, &writtenMetadata); err != nil {
		return fmt.Errorf("error parsing prow metadata (second revision): %v", err)
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

func TestGetResolverInfo(t *testing.T) {
	testCases := []struct {
		name     string
		opt      *options
		jobSpec  *api.JobSpec
		expected *load.ResolverInfo
	}{{
		name: "Only JobSpec Refs",
		opt: &options{
			resolverAddress: configResolverAddress,
		},
		jobSpec: &api.JobSpec{
			JobSpec: downwardapi.JobSpec{
				Refs: &prowapi.Refs{
					Org:     "testOrganization",
					Repo:    "testRepo",
					BaseRef: "testBranch",
				},
			},
		},
		expected: &load.ResolverInfo{
			Address: configResolverAddress,
			Org:     "testOrganization",
			Repo:    "testRepo",
			Branch:  "testBranch",
		},
	}, {
		name: "JobSpec Refs w/ vairant set via flag",
		opt: &options{
			resolverAddress: configResolverAddress,
			variant:         "v2",
		},
		jobSpec: &api.JobSpec{
			JobSpec: downwardapi.JobSpec{
				Refs: &prowapi.Refs{
					Org:     "testOrganization",
					Repo:    "testRepo",
					BaseRef: "testBranch",
				},
			},
		},
		expected: &load.ResolverInfo{
			Address: configResolverAddress,
			Org:     "testOrganization",
			Repo:    "testRepo",
			Branch:  "testBranch",
			Variant: "v2",
		},
	}, {
		name: "Ref with ExtraRefs",
		opt: &options{
			resolverAddress: configResolverAddress,
		},
		jobSpec: &api.JobSpec{
			JobSpec: downwardapi.JobSpec{
				Refs: &prowapi.Refs{
					Org:     "testOrganization",
					Repo:    "testRepo",
					BaseRef: "testBranch",
				},
				ExtraRefs: []prowapi.Refs{{
					Org:     "anotherOrganization",
					Repo:    "anotherRepo",
					BaseRef: "anotherBranch",
				}},
			},
		},
		expected: &load.ResolverInfo{
			Address: configResolverAddress,
			Org:     "testOrganization",
			Repo:    "testRepo",
			Branch:  "testBranch",
		},
	}, {
		name: "Incomplete refs not used",
		opt: &options{
			resolverAddress: configResolverAddress,
		},
		jobSpec: &api.JobSpec{
			JobSpec: downwardapi.JobSpec{
				Refs: &prowapi.Refs{
					Org:     "testOrganization",
					BaseRef: "testBranch",
				},
				ExtraRefs: []prowapi.Refs{{
					Org:     "anotherOrganization",
					Repo:    "anotherRepo",
					BaseRef: "anotherBranch",
				}},
			},
		},
		expected: &load.ResolverInfo{
			Address: configResolverAddress,
			Org:     "anotherOrganization",
			Repo:    "anotherRepo",
			Branch:  "anotherBranch",
		},
	}, {
		name: "Refs with single field overridden by options",
		opt: &options{
			resolverAddress: configResolverAddress,
			repo:            "anotherRepo",
			variant:         "v2",
		},
		jobSpec: &api.JobSpec{
			JobSpec: downwardapi.JobSpec{
				Refs: &prowapi.Refs{
					Org:     "testOrganization",
					Repo:    "testRepo",
					BaseRef: "testBranch",
				},
			},
		},
		expected: &load.ResolverInfo{
			Address: configResolverAddress,
			Org:     "testOrganization",
			Repo:    "anotherRepo",
			Branch:  "testBranch",
			Variant: "v2",
		},
	}, {
		name: "Only options",
		opt: &options{
			resolverAddress: configResolverAddress,
			org:             "testOrganization",
			repo:            "testRepo",
			branch:          "testBranch",
			variant:         "v2",
		},
		jobSpec: &api.JobSpec{},
		expected: &load.ResolverInfo{
			Address: configResolverAddress,
			Org:     "testOrganization",
			Repo:    "testRepo",
			Branch:  "testBranch",
			Variant: "v2",
		},
	}, {
		name: "All fields overridden by options",
		opt: &options{
			resolverAddress: configResolverAddress,
			org:             "anotherOrganization",
			repo:            "anotherRepo",
			branch:          "anotherBranch",
			variant:         "v2",
		},
		jobSpec: &api.JobSpec{
			JobSpec: downwardapi.JobSpec{
				Refs: &prowapi.Refs{
					Org:     "testOrganization",
					Repo:    "testRepo",
					BaseRef: "testBranch",
				},
			},
		},
		expected: &load.ResolverInfo{
			Address: configResolverAddress,
			Org:     "anotherOrganization",
			Repo:    "anotherRepo",
			Branch:  "anotherBranch",
			Variant: "v2",
		},
	}}
	for _, testCase := range testCases {
		actual := testCase.opt.getResolverInfo(testCase.jobSpec)
		if !reflect.DeepEqual(actual, testCase.expected) {
			t.Errorf("%s: Actual does not match expected:\n%s", testCase.name, diff.ObjectReflectDiff(testCase.expected, actual))
		}
	}
}
