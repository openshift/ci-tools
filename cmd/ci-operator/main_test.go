package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/google/go-cmp/cmp"

	rbacapi "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/client-go/kubernetes/scheme"
	prowapi "k8s.io/test-infra/prow/apis/prowjobs/v1"
	"k8s.io/test-infra/prow/pod-utils/downwardapi"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	fakectrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"

	imagev1 "github.com/openshift/api/image/v1"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/results"
	"github.com/openshift/ci-tools/pkg/secrets"
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

func TestBuildPartialGraph(t *testing.T) {
	testCases := []struct {
		name           string
		input          []api.Step
		targetName     string
		expectedErrors []error
	}{
		{
			name: "Missing input image results in human-readable error",
			input: []api.Step{
				steps.InputImageTagStep(
					&api.InputImageTagStepConfiguration{InputImage: api.InputImage{To: api.PipelineImageStreamTagReferenceRoot}},
					loggingclient.New(fakectrlruntimeclient.NewClientBuilder().WithRuntimeObjects(&imagev1.ImageStreamTag{ObjectMeta: metav1.ObjectMeta{Name: ":"}}).Build()),
					nil,
				),
				steps.SourceStep(api.SourceStepConfiguration{From: api.PipelineImageStreamTagReferenceRoot, To: api.PipelineImageStreamTagReferenceSource}, api.ResourceConfiguration{}, nil, nil, &api.JobSpec{}, nil, nil),
				steps.ProjectDirectoryImageBuildStep(
					api.ProjectDirectoryImageBuildStepConfiguration{
						From: api.PipelineImageStreamTagReferenceSource,
						ProjectDirectoryImageBuildInputs: api.ProjectDirectoryImageBuildInputs{
							Inputs: map[string]api.ImageBuildInputs{"cli": {Paths: []api.ImageSourcePath{{DestinationDir: ".", SourcePath: "/usr/bin/oc"}}}},
						},
						To: api.PipelineImageStreamTagReference("oc-bin-image"),
					},
					&api.ReleaseBuildConfiguration{}, api.ResourceConfiguration{}, nil, nil, nil, nil,
				),
				steps.OutputImageTagStep(api.OutputImageTagStepConfiguration{From: api.PipelineImageStreamTagReference("oc-bin-image")}, nil, nil),
				steps.ImagesReadyStep(steps.OutputImageTagStep(api.OutputImageTagStepConfiguration{From: api.PipelineImageStreamTagReference("oc-bin-image")}, nil, nil).Creates()),
			},
			targetName: "[images]",
			expectedErrors: []error{
				errors.New("steps are missing dependencies"),
				errors.New(`step [output::] is missing dependencies: <&api.internalImageStreamLink{name:"stable"}>, <&api.internalImageStreamTagLink{name:"pipeline", tag:"oc-bin-image", unsatisfiableError:""}>`),
				errors.New(`step oc-bin-image is missing dependencies: "cli" is neither an imported nor a built image`),
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			graph, err := api.BuildPartialGraph(tc.input, []string{tc.targetName})
			if err != nil {
				t.Fatalf("failed to build graph: %v", err)
			}

			// Apparently we only coincidentally validate the graph during the topologicalSort we do prior to printing it
			_, errs := graph.TopologicalSort()
			testhelper.Diff(t, "errors", errs, tc.expectedErrors, testhelper.EquateErrorMessage)
		})
	}
}

type fakeValidationStep struct {
	name string
	err  error
}

func (*fakeValidationStep) Inputs() (api.InputDefinition, error) { return nil, nil }
func (*fakeValidationStep) Run(ctx context.Context) error        { return nil }
func (*fakeValidationStep) Requires() []api.StepLink             { return nil }
func (*fakeValidationStep) Creates() []api.StepLink              { return nil }
func (f *fakeValidationStep) Name() string                       { return f.name }
func (*fakeValidationStep) Description() string                  { return "" }
func (*fakeValidationStep) Provides() api.ParameterMap           { return nil }
func (f *fakeValidationStep) Validate() error                    { return f.err }
func (*fakeValidationStep) Objects() []ctrlruntimeclient.Object  { return nil }

