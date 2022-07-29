package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"reflect"
	"strings"

	"github.com/sirupsen/logrus"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/test-infra/prow/logrusutil"

	"github.com/openshift/ci-tools/pkg/api/secretbootstrap"
	"github.com/openshift/ci-tools/pkg/api/secretgenerator"
	"github.com/openshift/ci-tools/pkg/secrets"
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
	o.secrets.Bind(fs, os.Getenv, censor)
	if err := fs.Parse(os.Args[1:]); err != nil {
		logrus.WithError(err).Errorf("cannot parse args: %q", os.Args[1:])
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
		for paramName, params := range item.Params {
			if len(params) == 0 {
				return fmt.Errorf("at least one argument required for param: %s, itemName: %s", paramName, item.ItemName)
			}
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
		// The command completed with non zero exit code, standard streams *should* be available.
		if _, ok := err.(*exec.ExitError); ok {
			errStr := errBuf.String()
			if len(errStr) == 0 {
				return nil, fmt.Errorf(" : %w", err)
			}
			return nil, fmt.Errorf("%s: %w", errStr, err)
		}
		// At this point we can't easily tell why the command failed,
		// there is no guarantee neither stdout nor stderr are valid.
		return nil, fmt.Errorf("failed to run the command: %w", err)
	}

	if len(errBuf.Bytes()) != 0 {
		return nil, fmt.Errorf("command %q has error output", command)
	}

	stdout := outBuf.Bytes()
	if len(stdout) == 0 || len(bytes.TrimSpace(stdout)) == 0 {
		return nil, fmt.Errorf("command %q returned no output", command)
	}
	if string(bytes.TrimSpace(stdout)) == "null" {
		return nil, fmt.Errorf("command %s returned 'null' as output", command)
	}
	return stdout, nil
}

func updateSecrets(config secretgenerator.Config, client secrets.Client) error {
	var errs []error
	for _, item := range config {
		logger := logrus.WithField("item", item.ItemName)
		for _, field := range item.Fields {
			logger = logger.WithFields(logrus.Fields{
				"field":   field.Name,
				"command": field.Cmd,
			})
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
			f, err = ioutil.TempFile("", "ci-secret-generator")
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
	}

	if err := updateSecrets(o.config, client); err != nil {
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
