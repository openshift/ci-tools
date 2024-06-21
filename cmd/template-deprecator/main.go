package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/kataras/tablewriter"
	"github.com/sirupsen/logrus"

	"sigs.k8s.io/prow/pkg/flagutil"
	configflagutil "sigs.k8s.io/prow/pkg/flagutil/config"
	pluginflagutil "sigs.k8s.io/prow/pkg/flagutil/plugins"

	"github.com/openshift/ci-tools/pkg/deprecatetemplates"
)

type options struct {
	config        configflagutil.ConfigOptions
	plugins       pluginflagutil.PluginOptions
	allowlistPath string
	prune         bool
	printStats    bool
	hideTotals    bool
	blockNewJobs  flagutil.Strings
	checks        bool

	help bool
}

func bindOptions(fs *flag.FlagSet) *options {
	opt := &options{config: configflagutil.ConfigOptions{ConfigPathFlagName: "prow-config-path", JobConfigPathFlagName: "prow-jobs-dir"}}
	opt.config.AddFlags(fs)
	opt.plugins.AddFlags(fs)

	fs.StringVar(&opt.allowlistPath, "allowlist-path", "", "Path to template deprecation allowlist")
	fs.Var(&opt.blockNewJobs, "block-new-jobs", "If set, new jobs will be added to this blocker instead of to the 'unknown blocker' list. Can be set multiple times and can have either JIRA or JIRA:description form")
	fs.BoolVar(&opt.prune, "prune", false, "If set, remove from allowlist all jobs that either no longer exist or no longer use a template")
	fs.BoolVar(&opt.printStats, "stats", false, "If true, print template usage stats")
	fs.BoolVar(&opt.hideTotals, "hide-totals", false, "If true, hide totals in template usage stats")
	fs.BoolVar(&opt.checks, "checks", true, "If true (default), validate allowlist for correctness after update")

	return opt
}

func (o *options) validate() error {
	for param, value := range map[string]string{
		"--plugin-config":  o.plugins.PluginConfigPath,
		"--allowlist-path": o.allowlistPath,
	} {
		if value == "" {
			return fmt.Errorf("mandatory argument %s was not set", param)
		}
	}

	if err := o.config.Validate(false); err != nil {
		return err
	}

	return nil
}

func main() {
	opt := bindOptions(flag.CommandLine)
	flag.Parse()

	if opt.help {
		flag.Usage()
		os.Exit(0)
	}

	if err := opt.validate(); err != nil {
		logrus.WithError(err).Fatal("Invalid parameters")
	}

	agent, err := opt.plugins.PluginAgent()
	if err != nil {
		logrus.WithError(err).Fatal("Failed to read Prow plugin configuration")
	}
	pluginCfg := agent.Config().ConfigUpdater

	configAgent, err := opt.config.ConfigAgent()
	if err != nil {
		logrus.WithError(err).Fatal("failed to load Prow configuration")
	}
	prowCfg := configAgent.Config()

	newJobBlockers := deprecatetemplates.JiraHints{}
	for _, value := range opt.blockNewJobs.Strings() {
		jira := strings.SplitN(value, ":", 2)
		switch len(jira) {
		case 1:
			newJobBlockers[jira[0]] = ""
		case 2:
			newJobBlockers[jira[0]] = jira[1]
		default:
			logrus.WithError(err).Fatal("invalid --block-new-jobs value")
		}
	}

	enforcer, err := deprecatetemplates.NewEnforcer(opt.allowlistPath, newJobBlockers)
	if err != nil {
		logrus.WithError(err).Fatal("Failed to initialize template deprecator")
	}

	enforcer.LoadTemplates(pluginCfg)
	if err := enforcer.ProcessJobs(prowCfg); err != nil {
		logrus.WithError(err).Fatal("Failed process jobs")
	}

	if opt.prune {
		enforcer.Prune()
	}

	if opt.printStats {
		header, footer, data := enforcer.Stats(opt.hideTotals)
		table := tablewriter.NewWriter(os.Stdout)
		table.SetHeader(header)
		table.SetFooter(footer)
		table.AppendBulk(data)
		table.Render()
	}

	if opt.checks {
		if violations := enforcer.Validate(); len(violations) > 0 {
			fmt.Printf("ERROR: Template deprecation allowlist has errors:\n")
			for idx, violation := range violations {
				fmt.Printf("\nERROR: %d)\n", idx+1)
				fmt.Printf("%s\n", violation)
			}
			fmt.Println()
			logrus.Fatalf("Template deprecation allowlist failed validation")
		}
	}

	if err := enforcer.SaveAllowlist(opt.allowlistPath); err != nil {
		logrus.WithError(err).Fatal("Failed to save template deprecation allowlist")
	}
}