func TestValidateSteps(t *testing.T) {
	valid0 := fakeValidationStep{name: "valid0"}
	valid1 := fakeValidationStep{name: "valid1"}
	valid2 := fakeValidationStep{name: "valid2"}
	valid3 := fakeValidationStep{name: "valid3"}
	invalid0 := fakeValidationStep{
		name: "invalid0",
		err:  errors.New("invalid0"),
	}
	for _, tc := range []struct {
		name     string
		expected bool
		steps    api.OrderedStepList
	}{{
		name:     "empty graph",
		expected: true,
	}, {
		name:     "valid graph",
		expected: true,
		steps: api.OrderedStepList{{
			Step: &valid0,
			Children: []*api.StepNode{
				{Step: &valid1},
				{Step: &valid2},
			},
		}, {
			Step: &valid3,
		}},
	}, {
		name: "invalid graph",
		steps: api.OrderedStepList{{
			Step: &valid0,
			Children: []*api.StepNode{
				{Step: &valid1},
				{Step: &valid2},
			},
		}, {
			Step: &invalid0,
			Children: []*api.StepNode{
				{Step: &valid1},
				{Step: &valid2},
			},
		}},
	}} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateSteps(tc.steps)
			if (err == nil) != tc.expected {
				t.Errorf("got %v, want %v", err == nil, tc.expected)
			}
		})
	}
}

func TestExcludeContextCancelledErrors(t *testing.T) {
	testCases := []struct {
		id       string
		errs     []error
		expected []error
	}{
		{
			id: "no context cancelled errors, no changes expected",
			errs: []error{
				results.ForReason("step_failed").WithError(errors.New("oopsie")).Errorf("step foo failed: oopsie"),
				results.ForReason("step_failed").WithError(errors.New("oopsie")).Errorf("step bar failed: oopsie"),
			},
			expected: []error{
				results.ForReason("step_failed").WithError(errors.New("oopsie")).Errorf("step foo failed: oopsie"),
				results.ForReason("step_failed").WithError(errors.New("oopsie")).Errorf("step bar failed: oopsie"),
			},
		},
		{
			id: "context cancelled errors, changes expected",
			errs: []error{
				results.ForReason("step_failed").WithError(errors.New("oopsie")).Errorf("step foo failed: oopsie"),
				results.ForReason("step_failed").WithError(context.Canceled).Errorf("step bar failed: %v", context.Canceled),
			},
			expected: []error{
				results.ForReason("step_failed").WithError(errors.New("oopsie")).Errorf("step foo failed: oopsie"),
			},
		},
	}

	for _, tc := range testCases {
		actualErrs := excludeContextCancelledErrors(tc.errs)
		if diff := cmp.Diff(actualErrs, tc.expected, testhelper.EquateErrorMessage); diff != "" {
			t.Fatal(diff)
		}
	}
}

