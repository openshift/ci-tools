package config

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/sirupsen/logrus"

	templateapi "github.com/openshift/api/template/v1"
	templatescheme "github.com/openshift/client-go/template/clientset/versioned/scheme"

	"k8s.io/api/core/v1"

	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime/serializer/json"
	kutilerrors "k8s.io/apimachinery/pkg/util/errors"

	"k8s.io/client-go/kubernetes/scheme"
	corev1 "k8s.io/client-go/kubernetes/typed/core/v1"
)

// CiTemplates is a map of all the changed templates
type CiTemplates map[string]*templateapi.Template

const (
	createByRehearse  = "created-by-pj-rehearse"
	rehearseLabelPull = "ci.openshift.org/rehearse-pull"
)

func getTemplates(templatePath string) (CiTemplates, error) {
	templates := make(map[string]*templateapi.Template)
	err := filepath.Walk(templatePath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return fmt.Errorf("prevent panic by handling failure accessing a path %q: %v", path, err)
		}

		if isYAML(path, info) {
			contents, err := ioutil.ReadFile(path)
			if err != nil {
				return fmt.Errorf("could not read file %s for template: %v", path, err)
			}

			if obj, _, err := templatescheme.Codecs.UniversalDeserializer().Decode(contents, nil, nil); err == nil {
				if template, ok := obj.(*templateapi.Template); ok {
					if len(template.Name) == 0 {
						template.Name = filepath.Base(path)
						template.Name = strings.TrimSuffix(template.Name, filepath.Ext(template.Name))
					}
					templates[filepath.Base(path)] = template
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("error walking the path %q: %v", templatePath, err)
	}
	return templates, nil
}

func isYAML(file string, info os.FileInfo) bool {
	return !info.IsDir() && (filepath.Ext(file) == ".yaml" || filepath.Ext(file) == ".yml")
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

// CreateCMTemplates creates configMaps for all the changed templates.
func (c *TemplateCMManager) CreateCMTemplates() error {
	var errors []error
	s := json.NewYAMLSerializer(json.DefaultMetaFactory, scheme.Scheme, scheme.Scheme)

	for filename, template := range c.templates {
		buf := new(bytes.Buffer)

		err := s.Encode(template, buf)
		if err != nil {
			errors = append(errors, err)
		}

		cmName := getTempCMName(c.prNumber, template.Name)
		cm := &v1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name: cmName,
				Labels: map[string]string{
					createByRehearse:  "true",
					rehearseLabelPull: strconv.Itoa(c.prNumber),
				},
			},
			Data: map[string]string{filename: buf.String()},
		}

		c.logger.WithFields(logrus.Fields{"template-name": template.Name, "cm-name": cmName}).Info("creating rehearsal configMap for template")
		if _, err := c.cmclient.Create(cm); err != nil {
			if kerrors.IsAlreadyExists(err) {
				if _, err := c.cmclient.Update(cm); err != nil {
					errors = append(errors, fmt.Errorf("could not update existing configmap: %v", err))
				}
			} else {
				errors = append(errors, err)
			}
		}
	}
	return kutilerrors.NewAggregate(errors)
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

func getTempCMName(prNumber int, templateName string) string {
	return fmt.Sprintf("rehearse-%d-%s", prNumber, templateName)
}
