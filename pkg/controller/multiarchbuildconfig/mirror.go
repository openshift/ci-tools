package multiarchbuildconfig

import (
	"bytes"
	"fmt"
	"os/exec"
	"regexp"
	"sort"
	"strings"

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
// image-registry.openshift-image-registry.svc:5000/ns/image:tag dst[0]/ns/image:tag dst[1]/ns/image:tag dst[2].. dst[n]/image:tag
// Registres can be just a hostname, hostname + ns, hostname + project + image:tag
func ocImageMirrorArgs(targetImageRef string, externalRegistries []string) ([]string, error) {
	destinations := sets.New[string]()
	re := regexp.MustCompile(`([\.a-z]+)/?(\w+)?/?(\w+)?:?(\w+)?`)
	for _, externalRegistry := range externalRegistries {
		pieces := removeEmpty(re.FindStringSubmatch(externalRegistry))
		piecesLen := len(pieces)
		// hostname only external registry generates something like ["quay.io", "quay.io"] substring output
		if piecesLen == 2 {
			destinations.Insert(fmt.Sprintf("%s/%s", externalRegistry, targetImageRef))
		} else {
			switch len(pieces) {
			case 3: // quay.io/ns
				imageRefPieces := strings.Split(targetImageRef, "/")
				destinations.Insert(fmt.Sprintf("%s/%s", externalRegistry, imageRefPieces[1]))
			case 4: // quay.io/ns/image
				imageRefPieces := strings.Split(targetImageRef, ":")
				destinations.Insert(fmt.Sprintf("%s:%s", externalRegistry, imageRefPieces[1]))
			case 5: // quay.io/ns/image:tag
				destinations.Insert(externalRegistry)
			default:
				return destinations.UnsortedList(), fmt.Errorf("External registry %s doesn't follow the expected pattern as <host>[/<ns>[/<image>[:<tag>]]]", externalRegistry)
			}
		}
	}

	destinationsList := destinations.UnsortedList()
	sort.Strings(destinationsList)

	return append([]string{fmt.Sprintf("%s/%s", registryURL, targetImageRef)}, destinationsList...), nil
}

func removeEmpty(values []string) []string {
	var normalized []string
	for _, v := range values {
		if v != "" {
			normalized = append(normalized, v)
		}
	}
	return normalized
}
