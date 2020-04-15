package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/config"
	"github.com/openshift/ci-tools/pkg/steps/release"
)

func main() {
	var configDir string
	flag.StringVar(&configDir, "config-dir", "", "The directory containing configuration files.")
	flag.Parse()

	if configDir == "" {
		fmt.Println("The --config-dir flag is required but was not provided")
		os.Exit(1)
	}

	seen := map[api.ImageStreamTagReference][]*config.Info{}
	if err := config.OperateOnCIOperatorConfigDir(configDir, func(configuration *api.ReleaseBuildConfiguration, repoInfo *config.Info) error {
		// validation is implicit, so we don't need to do anything
		// but record the images we saw for future validation
		for _, tag := range release.PromotedTags(configuration) {
			seen[tag] = append(seen[tag], repoInfo)
		}
		return nil
	}); err != nil {
		fmt.Printf("error validating configuration files: %v\n", err)
		os.Exit(1)
	}

	var dupes []error
	for tag, infos := range seen {
		if len(infos) > 1 {
			formatted := []string{}
			for _, info := range infos {
				identifier := fmt.Sprintf("%s/%s@%s", info.Org, info.Repo, info.Branch)
				if info.Variant != "" {
					identifier = fmt.Sprintf("%s [%s]", identifier, info.Variant)
				}
				formatted = append(formatted, identifier)
			}
			dupes = append(dupes, fmt.Errorf("output tag %s/%s:%s is promoted from more than one place: %v", tag.Namespace, tag.Name, tag.Tag, strings.Join(formatted, ", ")))
		}
	}
	if len(dupes) > 0 {
		fmt.Println("non-unique image publication found: ")
		for _, dupe := range dupes {
			fmt.Printf("ERROR: %v\n", dupe)
		}
		os.Exit(1)
	}
}
