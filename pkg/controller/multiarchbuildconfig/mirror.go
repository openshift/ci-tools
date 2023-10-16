package multiarchbuildconfig

import (
	"bytes"
	"fmt"
	"os/exec"

	v1 "github.com/openshift/ci-tools/pkg/api/multiarchbuildconfig/v1"
	"github.com/sirupsen/logrus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
)

const (
	// Condition types
	MirrorImageManifestDone string = "ImageMirrorDone"

	// Condition reasons
	ImageMirrorErrorReason   string = "ImageMirrorError"
	ImageMirrorSuccessReason string = "ImageMirrorSuccess"
)

// handleMirrorImage pushes an image to the locations specified in .spec.output.to. The function requires an image
// to have been already created and pushed to the local registry.
func (r *reconciler) handleMirrorImage(srcImage string, mabc *v1.MultiArchBuildConfig) (func(mabcToMutate *v1.MultiArchBuildConfig), bool) {
	if len(mabc.Spec.Output.To) == 0 {
		return nil, true
	}

	// If the condition exists then we assume the mirror pod has already run
	// so we don't need to do anything else
	if getCondition(mabc, MirrorImageManifestDone) != nil {
		return nil, true
	}

	imageMirrorArgs := ocImageMirrorArgs(srcImage, &mabc.Spec.Output)
	if err := r.mirrorImagesFn(r.logger, r.dockerConfigPath, imageMirrorArgs); err != nil {
		return func(mabcToMutate *v1.MultiArchBuildConfig) {
			setCondition(mabcToMutate, &metav1.Condition{
				Type:               MirrorImageManifestDone,
				Status:             metav1.ConditionFalse,
				LastTransitionTime: metav1.Time{Time: r.timeNowFn()},
				Reason:             ImageMirrorErrorReason,
				Message:            fmt.Sprintf("oc image mirror: %s", err),
			})
		}, false
	}

	return func(mabcToMutate *v1.MultiArchBuildConfig) {
		setCondition(mabcToMutate, &metav1.Condition{
			Type:               MirrorImageManifestDone,
			Status:             metav1.ConditionTrue,
			LastTransitionTime: metav1.Time{Time: r.timeNowFn()},
			Reason:             ImageMirrorSuccessReason,
		})
	}, true
}

// mirrorImages invokes `oc image mirror` in a new process in order to mirror images
// using credentials in registryConfig
func mirrorImages(log *logrus.Entry, registryConfig string, images []string) error {
	args := append([]string{
		"image",
		"mirror",
		// When the source is image-registry.openshift-image-registry.svc:5000 the oc client
		// cannot validate the certificate, this flag is required then
		"--insecure=true",
		"--keep-manifest-list=true",
		"--registry-config=" + registryConfig,
	}, images...)

	cmd := exec.Command("oc", args...)

	cmdOutput := &bytes.Buffer{}
	cmdError := &bytes.Buffer{}
	cmd.Stdout = cmdOutput
	cmd.Stderr = cmdError

	log.Debugf("Running command: %s", cmd.String())
	err := cmd.Run()
	if err != nil {
		log.WithError(err).
			WithField("output", cmdOutput.String()).
			WithField("error_output", cmdError.String()).
			Error("oc command failed")
		return err
	}
	log.WithField("output", cmdOutput.String()).Debug("oc command succeeded")
	return nil
}

// Prepare the arguments for the command `oc image mirror`. Mirror src to each location
// specified in output.to; duplicate locations will be removed.
// Example: src=output.to[0] src=output.to[1] ... src=output.to[n]
func ocImageMirrorArgs(src string, output *v1.MultiArchBuildConfigOutput) []string {
	if len(output.To) == 0 {
		return []string{}
	}
	noDup := sets.NewString(output.To...).List()
	return append([]string{src}, noDup...)
}