func TestMultiStageParams(t *testing.T) {
	testCases := []struct {
		id             string
		inputParams    stringSlice
		expectedParams map[string]string
		testConfig     []api.TestStepConfiguration
		expectedErrs   []string
	}{
		{
			id:          "Add params",
			inputParams: stringSlice{[]string{"PARAM1=VAL1", "PARAM2=VAL2"}},
			testConfig: []api.TestStepConfiguration{
				{
					MultiStageTestConfigurationLiteral: &api.MultiStageTestConfigurationLiteral{
						Environment: map[string]string{
							"OTHERPARAM": "OTHERVAL",
						},
					},
				},
			},
			expectedParams: map[string]string{
				"PARAM1":     "VAL1",
				"PARAM2":     "VAL2",
				"OTHERPARAM": "OTHERVAL",
			},
		},
		{
			id:          "Override existing param",
			inputParams: stringSlice{[]string{"PARAM1=NEWVAL", "PARAM2=VAL2"}},
			testConfig: []api.TestStepConfiguration{
				{
					MultiStageTestConfigurationLiteral: &api.MultiStageTestConfigurationLiteral{
						Environment: map[string]string{
							"PARAM1": "VAL1",
						},
					},
				},
			},
			expectedParams: map[string]string{
				"PARAM1": "NEWVAL",
				"PARAM2": "VAL2",
			},
		},
		{
			id:             "invalid params",
			inputParams:    stringSlice{[]string{"PARAM1", "PARAM2"}},
			expectedParams: map[string]string{},
			expectedErrs: []string{
				"could not parse multi-stage-param: PARAM1 is not in the format key=value",
				"could not parse multi-stage-param: PARAM2 is not in the format key=value",
			},
		},
	}

	t.Parallel()

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.id, func(t *testing.T) {
			t.Parallel()
			configSpec := api.ReleaseBuildConfiguration{
				Tests: tc.testConfig,
			}

			o := &options{
				multiStageParamOverrides: tc.inputParams,
				configSpec:               &configSpec,
			}

			errs := overrideMultiStageParams(o)
			actualParams := make(map[string]string)

			for _, test := range o.configSpec.Tests {
				if test.MultiStageTestConfigurationLiteral != nil {
					for name, val := range test.MultiStageTestConfigurationLiteral.Environment {
						actualParams[name] = val
					}
				}

				if test.MultiStageTestConfiguration != nil {
					for name, val := range test.MultiStageTestConfiguration.Environment {
						actualParams[name] = val
					}
				}
			}

			if errs == nil {
				if diff := cmp.Diff(tc.expectedParams, actualParams); diff != "" {
					t.Errorf("actual does not match expected, diff: %s", diff)
				}
			}

			var expectedErr error
			if len(tc.expectedErrs) > 0 {
				var errorsList []error
				for _, err := range tc.expectedErrs {
					errorsList = append(errorsList, errors.New(err))
				}
				expectedErr = utilerrors.NewAggregate(errorsList)
			}
			if diff := cmp.Diff(errs, expectedErr, testhelper.EquateErrorMessage); diff != "" {
				t.Fatal(diff)
			}
		})
	}
}

