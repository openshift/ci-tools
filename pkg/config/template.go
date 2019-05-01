package config

import (
	"crypto/sha256"
	"encoding/base32"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/sirupsen/logrus"

	"k8s.io/api/core/v1"

	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	kutilerrors "k8s.io/apimachinery/pkg/util/errors"

	corev1 "k8s.io/client-go/kubernetes/typed/core/v1"

	prowgithub "k8s.io/test-infra/prow/github"
	_ "k8s.io/test-infra/prow/hook"
	prowplugins "k8s.io/test-infra/prow/plugins"
	"k8s.io/test-infra/prow/plugins/updateconfig"
)

// CiTemplates is a map of all the changed templates
type CiTemplates map[string][]byte
type ClusterProfile struct {
	Name, TreeHash string
}

func (p ClusterProfile) CMName() string {
	return fmt.Sprintf("rehearse-cluster-profile-%s-%s", p.Name, p.TreeHash[:5])
}

const (
	createByRehearse  = "created-by-pj-rehearse"
	rehearseLabelPull = "ci.openshift.org/rehearse-pull"
)

func getTemplates(templatePath string) (CiTemplates, error) {
	templates := make(CiTemplates)
	err := filepath.Walk(templatePath, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !(strings.HasSuffix(path, ".yaml") || strings.HasSuffix(path, ".yml")) {
			return err
		}
		contents, err := ioutil.ReadFile(path)
		if err != nil {
			return fmt.Errorf("could not read template %q: %v", path, err)
		}
		templates[filepath.Base(path)] = contents
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("error walking the path %q: %v", templatePath, err)
	}
	return templates, nil
}

// TemplateCMManager holds the details needed for the configmap controller
type TemplateCMManager struct {
	cmclient         corev1.ConfigMapInterface
	configUpdaterCfg prowplugins.ConfigUpdater
	prNumber         int
	releaseRepoPath  string
	logger           *logrus.Entry
}

// NewTemplateCMManager creates a new TemplateCMManager
func NewTemplateCMManager(
	cmclient corev1.ConfigMapInterface,
	configUpdaterCfg prowplugins.ConfigUpdater,
	prNumber int,
	releaseRepoPath string,
	logger *logrus.Entry,
) *TemplateCMManager {
	return &TemplateCMManager{
		cmclient:         cmclient,
		configUpdaterCfg: configUpdaterCfg,
		prNumber:         prNumber,
		releaseRepoPath:  releaseRepoPath,
		logger:           logger,
	}
}

type osFileGetter struct {
	root string
}

func (g osFileGetter) GetFile(filename string) ([]byte, error) {
	return ioutil.ReadFile(filepath.Join(g.root, filename))
}

func (c *TemplateCMManager) createCM(name string, data []updateconfig.ConfigMapUpdate) error {
	cm := &v1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			Labels: map[string]string{
				createByRehearse:  "true",
				rehearseLabelPull: strconv.Itoa(c.prNumber),
			},
		},
		Data: map[string]string{},
	}
	if _, err := c.cmclient.Create(cm); err != nil && !kerrors.IsAlreadyExists(err) {
		return err
	} else if err := updateconfig.Update(osFileGetter{root: c.releaseRepoPath}, c.cmclient, cm.Name, cm.Namespace, data, c.logger); err != nil {
		return err
	}
	return nil
}

func genChanges(root, dir string) ([]prowgithub.PullRequestChange, error) {
	var ret []prowgithub.PullRequestChange
	err := filepath.Walk(filepath.Join(root, dir), func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		// Failure is impossible per filepath.Walk's API.
		path, err = filepath.Rel(root, path)
		if err != nil {
			return err
		}
		ret = append(ret, prowgithub.PullRequestChange{
			Filename: path,
			Status:   string(prowgithub.PullRequestFileModified),
		})
		return nil
	})
	return ret, err
}

