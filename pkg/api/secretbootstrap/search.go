package secretbootstrap

import (
	"fmt"
	"strings"
)

type secretConfigFilter struct {
	apply   func(*SecretConfig) bool
	explain string
}

// Filter secret that targets a specific namespace/name.
// If namespace isn't provided, the filter matches name only.
func ByNamespacedName(ns, name string) secretConfigFilter {
	return secretConfigFilter{
		apply: func(sc *SecretConfig) bool {
			for i := range sc.To {
				target := &sc.To[i]
				if ns == "" {
					if target.Name == name {
						return true
					}
				} else {
					if target.Name == name && target.Namespace == ns {
						return true
					}
				}
			}
			return false
		},
		explain: fmt.Sprintf("namespace/name: %s/%s", ns, name),
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
