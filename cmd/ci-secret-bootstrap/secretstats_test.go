package main

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	coreapi "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
)

func TestGenerateSecretStats(t *testing.T) {
	testCases := []struct {
		name                    string
		secretsByClusterAndName map[string]map[types.NamespacedName]coreapi.Secret
		expected                secretStats
	}{
		{
			name: "nil input",
		},
		{
			name: "basic case",
			secretsByClusterAndName: map[string]map[types.NamespacedName]coreapi.Secret{
				"app.ci": {
					types.NamespacedName{Name: "bar", Namespace: "ns1"}: coreapi.Secret{
						Data: map[string][]byte{
							"key1": []byte("value1"),
							"key2": []byte("value2"),
						},
					},
					types.NamespacedName{Name: "foo", Namespace: "ns2"}: coreapi.Secret{
						Data: map[string][]byte{
							"key2": []byte("a"),
							"key3": []byte("b"),
						},
					},
				},
				"b01": {
					types.NamespacedName{Name: "bar", Namespace: "ns3"}: coreapi.Secret{
						Data: map[string][]byte{
							"key": []byte("value1"),
						},
					},
				},
			},
			expected: secretStats{count: 3, median: 10},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual := generateSecretStats(tc.secretsByClusterAndName)
			if diff := cmp.Diff(tc.expected, actual, cmp.Comparer(func(x, y secretStats) bool {
				return cmp.Diff(x.count, y.count) == "" && cmp.Diff(x.median, y.median) == ""
			})); diff != "" {
				t.Errorf("%s differs from expected:\n%s", tc.name, diff)
			}
		})
	}
}
