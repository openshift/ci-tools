package steps

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/junit"
	"github.com/openshift/ci-tools/pkg/results"
)

type message struct {
	node            *api.StepNode
	duration        time.Duration
	err             error
	additionalTests []*junit.TestCase
	stepDetails     api.CIOperatorStepDetails
}

func Run(ctx context.Context, graph api.StepGraph) (*junit.TestSuites, []api.CIOperatorStepDetails, []error) {
	var seen []api.StepLink
	executionResults := make(chan message)
	done := make(chan bool)
	ctxDone := ctx.Done()
	var interrupted bool
	wg := &sync.WaitGroup{}
	wg.Add(len(graph))
	go func() {
		wg.Wait()
		done <- true
	}()

	start := time.Now()
	for _, root := range graph {
		go runStep(ctx, root, executionResults)
	}

	suites := &junit.TestSuites{
		Suites: []*junit.TestSuite{
			{
				Name: "step graph",
			},
		},
	}
	suite := suites.Suites[0]
	var executionErrors []error
	var stepDetails []api.CIOperatorStepDetails
	for {
		select {
		case <-ctxDone:
			executionErrors = append(executionErrors, results.ForReason("interrupted").ForError(errors.New("execution cancelled")))
			interrupted = true
			ctxDone = nil
		case out := <-executionResults:
			testCase := &junit.TestCase{Name: out.node.Step.Description(), Duration: out.duration.Seconds()}
			stepDetails = append(stepDetails, out.stepDetails)
			if out.err != nil {
				testCase.FailureOutput = &junit.FailureOutput{Output: out.err.Error()}
				executionErrors = append(executionErrors, results.ForReason("step_failed").WithError(out.err).Errorf("step %s failed: %v", out.node.Step.Name(), out.err))
			} else {
				seen = append(seen, out.node.Step.Creates()...)
				if !interrupted {
					for _, child := range out.node.Children {
						// we can trigger a child if all of it's pre-requisites
						// have been completed and if it has not yet been triggered.
						// We can ignore the child if it does not have prerequisites
						// finished as we know that we will process it here again
						// when the last of its parents finishes.
						if api.HasAllLinks(child.Step.Requires(), seen) {
							wg.Add(1)
							go runStep(ctx, child, executionResults)
						}
					}
				}
			}

			// append all reported tests cases
			var testCases []*junit.TestCase
			if len(out.additionalTests) > 0 {
				testCases = out.additionalTests
			} else {
				testCases = []*junit.TestCase{testCase}
			}
			for _, test := range testCases {
				switch {
				case test.FailureOutput != nil:
					suite.NumFailed++
				case test.SkipMessage != nil:
					suite.NumSkipped++
				}
				suite.NumTests++
				suite.TestCases = append(suite.TestCases, test)
			}

			wg.Done()
		case <-done:
			close(executionResults)
			close(done)
			suite.Duration = time.Since(start).Seconds()
			return suites, stepDetails, executionErrors
		}
	}
}

// subtestReporter may be implemented by steps that can return an optional set of
// additional JUnit tests to report to the cluster.
type subtestReporter interface {
	SubTests() []*junit.TestCase
}

// substepReport allows steps to report substeps.
// TODO: Should this be merged with the subtestReporter?
type SubStepReporter interface {
	SubSteps() []api.CIOperatorStepDetailInfo
}

func runStep(ctx context.Context, node *api.StepNode, out chan<- message) {
	start := time.Now()
	err := node.Step.Run(ctx)
	var additionalTests []*junit.TestCase
	if reporter, ok := node.Step.(subtestReporter); ok {
		additionalTests = reporter.SubTests()
	}
	duration := time.Since(start)
	failed := err != nil
	finishedAt := start.Add(duration)

	var subSteps []api.CIOperatorStepDetailInfo
	if x, ok := node.Step.(SubStepReporter); ok {
		subSteps = x.SubSteps()
	}

	out <- message{
		node:            node,
		duration:        duration,
		err:             err,
		additionalTests: additionalTests,
		stepDetails: api.CIOperatorStepDetails{
			CIOperatorStepDetailInfo: api.CIOperatorStepDetailInfo{
				StepName:    node.Step.Name(),
				Description: node.Step.Description(),
				StartedAt:   &start,
				FinishedAt:  &finishedAt,
				Duration:    &duration,
				Manifests:   node.Step.Objects(),
				Failed:      &failed,
			},
			Substeps: subSteps,
		},
	}
}
