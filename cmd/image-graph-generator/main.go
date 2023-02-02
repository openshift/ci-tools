package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"

	graphql "github.com/shurcooL/graphql"
	"github.com/sirupsen/logrus"

	imagegraphgenerator "github.com/openshift/ci-tools/pkg/image-graph-generator"
)

type options struct {
	releaseRepoPath string
	dgraphAddress   string
}

func (o options) validate() error {
	if o.dgraphAddress == "" {
		return fmt.Errorf("--graphql-endpoint-address is not specified")
	}
	if o.releaseRepoPath == "" {
		return fmt.Errorf("--release-repo is not specified")
	}

	return nil
}

func parseOptions() options {
	var o options
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	fs.StringVar(&o.releaseRepoPath, "release-repo", "", "Path to the openshift/release repository.")
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

	operator := imagegraphgenerator.NewOperator(graphqlClient, o.releaseRepoPath)

	if err := operator.Load(); err != nil {
		logrus.WithError(err).Fatal("couldn't load operator")
	}

	if err := operator.UpdateMirrorMappings(); err != nil {
		logrus.WithError(err).Fatal("couldn't update mirrored images")
	}
	if err := operator.AddManifestImages(); err != nil {
		logrus.WithError(err).Fatal("couldn't update images from manifests")
	}

	if err := operator.OperateOnCIOperatorConfigs(); err != nil {
		logrus.WithError(err).Fatal("error while operating in ci-operator configuration files")
	}
}
