package api

import (
	"io/ioutil"
	"os"
	"path/filepath"

	"github.com/sirupsen/logrus"

	"k8s.io/test-infra/prow/secretutil"
)

// prowArtifactsEnv is the directory Prow wants us to put artifacts into for upload
const prowArtifactsEnv string = "ARTIFACTS"

func Artifacts() (string, bool) {
	return os.LookupEnv(prowArtifactsEnv)
}

// SaveArtifact saves the data under the path relative to the artifact directory.
// If no artifact directory is set, we no-op.
func SaveArtifact(censor secretutil.Censorer, relPath string, data []byte) error {
	artifactDir, set := os.LookupEnv(prowArtifactsEnv)
	if !set {
		return nil
	}
	censor.Censor(&data)
	path := filepath.Join(artifactDir, relPath)
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0777); err != nil {
		logrus.WithError(err).Warn("Unable to create artifact directory.")
		return err
	}
	if err := ioutil.WriteFile(path, data, 0644); err != nil {
		logrus.WithError(err).Errorf("Failed to write %s", relPath)
		return err
	}
	return nil
}
