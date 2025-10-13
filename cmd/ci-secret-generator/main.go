package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"reflect"
	"strings"

	"github.com/sirupsen/logrus"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/prow/pkg/logrusutil"

	"github.com/openshift/ci-tools/pkg/api/secretbootstrap"
	"github.com/openshift/ci-tools/pkg/api/secretgenerator"
	gsm "github.com/openshift/ci-tools/pkg/gsm-secrets"
	"github.com/openshift/ci-tools/pkg/prowconfigutils"
	"github.com/openshift/ci-tools/pkg/secrets"
)

const (
	execCmdRunErrAction            = "run"
	execCmdValidateStdoutErrAction = "validate stdout of"
	execCmdValidateStderrErrAction = "validate stderr of"
	execCmdErrFmt                  = "failed to %s command %q: %w\n%s:\n%s\n%s:\n%s"
)

var (
	errExecCmdNotEmptyStderr = errors.New("stderr is not empty")
	errExecCmdNoStdout       = errors.New("no output returned")
	errExecCmdNullStdout     = errors.New("'null' output returned")
)

type options struct {
	secrets secrets.CLIOptions

	logLevel            string
	configPath          string
	bootstrapConfigPath string
	outputFile          string
	dryRun              bool
	validate            bool
	validateOnly        bool
	maxConcurrency      int
	disabledClusters    sets.Set[string]

	enableGsmSync      bool
	gcpProjectConfig   gsm.Config
	gsmCredentialsFile string

	config          secretgenerator.Config
	bootstrapConfig secretbootstrap.Config
}

func parseOptions(censor *secrets.DynamicCensor) options {
	var o options
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	fs.BoolVar(&o.dryRun, "dry-run", true, "Whether to actually create the secrets in vault.")
	fs.StringVar(&o.configPath, "config", "", "Path to the config file to use for this tool.")
	fs.StringVar(&o.bootstrapConfigPath, "bootstrap-config", "", "Path to the config file used for bootstrapping cluster secrets after using this tool.")
	fs.BoolVar(&o.validate, "validate", true, "Validate that the items created from this tool are used in bootstrapping")
	fs.BoolVar(&o.validateOnly, "validate-only", false, "If the tool should exit after the validation")
	fs.StringVar(&o.outputFile, "output-file", "", "output file for dry-run mode")
	fs.StringVar(&o.logLevel, "log-level", "info", fmt.Sprintf("Log level is one of %v.", logrus.AllLevels))
	fs.IntVar(&o.maxConcurrency, "concurrency", 1, "Maximum number of concurrent in-flight goroutines to BitWarden.")

	fs.BoolVar(&o.enableGsmSync, "enable-gsm-sync", false, "Whether to enable syncing cluster-init secrets to GSM.")
	fs.StringVar(&o.gsmCredentialsFile, "gsm-credentials-file", "", "Path to GCP service account credentials.")

	o.secrets.Bind(fs, os.Getenv, censor)
	if err := fs.Parse(os.Args[1:]); err != nil {
		logrus.WithError(err).Errorf("cannot parse args: %q", os.Args[1:])
	}

	if o.enableGsmSync {
		gcpConfig, err := gsm.GetConfigFromEnv()
		if err != nil {
			logrus.WithError(err).Error("Failed to get GCP config from environment, GSM sync will fail")
		}
		o.gcpProjectConfig = gcpConfig
	}

	return o
}

func (o *options) validateOptions() error {
	level, err := logrus.ParseLevel(o.logLevel)
	if err != nil {
		return fmt.Errorf("invalid log level specified: %w", err)
	}
	logrus.SetLevel(level)
	if !o.dryRun {
		if err := o.secrets.Validate(); err != nil {
			return err
		}
	}
	if o.configPath == "" {
		return errors.New("--config is empty")
	}
	if o.validate && o.bootstrapConfigPath == "" {
		return errors.New("--bootstrap-config is required with --validate")
	}
	return nil
}

func (o *options) completeOptions(censor *secrets.DynamicCensor) error {
	if err := o.secrets.Complete(censor); err != nil {
		return err
	}

	var err error
	o.config, err = secretgenerator.LoadConfigFromPath(o.configPath)
	if err != nil {
		return err
	}

	if o.bootstrapConfigPath != "" {
		if err := secretbootstrap.LoadConfigFromFile(o.bootstrapConfigPath, &o.bootstrapConfig); err != nil {
			return fmt.Errorf("couldn't load the bootstrap config: %w", err)
		}
	}

	prowDisabledClustersList, err := prowconfigutils.ProwDisabledClusters(nil)
	if err != nil {
		logrus.WithError(err).Warn("Failed to get Prow disable clusters")
	}
	o.disabledClusters = sets.New[string](prowDisabledClustersList...)

	return o.validateConfig()
}

