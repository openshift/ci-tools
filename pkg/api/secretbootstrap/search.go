package secretbootstrap

import (
	"fmt"
	"reflect"
	"strings"
)

type secretConfigFilter struct {
	apply   func(*SecretConfig) bool
	explain string
}

// Filter secret that targets a specific destination.
func ByDestination(targetTo *SecretContext) secretConfigFilter {
	return secretConfigFilter{
		apply: func(sc *SecretConfig) bool {
			for i := range sc.To {
				target := &sc.To[i]
				if reflect.DeepEqual(targetTo, target) {
					return true
				}
			}
			return false
		},
		explain: fmt.Sprintf("to: %+v", *targetTo),
	}
}

// Filter secrets for which at least one destination matches a predicate function.
func ByDestinationFunc(predicate func(*SecretContext) bool) secretConfigFilter {
	return secretConfigFilter{
		apply: func(sc *SecretConfig) bool {
			for i := range sc.To {
				if predicate(&sc.To[i]) {
					return true
				}
			}
			return false
		},
		explain: "custom predicate",
	}
}

func ExplainFilters(filters ...secretConfigFilter) string {
	explanations := make([]string, len(filters))
	for i, f := range filters {
		explanations[i] = f.explain
	}
	return strings.Join(explanations, " - ")
}

// FindSecret returns the first secret that matches all filters. If found, the
// second argument represents the secret's index, -1 otherwise.
func FindSecret(secrets []SecretConfig, filters ...secretConfigFilter) (*SecretConfig, int) {
loop:
	for i := range secrets {
		for _, f := range filters {
			if !f.apply(&secrets[i]) {
				continue loop
			}
		}
		return &secrets[i], i
	}
	return nil, -1
}
