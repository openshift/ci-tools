package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"

	"github.com/sirupsen/logrus"

	"sigs.k8s.io/yaml"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/steps"
)

func (o *options) resolveInputs(steps []api.Step) error {
	var inputs api.InputDefinition
	for _, step := range steps {
		definition, err := step.Inputs()
		if err != nil {
			return fmt.Errorf("could not determine inputs for step %s: %w", step.Name(), err)
		}
		inputs = append(inputs, definition...)
	}

	// a change in the config for the build changes the output
	cs := o.configSpec
	// The targetAdditionalSuffix should be trimmed for the input purposes as the intent is to have the different suffix resolve the same
	targetAdditionalSuffix := o.targetAdditionalSuffix
	if targetAdditionalSuffix != "" {
		cs = o.configSpec.DeepCopy()
		for i, test := range cs.Tests {
			for _, target := range o.targets.values {
				if test.As == target {
					suffix := fmt.Sprintf("-%s", targetAdditionalSuffix)
					logrus.Debugf("Trimming suffix: %s from: %s for input resolution", suffix, target)
					cs.Tests[i].As = strings.TrimSuffix(test.As, suffix)
					break
				}
			}
		}
	}
	configSpec, err := yaml.Marshal(cs)
	if err != nil {
		panic(err)
	}
	inputs = append(inputs, string(configSpec))
	if len(o.extraInputHash.values) > 0 {
		inputs = append(inputs, o.extraInputHash.values...)
	}

	// add the binary modification time and size (in lieu of a content hash)
	path, _ := exec.LookPath(os.Args[0])
	if len(path) == 0 {
		path = os.Args[0]
	}
	if stat, err := os.Stat(path); err == nil {
		logrus.Tracef("Using binary as hash: %s %d %d", path, stat.ModTime().UTC().Unix(), stat.Size())
		inputs = append(inputs, fmt.Sprintf("%d-%d", stat.ModTime().UTC().Unix(), stat.Size()))
	} else {
		logrus.WithError(err).Trace("Could not calculate info from current binary to add to input hash.")
	}

	sort.Strings(inputs)
	o.inputHash = inputHash(inputs)

	// input hash is unique for a given job definition and input refs
	if len(o.namespace) == 0 {
		o.namespace = "ci-op-{id}"
	}
	o.namespace = strings.Replace(o.namespace, "{id}", o.inputHash, -1)
	// TODO: instead of mutating this here, we should pass the parts of graph execution that are resolved
	// after the graph is created but before it is run down into the run step.
	o.jobSpec.SetNamespace(o.namespace)

	// If we can resolve the field, use it. If not, don't.
	if o.consoleHost != "" {
		logrus.Infof("Using namespace https://%s/k8s/cluster/projects/%s", o.consoleHost, o.namespace)
	} else {
		logrus.Infof("Using namespace %s", o.namespace)
	}

	return nil
}

func nodeNames(nodes []*api.StepNode) []string {
	var names []string
	for _, node := range nodes {
		name := node.Step.Name()
		if len(name) == 0 {
			name = fmt.Sprintf("<%T>", node.Step)
		}
		names = append(names, name)
	}
	return names
}

func printDigraph(w io.Writer, steps api.OrderedStepList) error {
	for i, step := range steps {
		req := step.Step.Requires()
		// Only the first `i` elements can fulfill the requirements since
		// `OrderedStepList` is a topological order.
		for _, other := range steps[:i] {
			if api.HasAnyLinks(req, other.Step.Creates()) {
				if _, err := fmt.Fprintf(w, "%s %s\n", step.Step.Name(), other.Step.Name()); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func calculateGraph(nodes api.OrderedStepList) (*api.CIOperatorStepGraph, []error) {
	if err := validateSteps(nodes); err != nil {
		return nil, err
	}
	var result api.CIOperatorStepGraph
	for i, n := range nodes {
		r := api.CIOperatorStepDetails{CIOperatorStepDetailInfo: api.CIOperatorStepDetailInfo{StepName: n.Step.Name(), Description: n.Step.Description()}}
		for _, requirement := range n.Step.Requires() {
			for _, inner := range nodes[:i] {
				if api.HasAnyLinks([]api.StepLink{requirement}, inner.Step.Creates()) {
					r.Dependencies = append(r.Dependencies, inner.Step.Name())
				}
			}
		}
		result = append(result, r)
	}

	return &result, nil
}

func validateSteps(nodes api.OrderedStepList) []error {
	var errs []error
	for _, n := range nodes {
		if err := n.Step.Validate(); err != nil {
			errs = append(errs, fmt.Errorf("step %q failed validation: %w", n.Step.Name(), err))
			if errors.Is(err, steps.NoLeaseClientErr) {
				errs = append(errs, errors.New("a lease client was required but none was provided, add the --lease-... arguments"))
			} else if errors.Is(err, steps.NoHiveClientErr) {
				errs = append(errs, errors.New("a Hive client was required but none was provided, add the --hive-kubeconfig argument"))
			}
		}
	}
	return errs
}
