package steps

import (
	"bytes"
	"context"
	"fmt"
	"sync"

	"github.com/openshift/ci-operator/pkg/api"
)

type message struct {
	node *api.StepNode
	err  error
}

func Run(ctx context.Context, graph []*api.StepNode, dry bool) error {
	var seen []api.StepLink
	results := make(chan message)
	done := make(chan bool)
	ctxDone := ctx.Done()
	wg := &sync.WaitGroup{}
	wg.Add(len(graph))
	go func() {
		wg.Wait()
		done <- true
	}()

	for _, root := range graph {
		go runStep(ctx, root, results, dry)
	}

	var errors []error
	for {
		select {
		case <-ctxDone:
			return aggregateError(errors)
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
					if api.HasAllLinks(child.Step.Requires(), seen) {
						wg.Add(1)
						go runStep(ctx, child, results, dry)
					}
				}
			}
			wg.Done()
		case <-done:
			close(results)
			close(done)
			return aggregateError(errors)
		}
	}
}

func aggregateError(errors []error) error {
	var aggregateErr error
	if len(errors) == 0 {
		return nil
	}
	if len(errors) == 1 {
		return errors[0]
	}
	if len(errors) > 1 {
		message := bytes.Buffer{}
		for _, err := range errors {
			message.WriteString(fmt.Sprintf("\n  * %s", err.Error()))
		}
		aggregateErr = fmt.Errorf("some steps failed:%s", message.String())
	}
	return aggregateErr
}

func runStep(ctx context.Context, node *api.StepNode, out chan<- message, dry bool) {
	err := node.Step.Run(ctx, dry)
	out <- message{
		node: node,
		err:  err,
	}
}
