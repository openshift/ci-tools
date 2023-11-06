package multiarchbuildconfig

import (
	"bytes"
	"os/exec"

	"github.com/sirupsen/logrus"
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
// Example: src dst[0] dst[1] ... dst[n]
func ocImageMirrorArgs(src string, dst []string) []string {
	if len(dst) == 0 {
		return []string{}
	}
	duplicates := make(map[string]struct{})
	noDup := make([]string, 0, len(dst))
	// Avoid searching for duplicates using a Set as it won't preserve the same
	// order of dst
	for _, d := range dst {
		if _, exists := duplicates[d]; !exists {
			duplicates[d] = struct{}{}
			noDup = append(noDup, d)
		}
	}
	return append([]string{src}, noDup...)
}
