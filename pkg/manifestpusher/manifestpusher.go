package manifestpusher

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"

	"github.com/sirupsen/logrus"

	buildv1 "github.com/openshift/api/build/v1"
)

const (
	nodeArchitectureLabel = "kubernetes.io/arch"
)

type ManifestPusher interface {
	PushImageWithManifest(builds map[string]*buildv1.Build, targetImageRef string) error
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
func (m manifestPusher) PushImageWithManifest(builds map[string]*buildv1.Build, targetImageRef string) error {
	args := []string{
		"--debug",
		"--insecure",
		"--docker-cfg", m.dockercfgPath,
		"push", "from-args",
	}
	for _, build := range builds {
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
		return err
	}
	m.logger.WithField("output", cmdOutput.String()).Debug("manifest-tool command succeeded")

	m.logger.Infof("Image %s created", targetImageRef)
	return nil
}

func CreateDockerCfg(tokenPath, registryURL, dest string) error {
	token, err := os.ReadFile(tokenPath)
	if err != nil {
		return fmt.Errorf("error reading token: %w", err)
	}

	authString := base64.StdEncoding.EncodeToString([]byte("serviceaccount:" + string(token)))

	configContent := fmt.Sprintf(`{
		"auths": {
			"%s": {
				"auth": "%s"
			}
		}
	}`, registryURL, authString)

	if err = os.WriteFile(dest, []byte(configContent), 0755); err != nil {
		return fmt.Errorf("error writing %s: %w", dest, err)

	}

	return nil
}
