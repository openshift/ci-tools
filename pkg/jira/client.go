package jira

import (
	"flag"
	"fmt"

	"github.com/andygrunwald/go-jira"
	"github.com/hashicorp/go-retryablehttp"
	"github.com/sirupsen/logrus"

	"k8s.io/test-infra/pkg/flagutil"
	"k8s.io/test-infra/prow/config/secret"
)

const DefaultHost = "https://issues.redhat.com/"

type Options struct {
	Username     string
	PasswordPath string
	Host         string
}

var _ flagutil.OptionGroup = &Options{}

// AddFlags injects options into the given FlagSet.
func (o *Options) AddFlags(fs *flag.FlagSet) {
	fs.StringVar(&o.Username, "jira-username", "", "Username to use when authenticating to Jira.")
	fs.StringVar(&o.PasswordPath, "jira-password-path", "", "Path to the file containing the password to use when authenticating to Jira.")
	fs.StringVar(&o.Host, "jira-host", DefaultHost, "Jira API host.")
}

// Validate validates options.
func (o *Options) Validate(dryRun bool) error {
	if o.Username == "" {
		return fmt.Errorf("--jira-username is required")
	}

	if o.PasswordPath == "" {
		return fmt.Errorf("--jira-password-path is required")
	}

	if o.Host == "" {
		return fmt.Errorf("--jira-host is required")
	}

	return nil
}

// Client creates a retrying, logging client for Jira given the options
func (o *Options) Client(secretAgent *secret.Agent) (*jira.Client, error) {
	client := retryablehttp.NewClient()
	client.Logger = &logrusLeveledLogger{logger: logrus.WithField("client", "jira")}

	if err := secretAgent.Add(o.PasswordPath); err != nil {
		return nil, fmt.Errorf("error loading Jira password: %w", err)
	}
	return jira.NewClient((&jira.BasicAuthTransport{
		Username: o.Username,
		Password: string(secretAgent.GetSecret(o.PasswordPath)),
		// Jira is notorious for sending us nonsense 500s on requests
		// and in general being very flaky, so we use a retryable client
		Transport: client.StandardClient().Transport,
	}).Client(), o.Host)
}

type logrusLeveledLogger struct {
	logger *logrus.Entry
}

// fieldsForContext translates a list of context fields to a
// logrus format; any items that don't conform to our expectations
// are omitted
func (l *logrusLeveledLogger) fieldsForContext(context ...interface{}) logrus.Fields {
	fields := logrus.Fields{}
	for i := 0; i < len(context)-1; i += 2 {
		key, ok := context[i].(string)
		if !ok {
			continue
		}
		fields[key] = context[i+1]
	}
	return fields
}

func (l *logrusLeveledLogger) Error(msg string, context ...interface{}) {
	l.logger.WithFields(l.fieldsForContext(context...)).Error(msg)
}

func (l *logrusLeveledLogger) Info(msg string, context ...interface{}) {
	l.logger.WithFields(l.fieldsForContext(context...)).Info(msg)
}

func (l *logrusLeveledLogger) Debug(msg string, context ...interface{}) {
	l.logger.WithFields(l.fieldsForContext(context...)).Debug(msg)
}

func (l *logrusLeveledLogger) Warn(msg string, context ...interface{}) {
	l.logger.WithFields(l.fieldsForContext(context...)).Warn(msg)
}
