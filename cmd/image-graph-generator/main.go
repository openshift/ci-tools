package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"

	graphql "github.com/shurcooL/graphql"
	"github.com/sirupsen/logrus"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/config"
	imagegraphgenerator "github.com/openshift/ci-tools/pkg/image-graph-generator"
)

type options struct {
	configsPath     string
	dgraphAddress   string
	imageMirrorPath string
}

func (o options) validate() error {
	if o.dgraphAddress == "" {
		return fmt.Errorf("--graphql-endpoint-address is not specified")
	}
	if o.configsPath == "" {
		return fmt.Errorf("--ci-operator-configs-path is not specified")
	}
	if o.imageMirrorPath == "" {
		return fmt.Errorf("--image-mirroring-path is not specified")
	}
	return nil
}

func parseOptions() options {
	var o options
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	fs.StringVar(&o.configsPath, "ci-operator-configs-path", "", "Path to ci-operator configurations.")
	fs.StringVar(&o.imageMirrorPath, "image-mirroring-path", "", "Path to image mirroring mapping files.")
	fs.StringVar(&o.dgraphAddress, "graphql-endpoint-address", "", "Address of the Dgraph's graphql endpoint.")

	if err := fs.Parse(os.Args[1:]); err != nil {
		logrus.WithError(err).Fatalf("cannot parse args: '%s'", os.Args[1:])
	}
	return o
}

func main() {
	o := parseOptions()
	if err := o.validate(); err != nil {
		logrus.WithError(err).Fatal("couldn't validate options")
	}
	graphqlClient := graphql.NewClient(o.dgraphAddress, http.DefaultClient)

	operator := imagegraphgenerator.NewOperator(graphqlClient)

	if err := operator.Load(); err != nil {
		logrus.WithError(err).Fatal("couldn't load operator")
	}

	if err := operator.UpdateMirrorMappings(o.imageMirrorPath); err != nil {
		logrus.WithError(err).Fatal("couldn't update mirrored images")
	}

	callback := func(c *api.ReleaseBuildConfiguration, i *config.Info) error {
		if i.Org == "openshift-priv" {
			return nil
		}

		if err := operator.AddBranchRef(i.Org, i.Repo, i.Branch); err != nil {
			return err
		}
		branchID := operator.Branches()[fmt.Sprintf("%s/%s:%s", i.Org, i.Repo, i.Branch)]

		if c.PromotionConfiguration == nil {
			return nil
		}

		var errs []error
		for _, image := range c.BaseImages {
			if err := operator.UpdateBaseImage(image); err != nil {
				errs = append(errs, err)
			}
		}

		for _, image := range c.Images {
			if err := operator.UpdateImage(image, c, branchID); err != nil {
				errs = append(errs, err)
			}
		}
		return utilerrors.NewAggregate(errs)
	}

	if err := config.OperateOnCIOperatorConfigDir(o.configsPath, callback); err != nil {
		logrus.WithError(err).Fatal("error while operating in ci-operator configuration files")
	}
}
