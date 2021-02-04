package release

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/registry"
	"github.com/openshift/ci-tools/pkg/util"
)

type test = api.MultiStageTestConfiguration
type testLiteral = api.MultiStageTestConfigurationLiteral

type registryOptions struct {
	*options
	list, resolve, tree bool
}

func newRegistryCommand(o *options) *cobra.Command {
	ret := cobra.Command{
		Use:   "registry",
		Short: "ci-operator step registry commands",
		Long: `Loads step registry components and writes them to stdout, optionally performing
resolution.`,
		RunE: func(_ *cobra.Command, args []string) error {
			return cmdRegistryListAll(o)
		},
	}
	ret.AddCommand(newCmdRegistryStep(o))
	ret.AddCommand(newCmdRegistryChain(o))
	ret.AddCommand(newCmdRegistryWorkflow(o))
	return &ret
}

func cmdRegistryListAll(o *options) error {
	if err := o.loadRegistry(); err != nil {
		return err
	}
	list(o.refs, "step ")
	list(o.chains, "chain ")
	list(o.workflows, "workflow ")
	return nil
}

func newCmdRegistryCommon[R any, T any](
	o *options,
	name string,
	m *map[string]T,
	cmd *cobra.Command,
	resolve func(registry.Resolver, string) (R, error),
	nameObj func(string, any) any,
	printTree func(string, any) error,
) *cobra.Command {
	ro := registryOptions{options: o}
	flags := cmd.Flags()
	flags.BoolVarP(&ro.list, "list", "l", false, "list file names instead of contents")
	flags.BoolVarP(&ro.resolve, "resolve", "r", false, "resolve components before printing")
	flags.BoolVar(&ro.tree, "tree", false, "display component and children in a tree")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if util.PopCount(ro.list, ro.resolve, ro.tree) > 1 {
			return errors.New("--list, --resolve, and --tree are mutually exclusive")
		}
		if err := ro.loadRegistry(); err != nil {
			return err
		}
		if ro.list {
			list(*m, "")
			return nil
		}
		var getFn func(string) (any, error)
		if ro.resolve {
			getFn = func(n string) (ret any, err error) {
				if ret, err = resolve(o.resolver, n); err != nil {
					err = fmt.Errorf("failed to resolve %s: %w", n, err)
				}
				return
			}
		} else if len(args) != 0 {
			getFn = func(n string) (ret any, err error) {
				ret, ok := (*m)[n]
				if !ok {
					err = fmt.Errorf("%s %q not found", name, n)
				}
				return
			}
		} else {
			getFn = func(n string) (ret any, err error) {
				return (*m)[n], nil
			}
		}
		if len(args) == 0 {
			args = util.SortedKeys(*m)
		}
		var printFn func(string, any) error
		if ro.tree {
			printFn = printTree
		} else {
			printFn = func(name string, x any) error {
				fmt.Println("---")
				return printYAML(nameObj(name, x))
			}
		}
		for _, n := range args {
			if x, err := getFn(n); err != nil {
				return err
			} else if err := printFn(n, x); err != nil {
				return err
			}
		}
		return nil
	}
	return cmd
}

func newCmdRegistryStep(o *options) *cobra.Command {
	return newCmdRegistryCommon(
		o, "step", (*map[string]api.LiteralTestStep)(&o.refs),
		&cobra.Command{Use: "step", Short: "commands for steps/references"},
		func(_ registry.Resolver, n string) (api.LiteralTestStep, error) {
			return o.refs[n], nil
		},
		ignoreName,
		func(_ string, x any) error {
			printTreeStep(x.(api.LiteralTestStep).As, 0)
			return nil
		})
}

func newCmdRegistryChain(o *options) *cobra.Command {
	return newCmdRegistryCommon(
		o, "chain", (*map[string]api.RegistryChain)(&o.chains),
		&cobra.Command{Use: "chain", Short: "commands for step chains"},
		func(r registry.Resolver, n string) (api.RegistryChain, error) {
			return r.ResolveChain(n)
		},
		ignoreName,
		func(_ string, x any) error {
			printTreeChain(o, x.(api.RegistryChain).As, 0)
			return nil
		})
}

func newCmdRegistryWorkflow(o *options) *cobra.Command {
	return newCmdRegistryCommon(
		o, "workflow", (*map[string]test)(&o.workflows),
		&cobra.Command{Use: "workflow", Short: "commands for workflows"},
		func(r registry.Resolver, n string) (testLiteral, error) {
			return r.ResolveWorkflow(n)
		},
		func(name string, x any) any {
			return &struct {
				test
				As string `json:"as"`
			}{x.(test), name}
		},
		func(name string, x any) error {
			printTreeWorkflow(o, name, x.(test), 0)
			return nil
		})
}

func ignoreName(_ string, x any) any {
	return x
}

func list[T any](m map[string]T, prefix string) {
	for _, n := range util.SortedKeys(m) {
		fmt.Printf("%s%s\n", prefix, n)
	}
}

func printTreeLevel(level uint, format string, args ...any) {
	for i := uint(0); i < level; i++ {
		fmt.Print("  ")
	}
	fmt.Printf(format, args...)
}

func printTreeSteps(o *options, steps []api.TestStep, level uint) {
	for _, s := range steps {
		if s.Chain != nil {
			printTreeChain(o, *s.Chain, level)
		} else if s.Reference != nil {
			printTreeStep(*s.Reference, level)
		} else if s.LiteralTestStep != nil {
			printTreeStep(s.LiteralTestStep.As, level)
		}
	}
}

func printTreeStep(name string, level uint) {
	printTreeLevel(level, "step: %s\n", name)
}

func printTreeChain(o *options, name string, level uint) {
	c := o.chains[name]
	printTreeLevel(level, "chain: %s\n", c.As)
	printTreeSteps(o, c.Steps, level+1)
}

func printTreeWorkflow(o *options, name string, w test, level uint) {
	fmt.Println("workflow:", name)
	fmt.Println("pre:")
	printTreeSteps(o, w.Pre, level+1)
	fmt.Println("test:")
	printTreeSteps(o, w.Test, level+1)
	fmt.Println("post:")
	printTreeSteps(o, w.Post, level+1)
}
