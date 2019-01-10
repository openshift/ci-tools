package rehearse

import (
	"fmt"
	"io/ioutil"
	"testing"

	"github.com/sirupsen/logrus"

	prowconfig "k8s.io/test-infra/prow/config"

	v1 "k8s.io/api/core/v1"

	"k8s.io/client-go/kubernetes/fake"

	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/diff"
)

var dryFalse = false
var dryTrue = true
var testNamespace = "test-namespace"
var testRepo = "organization/project"
var testPrNumber = 1234
var testRepoPath = "/path/to/openshift/release"

func createPresubmitWithEnv(env []v1.EnvVar) *prowconfig.Presubmit {
	return &prowconfig.Presubmit{
		JobBase: prowconfig.JobBase{
			Spec: &v1.PodSpec{Containers: []v1.Container{{Env: env}}},
		},
	}
}

func makeCMKeyRef(key, name string) *v1.ConfigMapKeySelector {
	return &v1.ConfigMapKeySelector{
		LocalObjectReference: v1.LocalObjectReference{Name: name},
		Key:                  key,
	}
}

type FakeConfigFilesReader struct {
	files map[string][]byte
}

func (f *FakeConfigFilesReader) Read(path string) (string, error) {
	content, ok := f.files[path]
	if ok {
		return string(content), nil
	}

	return "", fmt.Errorf("Failed to read a file %s", path)
}

func TestCreate(t *testing.T) {
	testLogger := logrus.New()
	testLogger.SetOutput(ioutil.Discard)

	testCases := []struct {
		description     string
		neededConfigs   map[string]string
		fakeConfigFiles map[string][]byte
		expectedCM      *v1.ConfigMap
		expectedError   bool
	}{{
		description:     "reference to a bad file",
		neededConfigs:   map[string]string{"config-file": "bad file"},
		fakeConfigFiles: map[string][]byte{},
		expectedCM:      nil,
		expectedError:   true,
	}, {
		description:   "reference to a good file",
		neededConfigs: map[string]string{"config-file": "good-file"},
		fakeConfigFiles: map[string][]byte{
			"/path/to/openshift/release/ci-operator/config/good-file": []byte("good-file-content"),
		},
		expectedCM: &v1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "test-namespace",
				Name:      "rehearsal-ci-operator-configs-1234",
			},
			Data: map[string]string{"config-file": "good-file-content"},
		},
		expectedError: false,
	}, {
		description: "multiple files",
		neededConfigs: map[string]string{
			"first-file":  "ff-path",
			"second-file": "sf-path",
		},
		fakeConfigFiles: map[string][]byte{
			"/path/to/openshift/release/ci-operator/config/ff-path": []byte("first-file-content"),
			"/path/to/openshift/release/ci-operator/config/sf-path": []byte("second-file-content"),
		},
		expectedCM: &v1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "test-namespace",
				Name:      "rehearsal-ci-operator-configs-1234",
			},
			Data: map[string]string{
				"first-file":  "first-file-content",
				"second-file": "second-file-content",
			},
		},
		expectedError: false,
	},
	}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			fakeclient := fake.NewSimpleClientset().CoreV1().ConfigMaps(testNamespace)
			configs := NewCIOperatorConfigs(fakeclient, testPrNumber, testRepoPath, testLogger, dryFalse).(*ciOperatorConfigs)
			configs.reader = &FakeConfigFilesReader{files: tc.fakeConfigFiles}
			configs.neededConfigs = tc.neededConfigs

			err := configs.Create()

			if tc.expectedError && err == nil {
				t.Errorf("Expected Create() to return error")
				return
			}

			if !tc.expectedError {
				if err != nil {
					t.Errorf("Expected Create() to not return error, returned %v", err)
					return
				}

				createdCM, err := fakeclient.Get("rehearsal-ci-operator-configs-1234", metav1.GetOptions{})
				if err != nil {
					t.Errorf("Failed to get expected CM from fake client: %v", err)
					return
				}

				if !equality.Semantic.DeepEqual(tc.expectedCM, createdCM) {
					t.Errorf("Created CM differs from expected:\n%s", diff.ObjectDiff(tc.expectedCM, createdCM))
				}
			}
		})
	}
}

func TestFixupJob(t *testing.T) {
	testLogger := logrus.New()
	testLogger.SetOutput(ioutil.Discard)
	fakeclient := fake.NewSimpleClientset().CoreV1().ConfigMaps(testNamespace)

	testCases := []struct {
		description           string
		sourceEnv             []v1.EnvVar
		expectedEnv           []v1.EnvVar
		expectedNeededConfigs map[string]string
	}{{
		description:           "empty Env -> job not fixed up",
		sourceEnv:             []v1.EnvVar{},
		expectedEnv:           []v1.EnvVar{},
		expectedNeededConfigs: map[string]string{},
	}, {
		description:           "EnvVar.ValueFrom == nil -> job not fixed up",
		sourceEnv:             []v1.EnvVar{{Name: "Name", Value: "Value"}},
		expectedEnv:           []v1.EnvVar{{Name: "Name", Value: "Value"}},
		expectedNeededConfigs: map[string]string{},
	}, {
		description:           "EnvVar.ValueFrom.ConfigMapKeyRef == nil -> job not fixed up",
		sourceEnv:             []v1.EnvVar{{Name: "Name", ValueFrom: &v1.EnvVarSource{}}},
		expectedEnv:           []v1.EnvVar{{Name: "Name", ValueFrom: &v1.EnvVarSource{}}},
		expectedNeededConfigs: map[string]string{},
	}, {
		description:           "unrelated ConfigMap reference -> job not fixed up",
		sourceEnv:             []v1.EnvVar{{Name: "Name", ValueFrom: &v1.EnvVarSource{ConfigMapKeyRef: makeCMKeyRef("some-key", "some-cm")}}},
		expectedEnv:           []v1.EnvVar{{Name: "Name", ValueFrom: &v1.EnvVarSource{ConfigMapKeyRef: makeCMKeyRef("some-key", "some-cm")}}},
		expectedNeededConfigs: map[string]string{},
	}, {
		description: "ci-operator-configs reference -> job fixed up",
		sourceEnv: []v1.EnvVar{{Name: "Name", ValueFrom: &v1.EnvVarSource{
			ConfigMapKeyRef: makeCMKeyRef("some-name", "ci-operator-configs")}},
		},
		expectedEnv: []v1.EnvVar{{Name: "Name", ValueFrom: &v1.EnvVarSource{
			ConfigMapKeyRef: makeCMKeyRef("some-name", "rehearsal-ci-operator-configs-1234")}},
		},
		expectedNeededConfigs: map[string]string{"some-name": "organization/project/some-name"},
	},
	}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			sourceJob := createPresubmitWithEnv(tc.sourceEnv)
			expectedJob := createPresubmitWithEnv(tc.expectedEnv)

			configs := NewCIOperatorConfigs(fakeclient, testPrNumber, testRepoPath, testLogger, dryTrue).(*ciOperatorConfigs)
			configs.FixupJob(sourceJob, testRepo)
			if !equality.Semantic.DeepEqual(sourceJob, expectedJob) {
				t.Errorf("Fixed up presubmit differs from expected:\n%s", diff.ObjectDiff(expectedJob, sourceJob))
			}
			if !equality.Semantic.DeepEqual(configs.neededConfigs, tc.expectedNeededConfigs) {
				t.Errorf("Needed ci-operator configs differ from expected:\n%s", diff.ObjectDiff(tc.expectedNeededConfigs, configs.neededConfigs))
			}
		})
	}
}