func replaceSpecNames(cfg prowplugins.ConfigUpdater, mapping map[string]string) (ret prowplugins.ConfigUpdater) {
	ret = cfg
	ret.Maps = make(map[string]prowplugins.ConfigMapSpec, len(cfg.Maps))
	for k, v := range cfg.Maps {
		if name, ok := mapping[v.Name]; ok {
			v.Name = name
		}
		ret.Maps[k] = v
	}
	return
}

func (c *TemplateCMManager) createCMs(filenames []string, mapping map[string]string) error {
	var changes []prowgithub.PullRequestChange
	for _, f := range filenames {
		c, err := genChanges(c.releaseRepoPath, f)
		if err != nil {
			return err
		}
		changes = append(changes, c...)
	}
	var errs []error
	for cm, data := range updateconfig.FilterChanges(replaceSpecNames(c.configUpdaterCfg, mapping), changes, c.logger) {
		c.logger.WithFields(logrus.Fields{"cm-name": cm.Name}).Info("creating rehearsal configMap")
		if err := c.createCM(cm.Name, data); err != nil {
			errs = append(errs, err)
		}
	}
	return kutilerrors.NewAggregate(errs)
}

// CreateCMTemplates creates configMaps for all the changed templates.
func (c *TemplateCMManager) CreateCMTemplates(templates CiTemplates) error {
	filenames := make([]string, 0, len(templates))
	nameMap := make(map[string]string, len(templates))
	for filename, template := range templates {
		filenames = append(filenames, filepath.Join(TemplatesPath, filename))
		name := GetTemplateName(filename)
		nameMap["cluster-launch-"+name] = GetTempCMName(name, filename, template)
	}
	return c.createCMs(filenames, nameMap)
}

func (c *TemplateCMManager) CreateClusterProfiles(profiles []ClusterProfile) error {
	filenames := make([]string, 0, len(profiles))
	nameMap := make(map[string]string, len(profiles))
	for _, p := range profiles {
		filenames = append(filenames, filepath.Join(ClusterProfilesPath, p.Name))
		nameMap["cluster-profile-"+p.Name] = p.CMName()
	}
	return c.createCMs(filenames, nameMap)
}

// CleanupCMTemplates deletes all the configMaps that have been created for the changed templates.
func (c *TemplateCMManager) CleanupCMTemplates() error {
	c.logger.Info("deleting temporary template configMaps")
	if err := c.cmclient.DeleteCollection(&metav1.DeleteOptions{},
		metav1.ListOptions{LabelSelector: fields.Set{
			createByRehearse:  "true",
			rehearseLabelPull: strconv.Itoa(c.prNumber),
		}.AsSelector().String()}); err != nil {
		return err
	}
	return nil
}

func GetTempCMName(templateName, filename string, templateData []byte) string {
	return fmt.Sprintf("rehearse-%s-%s", inputHash([]byte(filename), templateData), templateName)
}

// oneWayEncoding can be used to encode hex to a 62-character set (0 and 1 are duplicates) for use in
// short display names that are safe for use in kubernetes as resource names.
var oneWayNameEncoding = base32.NewEncoding("bcdfghijklmnpqrstvwxyz0123456789").WithPadding(base32.NoPadding)

// inputHash returns a string that hashes the unique parts of the input to avoid collisions.
func inputHash(inputs ...[]byte) string {
	hash := sha256.New()

	// the inputs form a part of the hash
	for _, s := range inputs {
		hash.Write(s)
	}

	// Object names can't be too long so we truncate
	// the hash. This increases chances of collision
	// but we can tolerate it as our input space is
	// tiny.
	return oneWayNameEncoding.EncodeToString(hash.Sum(nil)[:5])
}

// GetTemplateName generates a name for the template based of the filename.
func GetTemplateName(filename string) string {
	templateName := filepath.Base(filename)
	return strings.TrimSuffix(templateName, filepath.Ext(templateName))
}
