package multiarchbuildconfig

import (
	"bytes"
	"fmt"
	"os/exec"
	"sort"

	"github.com/sirupsen/logrus"

	"k8s.io/apimachinery/pkg/util/sets"
)

// imageMirrorer is the interface that wraps mirror.
//
// mirror mirrors a source image to some destinations, in
// accordance with what set in images.
type imageMirrorer interface {
	mirror(images []string) error
}

type ocImage struct {
	log            *logrus.Entry
	registryConfig string
}

// mirror invokes `oc image mirror` in a new process in order to mirror images
// using credentials in registryConfig
func (oci *ocImage) mirror(images []string) error {
	args := append([]string{
		"image",
		"mirror",
		// When the source is image-registry.openshift-image-registry.svc:5000 the oc client
		// cannot validate the certificate, this flag is required then
		"--insecure=true",
		"--keep-manifest-list=true",
		"--registry-config=" + oci.registryConfig,
	}, images...)

	cmd := exec.Command("oc", args...)

	cmdOutput := &bytes.Buffer{}
	cmdError := &bytes.Buffer{}
	cmd.Stdout = cmdOutput
	cmd.Stderr = cmdError

	oci.log.Debugf("Running command: %s", cmd.String())
	err := cmd.Run()
	if err != nil {
		oci.log.WithError(err).
			WithField("output", cmdOutput.String()).
			WithField("error_output", cmdError.String()).
			Error("oc command failed")
		return err
	}
	oci.log.WithField("output", cmdOutput.String()).Debug("oc command succeeded")
	return nil
}

// Prepare the arguments for the command `oc image mirror`. Mirror src to each location
// specified in dst; duplicate locations will be removed.
// Example:
// image-registry.openshift-image-registry.svc:5000/ns/image:tag dst[0]/ns/image:tag dst[1]/ns/image:tag ... dst[n]/ns/image:tag
func ocImageMirrorArgs(targetImageRef string, externalRegistries []string) []string {
	destinations := sets.New[string]()
	for _, externalRegistry := range externalRegistries {
		destinations.Insert(fmt.Sprintf("%s/%s", externalRegistry, targetImageRef))
	}

	destinationsList := destinations.UnsortedList()
	sort.Strings(destinationsList)

	return append([]string{fmt.Sprintf("%s/%s", registryURL, targetImageRef)}, destinationsList...)
}