func cmdEmptyErr(itemIndex, entryIndex int, entry string) error {
	return fmt.Errorf("config[%d].%s[%d]: empty field not allowed for cmd if name is specified", itemIndex, entry, entryIndex)
}

func (o *options) validateConfig() error {
	for i, item := range o.config {
		if item.ItemName == "" {
			return fmt.Errorf("config[%d].itemName: empty key is not allowed", i)
		}

		for fieldIndex, field := range item.Fields {
			if field.Name != "" && field.Cmd == "" {
				return cmdEmptyErr(i, fieldIndex, "fields")
			}
		}
		var hasCluster bool
		for paramName, params := range item.Params {
			if len(params) == 0 {
				return fmt.Errorf("at least one argument required for param: %s, itemName: %s", paramName, item.ItemName)
			}
			if paramName == "cluster" {
				hasCluster = true
			}
		}
		if !hasCluster {
			return fmt.Errorf("failed to find params['cluster'] in the %d item with name %q", i, item.ItemName)
		}
	}
	return nil
}

func executeCommand(command string) ([]byte, error) {
	cmd := exec.Command("bash", "-o", "errexit", "-o", "nounset", "-o", "pipefail", "-c", command)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	if err := cmd.Run(); err != nil {
		stderr := errBuf.Bytes()
		stdout := outBuf.Bytes()
		// The command completed with non zero exit code, standard streams *should* be available.
		_, partialStreams := err.(*exec.ExitError)
		return nil, fmtExecCmdErr(execCmdRunErrAction, command, err, stdout, stderr, !partialStreams)
	}

	stderr := errBuf.Bytes()
	stdout := outBuf.Bytes()

	if len(stderr) != 0 {
		return nil, fmtExecCmdErr(execCmdValidateStderrErrAction, command,
			errExecCmdNotEmptyStderr, stdout, stderr, false)
	}

	if len(stdout) == 0 || len(bytes.TrimSpace(stdout)) == 0 {
		return nil, fmtExecCmdErr(execCmdValidateStdoutErrAction, command,
			errExecCmdNoStdout, stdout, stderr, false)
	}

	if string(bytes.TrimSpace(stdout)) == "null" {
		return nil, fmtExecCmdErr(execCmdValidateStdoutErrAction, command,
			errExecCmdNullStdout, stdout, stderr, false)
	}

	return stdout, nil
}

func fmtExecCmdErr(action, cmd string, wrappedErr error, stdout, stderr []byte, partialStreams bool) error {
	stdoutPreamble := "output"
	stderrPreamble := "error output"
	if partialStreams {
		stdoutPreamble = "output (may be incomplete)"
		stderrPreamble = "error output (may be incomplete)"
	}
	return fmt.Errorf(execCmdErrFmt, action, cmd, wrappedErr, stdoutPreamble,
		stdout, stderrPreamble, stderr)
}

func updateSecrets(config secretgenerator.Config, client secrets.Client, disabledClusters sets.Set[string]) error {
	var errs []error
	for _, item := range config {
		logger := logrus.WithField("item", item.ItemName)
		for _, field := range item.Fields {
			logger = logger.WithFields(logrus.Fields{
				"field":   field.Name,
				"command": field.Cmd,
				"cluster": field.Cluster,
			})
			if disabledClusters.Has(field.Cluster) {
				logger.Info("ignored field for disabled cluster")
				continue
			}
			logger.Info("processing field")
			out, err := executeCommand(field.Cmd)
			if err != nil {
				msg := "failed to generate field"
				logger.WithError(err).Error(msg)
				errs = append(errs, errors.New(msg))
				continue
			}
			if err := client.SetFieldOnItem(item.ItemName, field.Name, out); err != nil {
				msg := "failed to upload field"
				logger.WithError(err).Error(msg)
				errs = append(errs, errors.New(msg))
				continue
			}
		}

		// Adding the notes not empty check here since we dont want to overwrite any notes that might already be present
		// If notes have to be deleted, it would have to be a manual operation where the user goes to the bw web UI and removes
		// the notes
		if item.Notes != "" {
			logger = logger.WithFields(logrus.Fields{
				"notes": item.Notes,
			})
			logger.Info("adding notes")
			if err := client.UpdateNotesOnItem(item.ItemName, item.Notes); err != nil {
				msg := "failed to update notes"
				logger.WithError(err).Error(msg)
				errs = append(errs, errors.New(msg))
			}
		}
	}
	return utilerrors.NewAggregate(errs)
}

