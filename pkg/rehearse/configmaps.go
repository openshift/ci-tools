package rehearse

import (
	"fmt"
	"io/ioutil"
	"path/filepath"

	"github.com/ghodss/yaml"
	"github.com/sirupsen/logrus"

	corev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	prowconfig "k8s.io/test-infra/prow/config"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Rehearsed Prow jobs may depend on ConfigMaps with content also modified by
// the tested PR. All ci-operator-based jobs use the `ci-operator-configs`
// ConfigMap that contains ci-operator configuration files. Rehearsed jobs
// need to have the PR-version of these files available. The following code
// takes care of creating a short-lived, rehearsal ConfigMap. The keys needed
// to be present are extracted from the rehearsal jobs and the rehearsal jobs
// are modified to use this ConfigMap instead of the "production" one.

var ciOperatorConfigsCMName = "ci-operator-configs"

type CIOperatorConfigs interface {
	FixupJob(job *prowconfig.Presubmit, repo string)
	Create() error
}

type reader interface {
	Read(configFilePath string) (string, error)
}

type fileReader struct{}

func (r *fileReader) Read(configFilePath string) (string, error) {
	content, err := ioutil.ReadFile(configFilePath)
	return string(content), err
}

type ciOperatorConfigs struct {
	reader

	cmclient  corev1.ConfigMapInterface
	prNumber  int
	configDir string

	logger logrus.FieldLogger
	dry    bool

	configMapName string
	neededConfigs map[string]string
}

func NewCIOperatorConfigs(cmclient corev1.ConfigMapInterface, prNumber int, configDir string, logger logrus.FieldLogger, dry bool) CIOperatorConfigs {
	name := fmt.Sprintf("rehearsal-ci-operator-configs-%d", prNumber)
	return &ciOperatorConfigs{
		reader:        &fileReader{},
		cmclient:      cmclient,
		prNumber:      prNumber,
		configDir:     configDir,
		logger:        logger.WithField("ciop-configs-cm", name),
		dry:           dry,
		configMapName: name,
		neededConfigs: map[string]string{},
	}
}

// If a job uses the `ci-operator-config` ConfigMap, save which key does it use
// from it and replace that ConfigMap reference with a reference to the
// temporary, rehearsal ConfigMap containing the necessary keys with content
// matching the version from tested PR
func (c *ciOperatorConfigs) FixupJob(job *prowconfig.Presubmit, repo string) {
	for _, container := range job.Spec.Containers {
		for _, env := range container.Env {
			if env.ValueFrom == nil {
				continue
			}
			if env.ValueFrom.ConfigMapKeyRef == nil {
				continue
			}
			if env.ValueFrom.ConfigMapKeyRef.Name == ciOperatorConfigsCMName {
				filename := env.ValueFrom.ConfigMapKeyRef.Key
				env.ValueFrom.ConfigMapKeyRef.Name = c.configMapName
				c.neededConfigs[filename] = filepath.Join(repo, filename)

				logFields := logrus.Fields{"ci-operator-config": filename, "rehearsal-job": job.Name}
				c.logger.WithFields(logFields).Info("Rehearsal job uses ci-operator config ConfigMap")
			}
		}
	}
}

// Create a rehearsal ConfigMap with ci-operator config files needed by the
// rehearsal jobs.
func (c *ciOperatorConfigs) Create() error {
	cm := &v1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: c.configMapName},
		Data:       map[string]string{},
	}
	c.logger.Debug("Preparing rehearsal ConfigMap for ci-operator configs")

	for key, path := range c.neededConfigs {
		c.logger.WithField("ciop-config", key).Info("Loading ci-operator config to rehearsal ConfigMap")
		fullPath := filepath.Join(c.configDir, path)

		var err error
		cm.Data[key], err = c.reader.Read(fullPath)
		if err != nil {
			return fmt.Errorf("failed to read ci-operator config file from %s: %v", fullPath, err)
		}
	}

	if c.dry {
		cmAsYAML, err := yaml.Marshal(cm)
		if err != nil {
			return fmt.Errorf("Failed to marshal ConfigMap to YAML: %v", err)
		}
		fmt.Printf("%s\n", cmAsYAML)
		return nil
	}

	c.logger.Info("Creating rehearsal ConfigMap for ci-operator configs")
	_, err := c.cmclient.Create(cm)
	return err
}
