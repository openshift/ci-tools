package config

import (
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/ghodss/yaml"

	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/openshift/ci-tools/pkg/util/gzip"
)

// WhitelistConfig holds a list of repositories mapped by organization
type WhitelistConfig struct {
	Whitelist map[string][]string `json:"whitelist,omitempty"`
}

func (w *WhitelistConfig) IsWhitelisted(info *Info) bool {
	if whiteRepos, ok := w.Whitelist[info.Org]; ok {
		if sets.New[string](whiteRepos...).Has(info.Repo) {
			return true
		}
	}
	return false
}

// WhitelistOptions holds the required flags to load the whitelist configuration
type WhitelistOptions struct {
	whitelistFile   string
	WhitelistConfig WhitelistConfig
}

// Validate validates if the whitelist cofiguration file actual exists.
func (o *WhitelistOptions) Validate() error {
	if o.whitelistFile != "" {
		info, err := os.Stat(o.whitelistFile)
		if os.IsNotExist(err) {
			return fmt.Errorf("The file that specified in --whitelist-file does not exist: %v", o.whitelistFile)
		}

		if info.IsDir() {
			return fmt.Errorf("The file that specified in --whitelist-file is a directory: %v", o.whitelistFile)
		}

		bytes, err := gzip.ReadFileMaybeGZIP(o.whitelistFile)
		if err != nil {
			return fmt.Errorf("Couldn't read whitelist configuration file: %v", o.whitelistFile)
		}
		if err := yaml.Unmarshal(bytes, &o.WhitelistConfig); err != nil {
			return errors.New("Couldn't unmarshal whitelist configuration")
		}
	}

	return nil
}

func (o *WhitelistOptions) Bind(fs *flag.FlagSet) {
	fs.StringVar(&o.whitelistFile, "whitelist-file", "", "File of the repository whitelist configuration")
}