func main() {
	logrusutil.ComponentInit()
	censor := secrets.NewDynamicCensor()
	logrus.SetFormatter(logrusutil.NewFormatterWithCensor(logrus.StandardLogger().Formatter, &censor))
	o := parseOptions(&censor)
	if err := o.validateOptions(); err != nil {
		logrus.WithError(err).Fatal("invalid arguments.")
	}
	if err := o.completeOptions(&censor); err != nil {
		logrus.WithError(err).Fatal("failed to complete options.")
	}

	itemContextsFromConfig := itemContextsFromConfig(o.config)
	if o.validate {
		if err := validateContexts(itemContextsFromConfig, o.bootstrapConfig); err != nil {
			for _, err := range err.Errors() {
				logrus.WithError(err).Error("Invalid entry")
			}
			logrus.Fatal("Failed to validate secret entries.")
		}
	}
	if o.validateOnly {
		logrus.Info("Validation succeeded and --validate-only is set, exiting")
		return
	}

	if errs := generateSecrets(o, &censor); len(errs) > 0 {
		logrus.WithError(utilerrors.NewAggregate(errs)).Fatal("Failed to update secrets.")
	}
	logrus.Info("Updated secrets.")
}

func generateSecrets(o options, censor *secrets.DynamicCensor) (errs []error) {
	var client secrets.Client

	if o.dryRun {
		var err error
		var f *os.File
		if o.outputFile == "" {
			f, err = os.CreateTemp("", "ci-secret-generator")
			if err != nil {
				return append(errs, fmt.Errorf("failed to create tempfile: %w", err))
			}
			logrus.Infof("Writing secrets to %s", f.Name())
		} else {
			f, err = os.OpenFile(o.outputFile, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, os.ModePerm)
			if err != nil {
				return append(errs, fmt.Errorf("failed to open output file %q: %w", o.outputFile, err))
			}
		}
		client = secrets.NewDryRunClient(f)
	} else {
		var err error
		client, err = o.secrets.NewClient(censor)
		if err != nil {
			return append(errs, fmt.Errorf("failed to create secrets client: %w", err))
		}

		if o.enableGsmSync {
			client, err = secrets.NewGSMSyncDecorator(client, o.gcpProjectConfig, o.gsmCredentialsFile)
			if err != nil {
				return append(errs, fmt.Errorf("failed to enable GSM sync: %w", err))
			}
			logrus.Info("GSM sync enabled for cluster-init secret")
		}
	}

	if err := updateSecrets(o.config, client, o.disabledClusters); err != nil {
		errs = append(errs, fmt.Errorf("failed to update secrets: %w", err))
	}

	return errs
}

func itemContextsFromConfig(items secretgenerator.Config) []secretbootstrap.ItemContext {
	var itemContexts []secretbootstrap.ItemContext
	for _, item := range items {
		for _, field := range item.Fields {
			itemContexts = append(itemContexts, secretbootstrap.ItemContext{
				Item:  item.ItemName,
				Field: field.Name,
			})
		}
	}
	return itemContexts
}

func validateContexts(contexts []secretbootstrap.ItemContext, config secretbootstrap.Config) utilerrors.Aggregate {
	var errs []error
	for _, needle := range contexts {
		var found bool
		for _, secret := range config.Secrets {
			for _, haystack := range secret.From {
				haystack.Item = strings.TrimPrefix(haystack.Item, config.VaultDPTPPrefix+"/")
				if reflect.DeepEqual(needle, haystack) {
					found = true
				}
				for _, dc := range haystack.DockerConfigJSONData {
					ctx := secretbootstrap.ItemContext{
						Item:  strings.TrimPrefix(dc.Item, config.VaultDPTPPrefix+"/"),
						Field: dc.AuthField,
					}
					if reflect.DeepEqual(needle, ctx) {
						found = true
					}
				}
			}
		}
		if !found {
			errs = append(errs, fmt.Errorf("could not find context %v in bootstrap config", needle))
		}
	}
	return utilerrors.NewAggregate(errs)
}
