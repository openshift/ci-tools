package secretrecordingclient

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestValuesToCensor(t *testing.T) {
	var testCases = []struct {
		name   string
		input  *v1.Secret
		values []string
	}{
		{
			name: "basic case",
			input: &v1.Secret{
				Data:       map[string][]byte{"FOO": []byte("BAR"), "FAA": []byte("BOZ")},
				StringData: map[string]string{"foo": "bar", "faa": "boz"},
			},
			values: []string{"BAR", "BOZ", "bar", "boz"},
		},
		{
			name:  "secret ignored due to label",
			input: &v1.Secret{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"ci.openshift.io/skip-censoring": "true"}}, StringData: map[string]string{"foo": "bar"}},
		},
		{
			name: "values from annotations",
			input: &v1.Secret{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{
				"openshift.io/token-secret.value":                  "foo",
				"kubectl.kubernetes.io/last-applied-configuration": "bar",
			}}},
			values: []string{"bar", "foo"},
		},
		{
			name: "service-account namespace ignored",
			input: &v1.Secret{
				Type:       v1.SecretTypeServiceAccountToken,
				Data:       map[string][]byte{"namespace": []byte("foo")},
				StringData: map[string]string{"namespace": "bar"},
			},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if diff := cmp.Diff(testCase.values, valuesToCensor(testCase.input)); diff != "" {
				t.Errorf("%s: got incorrect values to censor: %s", testCase.name, diff)
			}
		})
	}
}
