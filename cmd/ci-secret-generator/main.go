package main

import (
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"strings"

	"github.com/openshift/ci-tools/pkg/bitwarden"
	"github.com/sirupsen/logrus"
	"gopkg.in/yaml.v2"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/test-infra/prow/logrusutil"
)

type options struct {
	logLevel       string
	configPath     string
	bwUser         string
	dryRun         bool
	bwPasswordPath string
	maxConcurrency int

	config     []bitWardenItem
	bwPassword string
}

type bitWardenItem struct {
	ItemName   string         `yaml:"item_name"`
	Field      fieldGenerator `yaml:"field,omitempty"`
	Attachment fieldGenerator `yaml:"attachment,omitempty"`
	Attribute  fieldGenerator `yaml:"attribute,omitempty"`
}

type fieldGenerator struct {
	Name string `yaml:"name,omitempty"`
	Cmd  string `yaml:"cmd,omitempty"`
}

const (
	attributeTypePassword string = "password"
)

func parseOptions() options {
	var o options
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	fs.BoolVar(&o.dryRun, "dry-run", false, "Whether to actually create the secrets with oc command")
	fs.StringVar(&o.configPath, "config", "", "Path to the config file to use for this tool.")
	fs.StringVar(&o.bwUser, "bw-user", "", "Username to access BitWarden.")
	fs.StringVar(&o.bwPasswordPath, "bw-password-path", "", "Path to a password file to access BitWarden.")
	fs.StringVar(&o.logLevel, "log-level", "info", fmt.Sprintf("Log level is one of %v.", logrus.AllLevels))
	fs.IntVar(&o.maxConcurrency, "concurrency", 1, "Maximum number of concurrent in-flight goroutines to BitWarden.")
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
	if o.bwUser == "" {
		return fmt.Errorf("--bw-user is empty")
	}
	if o.bwPasswordPath == "" {
		return fmt.Errorf("--bw-password-path is empty")
	}
	if o.configPath == "" {
		return fmt.Errorf("--config is empty")
	}
	return nil
}

func (o *options) completeOptions(secrets sets.String) error {
	bytes, err := ioutil.ReadFile(o.bwPasswordPath)
	if err != nil {
		return err
	}
	o.bwPassword = strings.TrimSpace(string(bytes))
	secrets.Insert(o.bwPassword)

	bytes, err = ioutil.ReadFile(o.configPath)
	if err != nil {
		return err
	}

	err = yaml.Unmarshal(bytes, &o.config)
	if err != nil {
		return err
	}
	return o.validateCompletedOptions()
}

func (o *options) validateCompletedOptions() error {
	if o.bwPassword == "" {
		return fmt.Errorf("--bw-password-file was empty")
	}

	for i, bwItem := range o.config {
		if bwItem.ItemName == "" {
			return fmt.Errorf("config[%d].itemName: empty key is not allowed", i)
		}
		switch bwItem.Attribute.Name {
		case attributeTypePassword, "":
		default:
			return fmt.Errorf("config[%d].attribute: only the '%s' is supported, not %s", i, attributeTypePassword, bwItem.Attribute.Name)
		}
		if (bwItem.Field.Name != "" && bwItem.Field.Cmd == "") ||
			(bwItem.Attribute.Name != "" && bwItem.Attribute.Cmd == "") ||
			(bwItem.Attachment.Name != "" && bwItem.Attachment.Cmd == "") {
			return fmt.Errorf("config[%d]: empty field not allowed for cmd if name is specified for any of attribute, field or attachment", i)
		}
	}
	return nil
}

func executeCommand(command string) ([]byte, error) {
	cmd := strings.Fields(command)
	out, err := exec.Command(cmd[0], cmd[1:]...).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("bw cmd failed: %v", string(out))
	}
	return out, nil
}

func processItem(w io.Writer, cmd string, entryType string, dryRun bool, procesor func([]byte) error) error {
	out, err := executeCommand(cmd)
	if err != nil {
		return fmt.Errorf("failed to execute command: %w", err)
	}
	if dryRun {
		if _, err := fmt.Fprintf(w, "\t%s: %s", entryType, string(out)); err != nil {
			return err
		}
	} else {
		return procesor(out)
	}
	return nil
}

func updateSecrets(bwItems []bitWardenItem, bwClient bitwarden.Client, dryRun bool) error {
	if dryRun {
		logrus.Infof("Dry-Run enabled, writing secrets to stdout")
		if _, err := fmt.Fprintln(os.Stdout, "---"); err != nil {
			return err
		}
	}
	for _, bwItem := range bwItems {
		if dryRun {
			if _, err := fmt.Fprintf(os.Stdout, "Item: %s", bwItem.ItemName); err != nil {
				return err
			}
		}
		if bwItem.Field.Name != "" {
			if err := processItem(os.Stdout, bwItem.Field.Cmd, "Field", dryRun, func(out []byte) error {
				if err := bwClient.SetFieldOnItem(bwItem.ItemName, bwItem.Field.Name, out); err != nil {
					return fmt.Errorf("failed to set field item: %s, field: %s - %w", bwItem.ItemName, bwItem.Field.Name, err)
				}
				return nil
			}); err != nil {
				return err
			}
		}
		if bwItem.Attachment.Name != "" {
			if err := processItem(os.Stdout, bwItem.Attachment.Cmd, "Attachment", dryRun, func(out []byte) error {
				if err := bwClient.SetAttachmentOnItem(bwItem.ItemName, bwItem.Attachment.Name, out); err != nil {
					return fmt.Errorf("failed to set attachment, item: %s, attachment: %s - %w", bwItem.ItemName, bwItem.Attachment.Name, err)
				}
				return nil
			}); err != nil {
				return err
			}
		}
		if bwItem.Attribute.Name != "" {
			if err := processItem(os.Stdout, bwItem.Attribute.Cmd, "Password", dryRun, func(out []byte) error {
				if err := bwClient.SetPassword(bwItem.ItemName, out); err != nil {
					return fmt.Errorf("failed to set password, item: %s - %w", bwItem.ItemName, err)
				}
				return nil
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

func main() {
	// CLI tool which does the secret generation and uploading to bitwarden
	o := parseOptions()
	secrets := sets.NewString()
	logrus.SetFormatter(logrusutil.NewCensoringFormatter(logrus.StandardLogger().Formatter, func() sets.String {
		return secrets
	}))
	if err := o.validateOptions(); err != nil {
		logrus.WithError(err).Fatal("invalid arguments.")
	}
	if err := o.completeOptions(secrets); err != nil {
		logrus.WithError(err).Fatal("failed to complete options.")
	}
	bwClient, err := bitwarden.NewClient(o.bwUser, o.bwPassword, func(s string) {
		secrets.Insert(s)
	})
	if err != nil {
		logrus.WithError(err).Fatal("failed to get Bitwarden client.")
	}
	logrus.RegisterExitHandler(func() {
		if _, err := bwClient.Logout(); err != nil {
			logrus.WithError(err).Fatal("failed to logout.")
		}
	})
	defer logrus.Exit(0)

	// Upload the output to bitwarden
	if err := updateSecrets(o.config, bwClient, o.dryRun); err != nil {
		logrus.WithError(err).Fatalf("Failed to update secrets.")
	}
	logrus.Info("Updated secrets.")
}
