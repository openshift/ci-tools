package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/google/go-cmp/cmp"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/client-go/kubernetes/scheme"
	prowapi "k8s.io/test-infra/prow/apis/prowjobs/v1"
	"k8s.io/test-infra/prow/pod-utils/downwardapi"
	"k8s.io/utils/diff"
	fakectrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"

	imagev1 "github.com/openshift/api/image/v1"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/load"
	"github.com/openshift/ci-tools/pkg/results"
	"github.com/openshift/ci-tools/pkg/steps"
	"github.com/openshift/ci-tools/pkg/steps/loggingclient"
	"github.com/openshift/ci-tools/pkg/testhelper"
)

func init() {
	if err := imagev1.AddToScheme(scheme.Scheme); err != nil {
		panic(fmt.Sprintf("failed to add imagev1 to scheme: %v", err))
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
	if err := os.Setenv("ARTIFACTS", tempDir); err != nil {
		return err
	}

	metadataFile := filepath.Join(tempDir, "metadata.json")

	// Verify without custom metadata
	o := &options{
		jobSpec:   jobSpec,
		namespace: namespace,
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
		err = ioutil.WriteFile(filepath.Join(testArtifactDirectory, "custom-prow-metadata.json"), testJSON, os.FileMode(0644))
		if err != nil {
			return fmt.Errorf("unable to create custom metadata file: %v", err)
		}
	}

	// Write a bunch of empty files that should be ignored
	var errs []error
	errs = append(errs, ioutil.WriteFile(filepath.Join(testArtifactDirectory, "a-ignore1.txt"), []byte(``), os.FileMode(0644)))
	errs = append(errs, ioutil.WriteFile(filepath.Join(testArtifactDirectory, "b-ignore1.txt"), []byte(`{"invalid-field1": "invalid-value1"}`), os.FileMode(0644)))
	errs = append(errs, ioutil.WriteFile(filepath.Join(testArtifactDirectory, "d-ignore1.txt"), []byte(``), os.FileMode(0644)))
	errs = append(errs, ioutil.WriteFile(filepath.Join(testArtifactDirectory, "e-ignore1.txt"), []byte(`{"invalid-field2": "invalid-value2"}`), os.FileMode(0644)))
	if err := utilerrors.NewAggregate(errs); err != nil {
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
		name: "JobSpec Refs w/ variant set via flag",
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

func TestErrWroteJUnit(t *testing.T) {
	// this simulates the error chain bubbling up to the top of the call chain
	rootCause := errors.New("failure")
	reasonedErr := results.ForReason("something").WithError(rootCause).Errorf("couldn't do it: %v", rootCause)
	withJunit := &errWroteJUnit{wrapped: reasonedErr}
	defaulted := results.DefaultReason(withJunit)

	if !errors.Is(defaulted, &errWroteJUnit{}) {
		t.Error("expected the top-level error to still expose that we wrote jUnit")
	}
	if results.FullReason(defaulted) != "something" {
		t.Errorf(`expected full reason for error to be "something", but got %q"`, results.FullReason(defaulted))
	}
}

func TestBuildPartialGraph(t *testing.T) {
	testCases := []struct {
		name             string
		input            []api.Step
		targetName       string
		expectedErrorMsg string
	}{
		{
			name: "Missing input image results in human-readable error",
			input: []api.Step{
				steps.InputImageTagStep(
					api.InputImageTagStepConfiguration{To: api.PipelineImageStreamTagReferenceRoot},
					loggingclient.New(fakectrlruntimeclient.NewFakeClient(&imagev1.ImageStreamTag{ObjectMeta: metav1.ObjectMeta{Name: ":"}})),
					nil,
				),
				steps.SourceStep(api.SourceStepConfiguration{From: api.PipelineImageStreamTagReferenceRoot, To: api.PipelineImageStreamTagReferenceSource}, api.ResourceConfiguration{}, nil, &api.JobSpec{}, nil, nil),
				steps.ProjectDirectoryImageBuildStep(
					api.ProjectDirectoryImageBuildStepConfiguration{
						From: api.PipelineImageStreamTagReferenceSource,
						ProjectDirectoryImageBuildInputs: api.ProjectDirectoryImageBuildInputs{
							Inputs: map[string]api.ImageBuildInputs{"cli": {Paths: []api.ImageSourcePath{{DestinationDir: ".", SourcePath: "/usr/bin/oc"}}}},
						},
						To: api.PipelineImageStreamTagReference("oc-bin-image"),
					},
					&api.ReleaseBuildConfiguration{}, api.ResourceConfiguration{}, nil, nil, nil,
				),
				steps.OutputImageTagStep(api.OutputImageTagStepConfiguration{From: api.PipelineImageStreamTagReference("oc-bin-image")}, nil, nil),
				steps.ImagesReadyStep(steps.OutputImageTagStep(api.OutputImageTagStepConfiguration{From: api.PipelineImageStreamTagReference("oc-bin-image")}, nil, nil).Creates()),
			},
			targetName:       "[images]",
			expectedErrorMsg: "steps are missing dependencies",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			steps, err := api.BuildPartialGraph(tc.input, []string{tc.targetName})
			if err != nil {
				t.Fatalf("failed to build graph: %v", err)
			}

			// Apparently we only coincidentally validate the graph during the topologicalSort we do prior to printing it
			_, err = topologicalSort(steps)
			if err == nil {
				return
			}
			if err.Error() != tc.expectedErrorMsg {
				t.Errorf("expected error message %q, got %q", tc.expectedErrorMsg, err.Error())
			}
		})
	}
}

func TestLoadLeaseCredentials(t *testing.T) {
	dir, err := ioutil.TempDir("", "test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	leaseServerCredentialsFile := filepath.Join(dir, "leaseServerCredentialsFile")
	if err := ioutil.WriteFile(leaseServerCredentialsFile, []byte("ci-new:secret-new"), 0644); err != nil {
		t.Fatal(err)
	}

	leaseServerCredentialsInvalidFile := filepath.Join(dir, "leaseServerCredentialsInvalidFile")
	if err := ioutil.WriteFile(leaseServerCredentialsInvalidFile, []byte("no-colon"), 0644); err != nil {
		t.Fatal(err)
	}

	testCases := []struct {
		name                       string
		leaseServerCredentialsFile string
		expectedUsername           string
		passwordGetterVerify       func(func() []byte) error
		expectedErr                error
	}{
		{
			name:                       "valid credential file",
			leaseServerCredentialsFile: leaseServerCredentialsFile,
			expectedUsername:           "ci-new",
			passwordGetterVerify: func(passwordGetter func() []byte) error {
				p := string(passwordGetter())
				if diff := cmp.Diff("secret-new", p); diff != "" {
					return fmt.Errorf("actual does not match expected, diff: %s", diff)
				}
				return nil
			},
		},
		{
			name:                       "wrong credential file",
			leaseServerCredentialsFile: leaseServerCredentialsInvalidFile,
			expectedErr:                fmt.Errorf("got invalid content of lease server credentials file which must be of the form '<username>:<passwrod>'"),
		},
	}

	for _, tc := range testCases {

		t.Run(tc.name, func(t *testing.T) {
			username, passwordGetter, err := loadLeaseCredentials(tc.leaseServerCredentialsFile)
			if diff := cmp.Diff(tc.expectedUsername, username); diff != "" {
				t.Errorf("actual does not match expected, diff: %s", diff)
			}
			if diff := cmp.Diff(tc.expectedErr, err, testhelper.EquateErrorMessage); diff != "" {
				t.Errorf("actualError does not match expectedError, diff: %s", diff)
			}
			if tc.passwordGetterVerify != nil {
				if err := tc.passwordGetterVerify(passwordGetter); err != nil {
					t.Errorf("unexpcected error: %v", err)
				}
			}
		})
	}
}
