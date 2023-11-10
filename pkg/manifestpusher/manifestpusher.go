package manifestpusher

import (
	"bytes"
	"fmt"
	"os/exec"
	"time"

	"github.com/sirupsen/logrus"

	"k8s.io/apimachinery/pkg/util/wait"

	buildv1 "github.com/openshift/api/build/v1"
)

const (
	nodeArchitectureLabel = "kubernetes.io/arch"
)

type ManifestPusher interface {
	PushImageWithManifest(builds []buildv1.Build, targetImageRef string) error
}

func NewManifestPushfer(logger *logrus.Entry, registryURL string, dockercfgPath string) ManifestPusher {
	return &manifestPusher{
		logger:        logger,
		registryURL:   registryURL,
		dockercfgPath: dockercfgPath,
	}
}

type manifestPusher struct {
	logger        *logrus.Entry
	registryURL   string
	dockercfgPath string
}

// pushOutputImageWithManifest constructs a manifest-tool command to create and push a new image with all images that we built
// in the manifest list based on their architecture.
//
// Example command:
// /usr/bin/manifest-tool push from-args \
// --platforms linux/amd64 --template registry.multi-build01.arm-build.devcluster.openshift.com/ci/managed-clonerefs:latest-amd64 \
// --platforms linux/arm64 --template registry.multi-build01.arm-build.devcluster.openshift.com/ci/managed-clonerefs:latest-arm64 \
// --target registry.multi-build01.arm-build.devcluster.openshift.com/ci/managed-clonerefs:latest
func (m manifestPusher) PushImageWithManifest(builds []buildv1.Build, targetImageRef string) error {
	return wait.ExponentialBackoff(wait.Backoff{
		Steps:    5,
		Duration: 20 * time.Second,
		Factor:   2.0,
		Jitter:   0.1,
	}, func() (bool, error) {
		args := []string{
			"--debug",
			"--insecure",
			"--docker-cfg", m.dockercfgPath,
			"push", "from-args",
		}
		for i := range builds {
			build := &builds[i]
			args = append(args, []string{
				"--platforms",
				fmt.Sprintf("linux/%s", build.Spec.NodeSelector[nodeArchitectureLabel]),
				"--template",
				fmt.Sprintf("%s/%s/%s", m.registryURL, build.Spec.Output.To.Namespace, build.Spec.Output.To.Name),
			}...)
		}

		args = append(args, []string{
			"--target",
			fmt.Sprintf("%s/%s", m.registryURL, targetImageRef),
		}...)

		cmd := exec.Command("manifest-tool", args...)

		cmdOutput := &bytes.Buffer{}
		cmdError := &bytes.Buffer{}
		cmd.Stdout = cmdOutput
		cmd.Stderr = cmdError

		m.logger.Debugf("Running command: %s", cmd.String())
		err := cmd.Run()
		if err != nil {
			m.logger.WithError(err).WithField("output", cmdOutput.String()).WithField("error_output", cmdError.String()).Error("manifest-tool command failed")
			return false, nil
		}
		m.logger.WithField("output", cmdOutput.String()).Debug("manifest-tool command succeeded")

		m.logger.Infof("Image %s created", targetImageRef)
		return true, nil
	})
}
