// prowgen-efs publishes managed Prow jobs to EFS instead of storing them in Git.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

// options identifies the source checkout, ownership rules and publication mode.
type options struct {
	releaseRepo   string
	managedConfig string
	efsRoot       string
	candidateDir  string
	publish       bool
}

// main executes the command until completion or cancellation.
func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := newCommand().ExecuteContext(ctx); err != nil {
		logrus.WithError(err).Fatal("Publisher failed")
	}
}

// newCommand defines the publisher's flags and usage.
func newCommand() *cobra.Command {
	opts := &options{}
	cmd := &cobra.Command{
		Use:   "prowgen-efs",
		Short: "Generate managed Prow jobs and publish them to EFS",
		Long: `Publish jobs for repositories managed outside the Git job tree.

Select managed ci-operator configurations, generate and sanitize their jobs,
then validate and publish them to EFS. Existing job overrides are preserved.
Without --publish, only generate a preview; visible EFS jobs remain unchanged.`,
		Args:          cobra.NoArgs,
		RunE:          opts.run,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	flags := cmd.Flags()
	flags.StringVar(&opts.releaseRepo, "release-repo", ".", "Path to an openshift/release checkout")
	flags.StringVar(&opts.managedConfig, "managed-repos-config", "core-services/prow/_managed-repos.yaml", "Managed repository configuration, relative to release-repo or absolute")
	flags.StringVar(&opts.efsRoot, "efs-root", "/mnt/job-configs", "Shared job configuration root")
	flags.StringVar(&opts.candidateDir, "candidate-dir", "", "New directory outside efs-root in which to retain a preview")
	flags.BoolVar(&opts.publish, "publish", false, "Validate and publish jobs; otherwise only generate a preview")
	return cmd
}

// run connects Cobra's context and options to the publisher.
func (o *options) run(cmd *cobra.Command, _ []string) error {
	p := publisher{commands: execCommands{}, rename: os.Rename}
	return p.run(cmd.Context(), *o)
}

// complete resolves source and EFS paths before any work begins.
func (o *options) complete() error {
	source, err := filepath.EvalSymlinks(o.releaseRepo)
	if err != nil {
		return fmt.Errorf("resolving release checkout: %w", err)
	}
	o.releaseRepo, err = filepath.Abs(source)
	if err != nil {
		return err
	}
	if !filepath.IsAbs(o.managedConfig) {
		o.managedConfig = filepath.Join(o.releaseRepo, o.managedConfig)
	}
	if err := os.MkdirAll(o.efsRoot, 0755); err != nil {
		return fmt.Errorf("creating EFS root: %w", err)
	}
	o.efsRoot, err = resolvedPath(o.efsRoot)
	return err
}
