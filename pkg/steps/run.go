package steps

import (
	"bytes"
	"fmt"
	"sync"

	"github.com/openshift/ci-operator/pkg/api"
)

type message struct {
	node *api.StepNode
	err  error
}

func Run(graph []*api.StepNode) error {
	var seen []api.StepLink
	results := make(chan message)
	done := make(chan bool)
	wg := &sync.WaitGroup{}
	wg.Add(len(graph))
	go func() {
		wg.Wait()
		done <- true
	}()

	for _, root := range graph {
		go runStep(root, results)
	}

	var errors []error
	for {
		select {
		case out := <-results:
			if out.err != nil {
				errors = append(errors, out.err)
			} else {
				seen = append(seen, out.node.Step.Creates()...)
				for _, child := range out.node.Children {
					// we can trigger a child if all of it's pre-requisites
					// have been completed and if it has not yet been triggered.
					// We can ignore the child if it does not have prerequisites
					// finished as we know that we will process it here again
					// when the last of its parents finishes.
					if containsAll(child.Step.Requires(), seen) {
						wg.Add(1)
						go runStep(child, results)
					}
				}
			}
			wg.Done()
		case <-done:
			close(results)
			close(done)

			var aggregateErr error
			if len(errors) > 0 {
				message := bytes.Buffer{}
				for _, err := range errors {
					message.WriteString(fmt.Sprintf("%s\n", err.Error()))
				}
				aggregateErr = fmt.Errorf("encountered errors running steps:\n%s", message.String())
			}
			return aggregateErr
		}
	}
}

func runStep(node *api.StepNode, out chan<- message) {
	out <- message{
		node: node,
		err:  node.Step.Run(),
	}
}

func containsAll(needles, haystack []api.StepLink) bool {
	for _, needle := range needles {
		contains := false
		for _, hay := range haystack {
			if hay.Matches(needle) {
				contains = true
			}
		}
		if ! contains {
			return false
		}
	}
	return true
}
