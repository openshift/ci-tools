package pagerdutyutil

import (
	"errors"
	"flag"
	"fmt"

	"github.com/PagerDuty/go-pagerduty"

	"sigs.k8s.io/prow/pkg/config/secret"
)

type Options struct {
	tokenFile string
}

func (o *Options) AddFlags(fs *flag.FlagSet) {
	fs.StringVar(&o.tokenFile, "pager-duty-token-file", "", "Location to a file containing the PagerDuty OAuth token")
}

func (o *Options) Validate(_ bool) error {
	if o.tokenFile == "" {
		return errors.New("--pager-duty-token-file is required")
	}

	return nil
}

func (o *Options) Client() (*pagerduty.Client, error) {
	if err := secret.Add(o.tokenFile); err != nil {
		return nil, fmt.Errorf("failed to load --pager-duty-token-file: %w", err)
	}
	return pagerduty.NewClient(string(secret.GetSecret(o.tokenFile))), nil
}
