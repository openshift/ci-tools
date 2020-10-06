package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/prowjob"
	"github.com/openshift/ci-tools/pkg/steps/utils"

	"github.com/openshift/ci-tools/pkg/steps"
	"github.com/sirupsen/logrus"
	"github.com/spf13/afero"
	"k8s.io/test-infra/prow/interrupts"
)

const (
	bundleImageRefOption       = "bundle-image-ref"
	channelOption              = "channel"
	indexImageRefOption        = "index-image-ref"
	installNamespaceOption     = "install-namespace"
	ocpVersionOption           = "ocp-version"
	operatorPackageNameOptions = "operator-package-name"
	releaseImageRefOption      = "release-image-ref"
	targetNamespacesOption     = "target-namespaces"
)

type options struct {
	bundleImageRef      string
	channel             string
	indexImageRef       string
	installNamespace    string
	ocpVersion          string
	operatorPackageName string
	releaseImageRef     string
	targetNamespaces    string
	prowjob.ProwJobOptions
}

var fileSystem = afero.NewOsFs()
var fs = flag.NewFlagSet(os.Args[0], flag.ExitOnError)
var o options

//var prowJobArtifactsURL string

// gatherOptions binds flag entries to entries in the options struct
func (o *options) gatherOptions() {
	fs.StringVar(&o.bundleImageRef, bundleImageRefOption, "", "URL for the bundle image")
	fs.StringVar(&o.channel, channelOption, "", "The channel to test")
	fs.StringVar(&o.indexImageRef, indexImageRefOption, "", "URL for the index image")
	fs.StringVar(&o.installNamespace, installNamespaceOption, "", "namespace into which the operator and catalog will be installed. If empty, a new namespace will be created.")
	fs.StringVar(&o.ocpVersion, ocpVersionOption, "", "Version of OCP to use. Version must be 4.x or higher")
	fs.StringVar(&o.operatorPackageName, operatorPackageNameOptions, "", "Operator package name to test")
	fs.StringVar(&o.releaseImageRef, releaseImageRefOption, "", "Pull spec of a specific release payload image used for OCP deployment.")
	fs.StringVar(&o.targetNamespaces, targetNamespacesOption, "", "A comma-separated list of namespaces the operator will target. If empty, all namespaces are targeted")
	o.ProwJobOptions.AddFlags(fs)
}

// validateOptions ensures that all required flag options are
// present and validates any constraints on appropriate values
func (o options) validateOptions(fileSystem afero.Fs) error {

	if o.bundleImageRef == "" {
		return fmt.Errorf("required parameter %s was not provided", bundleImageRefOption)
	}

	if o.channel == "" {
		return fmt.Errorf("required parameter %s was not provided", channelOption)
	}

	if o.indexImageRef == "" {
		return fmt.Errorf("required parameter %s was not provided", indexImageRefOption)
	}
	if o.ocpVersion == "" {
		return fmt.Errorf("required parameter %s was not provided", ocpVersionOption)
	}
	if !strings.HasPrefix(o.ocpVersion, "4") {
		return fmt.Errorf("ocp-version must be 4.x or higher")
	}

	if o.operatorPackageName == "" {
		return fmt.Errorf("required parameter %s was not provided", operatorPackageNameOptions)
	}
	return o.ProwJobOptions.ValidateOptions(fileSystem)
}

func main() {
	o.gatherOptions()
	err := fs.Parse(os.Args[1:])
	if err != nil {
		logrus.WithError(err).Fatal("error parsing flag set")
	}

	err = o.validateOptions(fileSystem)
	if err != nil {
		logrus.WithError(err).Fatal("incorrect options")
	}

	go func() {
		interrupts.WaitForGracefulShutdown()
		os.Exit(1)
	}()

	// Add flag values to inject as ENV var entries in the prowjob configuration
	envVars := map[string]string{
		"BUNDLE_IMAGE":  o.bundleImageRef,
		"OCP_VERSION":   o.ocpVersion,
		"CLUSTER_TYPE":  "aws",
		steps.OOIndex:   o.indexImageRef,
		steps.OOPackage: o.operatorPackageName,
		steps.OOChannel: o.channel,
	}
	if o.releaseImageRef != "" {
		envVars[utils.ReleaseImageEnv(api.LatestReleaseName)] = o.releaseImageRef
	}
	if o.installNamespace != "" {
		envVars[steps.OOInstallNamespace] = o.installNamespace
	}
	if o.targetNamespaces != "" {
		envVars[steps.OOTargetNamespaces] = o.targetNamespaces
	}

	if err = o.ProwJobOptions.SubmitJobAndWatchResults(envVars, fileSystem); err != nil {
		logrus.WithError(err).Fatal("failed while submitting job or watching its result")
	}
}
