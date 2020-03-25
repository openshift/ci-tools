package steps

import (
	"fmt"
	"strings"

	coreapi "k8s.io/api/core/v1"
)

const (
	ooIndex            = "OO_INDEX"
	ooPackage          = "OO_PACKAGE"
	ooChannel          = "OO_CHANNEL"
	ooInstallNamespace = "OO_INSTALL_NAMESPACE"
	ooTargetNamespaces = "OO_TARGET_NAMESPACES"
)

// optionalOperator is the information needed by the optional operator installation
// steps to be able to subscribe and install an optional operator
type optionalOperator struct {
	// Index is the pullspec of an index image
	Index string
	// Package is the name of the operator package to be installed
	Package string
	// Channel is name of the operator channel to track
	Channel string
	// Namespace is the name of the namespace into which the operator and catalog
	// will be installed (optional)
	Namespace string
	// TargetNamespaces is a list of namespaces the operator will target. If empty,
	// all namespaces will be targeted.
	TargetNamespaces []string
}

// getter is a subset of api.Parameters
type getter interface {
	Get(name string) (string, error)
}

// resolveOptionalOperator extracts an optionalOperator instance from the
// given parameters. If no optional operator-related parameters are set, returns
// nil
func resolveOptionalOperator(parameters getter) (*optionalOperator, error) {
	var err error
	var oo optionalOperator

	required := map[string]*string{
		ooIndex:   &oo.Index,
		ooPackage: &oo.Package,
		ooChannel: &oo.Channel,
	}

	for param, valuePointer := range required {
		value, err := parameters.Get(param)
		if err != nil {
			return nil, err
		}
		*valuePointer = value
	}

	if oo.Namespace, err = parameters.Get(ooInstallNamespace); err != nil {
		oo.Namespace = ""
	}
	if targetNamespaces, err := parameters.Get(ooTargetNamespaces); err != nil || targetNamespaces == "" {
		oo.TargetNamespaces = nil
	} else {
		oo.TargetNamespaces = strings.Split(targetNamespaces, ",")
	}

	if oo.Index != "" || oo.Package != "" || oo.Channel != "" || oo.Namespace != "" || len(oo.TargetNamespaces) > 0 {
		for param, value := range required {
			if *value == "" {
				return nil, fmt.Errorf("at least one of optional operator parameters OO_* is set, but not the required parameter %s", param)
			}
		}
	}

	if oo.Index == "" && oo.Package == "" && oo.Channel == "" {
		return nil, nil
	}

	return &oo, nil
}

// asEnv creates EnvVar slice embeddable into a Container that will execute
// the optional operator installation step.
func (oo *optionalOperator) asEnv() []coreapi.EnvVar {
	env := []coreapi.EnvVar{
		{Name: ooIndex, Value: oo.Index},
		{Name: ooPackage, Value: oo.Package},
		{Name: ooChannel, Value: oo.Channel},
	}

	if oo.Namespace != "" {
		env = append(env, coreapi.EnvVar{
			Name:  ooInstallNamespace,
			Value: oo.Namespace,
		})
	}

	if len(oo.TargetNamespaces) > 0 {
		env = append(env, coreapi.EnvVar{
			Name:  ooTargetNamespaces,
			Value: strings.Join(oo.TargetNamespaces, ","),
		})
	}

	return env
}