func TestDependencyOverrides(t *testing.T) {
	testCases := []struct {
		id           string
		inputParams  stringSlice
		expectedDeps map[string]string
		testConfig   []api.TestStepConfiguration
		expectedErrs []string
	}{
		{
			id:          "Override dependency",
			inputParams: stringSlice{[]string{"OO_INDEX=registry.mystuff.com:5000/pushed/myimage"}},
			testConfig: []api.TestStepConfiguration{
				{
					MultiStageTestConfigurationLiteral: &api.MultiStageTestConfigurationLiteral{
						Test: []api.LiteralTestStep{
							{
								As: "step1",
								Dependencies: []api.StepDependency{
									{
										Name: "ci-index",
										Env:  "OO_INDEX",
									},
									{
										Name: "cool-image",
										Env:  "OTHER_THING",
									},
								},
							},
						},
					},
				},
			},
			expectedDeps: map[string]string{
				"OO_INDEX":    "registry.mystuff.com:5000/pushed/myimage",
				"OTHER_THING": "cool-image",
			},
		},
		{
			id:          "No matching dependency for override, dependencies untouched",
			inputParams: stringSlice{[]string{"NOT_FOUND=NOT_UPDATES"}},
			testConfig: []api.TestStepConfiguration{
				{
					MultiStageTestConfigurationLiteral: &api.MultiStageTestConfigurationLiteral{
						Test: []api.LiteralTestStep{
							{
								As: "step1",
								Dependencies: []api.StepDependency{
									{
										Name: "ci-index",
										Env:  "OO_INDEX",
									},
									{
										Name: "cool-image",
										Env:  "OTHER_THING",
									},
								},
							},
						},
					},
				},
			},
			expectedDeps: map[string]string{
				"OO_INDEX":    "ci-index",
				"OTHER_THING": "cool-image",
			},
		},
		{
			id:          "invalid params",
			inputParams: stringSlice{[]string{"NOT_GOOD", "ALSO_NOT_GOOD"}},
			expectedErrs: []string{
				"could not parse dependency-override-param: NOT_GOOD is not in the format key=value",
				"could not parse dependency-override-param: ALSO_NOT_GOOD is not in the format key=value",
			},
		},
		{
			id: "Override dependency using test-level dependency override",
			testConfig: []api.TestStepConfiguration{
				{
					MultiStageTestConfigurationLiteral: &api.MultiStageTestConfigurationLiteral{
						DependencyOverrides: map[string]string{
							"OO_INDEX": "registry.mystuff.com:5000/pushed/myimage",
						},
						Test: []api.LiteralTestStep{
							{
								As: "step1",
								Dependencies: []api.StepDependency{
									{
										Name: "ci-index",
										Env:  "OO_INDEX",
									},
									{
										Name: "cool-image",
										Env:  "OTHER_THING",
									},
								},
							},
						},
					},
				},
			},
			expectedDeps: map[string]string{
				"OO_INDEX":    "registry.mystuff.com:5000/pushed/myimage",
				"OTHER_THING": "cool-image",
			},
		},
		{
			id:          "Input param dependency takes precedence",
			inputParams: stringSlice{[]string{"OO_INDEX=registry.mystuff.com:5000/pushed/myimage"}},
			testConfig: []api.TestStepConfiguration{
				{
					MultiStageTestConfigurationLiteral: &api.MultiStageTestConfigurationLiteral{
						DependencyOverrides: map[string]string{
							"OO_INDEX": "registry.mystuff.com:5000/pushed/myimage2",
						},
						Test: []api.LiteralTestStep{
							{
								As: "step1",
								Dependencies: []api.StepDependency{
									{
										Name: "ci-index",
										Env:  "OO_INDEX",
									},
									{
										Name: "cool-image",
										Env:  "OTHER_THING",
									},
								},
							},
						},
					},
				},
			},
			expectedDeps: map[string]string{
				"OO_INDEX":    "registry.mystuff.com:5000/pushed/myimage",
				"OTHER_THING": "cool-image",
			},
		},
	}

	t.Parallel()

	for i := range testCases {
		tc := testCases[i]
		t.Run(tc.id, func(t *testing.T) {
			t.Parallel()

			configSpec := api.ReleaseBuildConfiguration{
				Tests: tc.testConfig,
			}

			o := &options{
				dependencyOverrides: tc.inputParams,
				configSpec:          &configSpec,
			}

			errs := overrideTestStepDependencyParams(o)
			actualDeps := make(map[string]string)

			for _, test := range o.configSpec.Tests {
				if test.MultiStageTestConfigurationLiteral != nil {
					for _, step := range test.MultiStageTestConfigurationLiteral.Test {
						for _, dependency := range step.Dependencies {
							if dependency.PullSpec != "" {
								actualDeps[dependency.Env] = dependency.PullSpec
							} else {
								actualDeps[dependency.Env] = dependency.Name
							}
						}
					}
				}
			}

			if errs == nil {
				if diff := cmp.Diff(tc.expectedDeps, actualDeps); diff != "" {
					t.Errorf("actual does not match expected, diff: %s", diff)
				}
			}

			var expectedErr error
			if len(tc.expectedErrs) > 0 {
				var errorsList []error
				for _, err := range tc.expectedErrs {
					errorsList = append(errorsList, errors.New(err))
				}
				expectedErr = utilerrors.NewAggregate(errorsList)
			}
			if diff := cmp.Diff(errs, expectedErr, testhelper.EquateErrorMessage); diff != "" {
				t.Fatal(diff)
			}
		})
	}
}

