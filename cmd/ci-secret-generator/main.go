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

	"github.com/sirupsen/logrus"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/test-infra/prow/logrusutil"
	"sigs.k8s.io/yaml"

	"github.com/openshift/ci-tools/pkg/api/secretbootstrap"
	"github.com/openshift/ci-tools/pkg/api/secretgenerator"
	"github.com/openshift/ci-tools/pkg/secrets"
	"github.com/openshift/ci-tools/pkg/util/gzip"
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
	fs.BoolVar(&o.dryRun, "dry-run", true, "Whether to actually create the secrets with bw command")
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
			return utilerrors.NewAggregate(err)
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
		cfgBytes, err := gzip.ReadFileMaybeGZIP(o.bootstrapConfigPath)
		if err != nil {
			return err
		}

		if err := yaml.Unmarshal(cfgBytes, &o.bootstrapConfig); err != nil {
			return err
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
		for attachmentIndex, attachment := range item.Fields {
			if attachment.Name != "" && attachment.Cmd == "" {
				return cmdEmptyErr(i, attachmentIndex, "attachments")
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
	out, err := exec.Command("bash", "-o", "errexit", "-o", "nounset", "-o", "pipefail", "-c", command).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("%s : %w", string(out), err)
	}
	if len(out) == 0 || len(bytes.TrimSpace(out)) == 0 {
		return nil, fmt.Errorf("command %q returned no output", command)
	}
	if string(bytes.TrimSpace(out)) == "null" {
		return nil, fmt.Errorf("command %s returned 'null' as output", command)
	}
	return out, nil
}

func updateSecrets(config secretgenerator.Config, client secrets.Client) error {
	var errs []error
	for _, bwItem := range config {
		logger := logrus.WithField("item", bwItem.ItemName)
		for _, field := range bwItem.Fields {
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
			if err := client.SetFieldOnItem(bwItem.ItemName, field.Name, out); err != nil {
				msg := "failed to upload field"
				logger.WithError(err).Error(msg)
				errs = append(errs, errors.New(msg))
				continue
			}
		}
		for _, attachment := range bwItem.Attachments {
			logger = logger.WithFields(logrus.Fields{
				"attachment": attachment.Name,
				"command":    attachment.Cmd,
			})
			logger.Info("processing attachment")
			out, err := executeCommand(attachment.Cmd)
			if err != nil {
				msg := "failed to generate attachment"
				logger.WithError(err).Error(msg)
				errs = append(errs, errors.New(msg))
				continue
			}
			if err := client.SetAttachmentOnItem(bwItem.ItemName, attachment.Name, out); err != nil {
				msg := "failed to upload attachment"
				logger.WithError(err).Error(msg)
				errs = append(errs, errors.New(msg))
				continue
			}
		}
		if bwItem.Password != "" {
			logger = logger.WithFields(logrus.Fields{
				"password": bwItem.Password,
			})
			logger.Info("processing password")
			out, err := executeCommand(bwItem.Password)
			if err != nil {
				msg := "failed to generate password"
				logger.WithError(err).Error(msg)
				errs = append(errs, errors.New(msg))
			} else {
				if err := client.SetPassword(bwItem.ItemName, out); err != nil {
					msg := "failed to upload password"
					logger.WithError(err).Error(msg)
					errs = append(errs, errors.New(msg))
				}
			}
		}

		// Adding the notes not empty check here since we dont want to overwrite any notes that might already be present
		// If notes have to be deleted, it would have to be a manual operation where the user goes to the bw web UI and removes
		// the notes
		if bwItem.Notes != "" {
			logger = logger.WithFields(logrus.Fields{
				"notes": bwItem.Notes,
			})
			logger.Info("adding notes")
			if err := client.UpdateNotesOnItem(bwItem.ItemName, bwItem.Notes); err != nil {
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
	// CLI tool which does the secret generation and uploading to bitwarden
	censor := secrets.NewDynamicCensor()
	logrus.SetFormatter(logrusutil.NewFormatterWithCensor(logrus.StandardLogger().Formatter, &censor))
	o := parseOptions(&censor)
	if err := o.validateOptions(); err != nil {
		logrus.WithError(err).Fatal("invalid arguments.")
	}
	if err := o.completeOptions(&censor); err != nil {
		logrus.WithError(err).Fatal("failed to complete options.")
	}

	bitWardenContexts := bitwardenContextsFor(o.config)
	if o.validate {
		if err := validateContexts(bitWardenContexts, o.bootstrapConfig); err != nil {
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

	var client secrets.Client
	logrus.RegisterExitHandler(func() {
		if client != nil {
			if _, err := client.Logout(); err != nil {
				logrus.WithError(err).Error("failed to logout.")
			}
		}
	})
	defer logrus.Exit(0)
	if o.dryRun {
		var err error
		var f *os.File
		if o.outputFile == "" {
			f, err = ioutil.TempFile("", "ci-secret-generator")
			if err != nil {
				logrus.WithError(err).Fatal("failed to create tempfile")
			}
			logrus.Infof("Writing secrets to %s", f.Name())
		} else {
			f, err = os.OpenFile(o.outputFile, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, os.ModePerm)
			if err != nil {
				logrus.WithError(err).Fatalf("failed to open output file %q", o.outputFile)
			}
		}
		client, err = secrets.NewDryRunClient(f)
		if err != nil {
			logrus.WithError(err).Fatal("failed to create dry-run mode client")
		}
	} else {
		var err error
		client, err = o.secrets.NewClient(&censor)
		if err != nil {
			logrus.WithError(err).Fatal("failed to create Bitwarden client")
		}
	}

	// Upload the output to bitwarden
	if err := updateSecrets(o.config, client); err != nil {
		logrus.WithError(err).Fatal("Failed to update secrets.")
	}
	logrus.Info("Updated secrets.")
}

func bitwardenContextsFor(items secretgenerator.Config) []secretbootstrap.BitWardenContext {
	var bitWardenContexts []secretbootstrap.BitWardenContext
	for _, bwItem := range items {
		for _, field := range bwItem.Fields {
			bitWardenContexts = append(bitWardenContexts, secretbootstrap.BitWardenContext{
				BWItem: bwItem.ItemName,
				Field:  field.Name,
			})
		}
		for _, attachment := range bwItem.Attachments {
			bitWardenContexts = append(bitWardenContexts, secretbootstrap.BitWardenContext{
				BWItem:     bwItem.ItemName,
				Attachment: attachment.Name,
			})
		}
		if bwItem.Password != "" {
			bitWardenContexts = append(bitWardenContexts, secretbootstrap.BitWardenContext{
				BWItem:    bwItem.ItemName,
				Attribute: secretbootstrap.AttributeTypePassword,
			})
		}
	}
	return bitWardenContexts
}

func validateContexts(contexts []secretbootstrap.BitWardenContext, config secretbootstrap.Config) utilerrors.Aggregate {
	var errs []error
	for _, needle := range contexts {
		var found bool
		for _, secret := range config.Secrets {
			for _, haystack := range secret.From {
				if reflect.DeepEqual(needle, haystack) {
					found = true
				}
				for _, dc := range haystack.DockerConfigJSONData {
					ctx := secretbootstrap.BitWardenContext{
						BWItem:     dc.BWItem,
						Attachment: dc.AuthBitwardenAttachment,
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
