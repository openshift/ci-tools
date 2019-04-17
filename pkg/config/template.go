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
	cmclient  corev1.ConfigMapInterface
	prNumber  int
	logger    *logrus.Entry
	templates CiTemplates
}

// NewTemplateCMManager creates a new TemplateCMManager
func NewTemplateCMManager(cmclient corev1.ConfigMapInterface, prNumber int, logger *logrus.Entry, templates CiTemplates) *TemplateCMManager {
	return &TemplateCMManager{
		cmclient:  cmclient,
		prNumber:  prNumber,
		logger:    logger,
		templates: templates,
	}
}

func (c *TemplateCMManager) createCM(cm *v1.ConfigMap) error {
	if cm.ObjectMeta.Labels == nil {
		cm.ObjectMeta.Labels = map[string]string{}
	}
	cm.ObjectMeta.Labels[createByRehearse] = "true"
	cm.ObjectMeta.Labels[rehearseLabelPull] = strconv.Itoa(c.prNumber)
	if _, err := c.cmclient.Create(cm); err != nil && !kerrors.IsAlreadyExists(err) {
		return err
	}
	return nil
}

// CreateCMTemplates creates configMaps for all the changed templates.
func (c *TemplateCMManager) CreateCMTemplates() error {
	var errors []error
	for filename, template := range c.templates {
		templateName := GetTemplateName(filename)
		cmName := GetTempCMName(templateName, filename, template)
		cm := &v1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: cmName},
			Data:       map[string]string{filename: string(template)},
		}

		c.logger.WithFields(logrus.Fields{"template-name": templateName, "cm-name": cmName}).Info("creating rehearsal configMap for template")
		if err := c.createCM(cm); err != nil {
			errors = append(errors, err)
		}
	}
	return kutilerrors.NewAggregate(errors)
}

func (c *TemplateCMManager) CreateClusterProfiles(dir string, profiles []ClusterProfile) error {
	var errs []error
	for _, p := range profiles {
		cm, err := genClusterProfileCM(dir, p)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		c.logger.WithFields(logrus.Fields{"cluster-profile": cm.ObjectMeta.Name}).Info("creating rehearsal cluster profile ConfigMap")
		if err := c.createCM(cm); err != nil {
			errs = append(errs, err)
		}
	}
	return kutilerrors.NewAggregate(errs)
}

func genClusterProfileCM(dir string, profile ClusterProfile) (*v1.ConfigMap, error) {
	ret := &v1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: profile.CMName()},
		Data:       map[string]string{},
	}
	profilePath := filepath.Join(dir, profile.Name)
	err := filepath.Walk(profilePath, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		b, err := ioutil.ReadFile(path)
		if err != nil {
			return err
		}
		ret.Data[filepath.Base(path)] = string(b)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return ret, nil
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