func TestGenerateAuthorAccessRoleBinding(t *testing.T) {
	testCases := []struct {
		id       string
		authors  []string
		expected *rbacapi.RoleBinding
	}{
		{
			id:      "basic case",
			authors: []string{"a", "e"},
			expected: &rbacapi.RoleBinding{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "ci-op-author-access",
					Namespace: "ci-op-xxxx",
				},
				Subjects: []rbacapi.Subject{{Kind: "Group", Name: "a-group"}, {Kind: "Group", Name: "e-group"}},
				RoleRef: rbacapi.RoleRef{
					Kind: "ClusterRole",
					Name: "admin",
				},
			},
		},
		{
			id:      "no duplicated authors",
			authors: []string{"a", "a"},
			expected: &rbacapi.RoleBinding{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "ci-op-author-access",
					Namespace: "ci-op-xxxx",
				},
				Subjects: []rbacapi.Subject{{Kind: "Group", Name: "a-group"}},
				RoleRef: rbacapi.RoleRef{
					Kind: "ClusterRole",
					Name: "admin",
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.id, func(t *testing.T) {
			actual := generateAuthorAccessRoleBinding("ci-op-xxxx", tc.authors)
			if diff := cmp.Diff(tc.expected, actual, testhelper.RuntimeObjectIgnoreRvTypeMeta); diff != "" {
				t.Fatal(diff)
			}
		})
	}
}

func TestHandleTargetAdditionalSuffix(t *testing.T) {
	testCases := []struct {
		name                   string
		targetAdditionalSuffix string
		targets                stringSlice
		expectedTargets        stringSlice
		tests                  []api.TestStepConfiguration
		expectedTests          []api.TestStepConfiguration
		jobSpec                api.JobSpec
		expectedJobSpec        api.JobSpec
	}{
		{
			name:            "no target-additional-suffix set",
			targets:         stringSlice{[]string{"e2e"}},
			expectedTargets: stringSlice{[]string{"e2e"}},
			tests:           []api.TestStepConfiguration{{As: "e2e"}},
			expectedTests:   []api.TestStepConfiguration{{As: "e2e"}},
			jobSpec:         api.JobSpec{Target: "e2e"},
			expectedJobSpec: api.JobSpec{Target: "e2e"},
		},
		{
			name:                   "basic target-additional-suffix set",
			targetAdditionalSuffix: "1",
			targets:                stringSlice{[]string{"e2e"}},
			expectedTargets:        stringSlice{[]string{"e2e-1"}},
			tests:                  []api.TestStepConfiguration{{As: "e2e"}},
			expectedTests:          []api.TestStepConfiguration{{As: "e2e-1"}},
			jobSpec:                api.JobSpec{Target: "e2e"},
			expectedJobSpec:        api.JobSpec{Target: "e2e-1", TargetAdditionalSuffix: "1"},
		},
		{
			name:                   "target-additional-suffix set with multiple targets",
			targetAdditionalSuffix: "1",
			targets:                stringSlice{[]string{"e2e", "unit"}},
			expectedTargets:        stringSlice{[]string{"e2e-1", "unit-1"}},
			tests:                  []api.TestStepConfiguration{{As: "e2e"}, {As: "unit"}},
			expectedTests:          []api.TestStepConfiguration{{As: "e2e-1"}, {As: "unit-1"}},
			jobSpec:                api.JobSpec{Target: "e2e"},
			expectedJobSpec:        api.JobSpec{Target: "e2e-1", TargetAdditionalSuffix: "1"},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			o := &options{
				targetAdditionalSuffix: tc.targetAdditionalSuffix,
				configSpec: &api.ReleaseBuildConfiguration{
					Tests: tc.tests,
				},
				jobSpec: &tc.jobSpec,
				targets: tc.targets,
			}

			handleTargetAdditionalSuffix(o)

			if diff := cmp.Diff(tc.expectedTargets.values, o.targets.values); diff != "" {
				t.Fatalf("expectedTargets differ from actual, diff: %s", diff)
			}
			if diff := cmp.Diff(tc.expectedTests, o.configSpec.Tests); diff != "" {
				t.Fatalf("expectedTests differ from actual, diff: %s", diff)
			}
			if diff := cmp.Diff(tc.expectedJobSpec.Target, o.jobSpec.Target); diff != "" {
				t.Fatalf("expectedJobSpec Target differs from actual, diff: %s", diff)
			}
		})
	}
}
