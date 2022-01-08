package main

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"

	"github.com/sirupsen/logrus"

	v1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/kubernetes/scheme"

	templatev1 "github.com/openshift/api/template/v1"
	userv1 "github.com/openshift/api/user/v1"
)

type yamlGroupCollector struct {
	decoder          runtime.Decoder
	validateSubjects bool
}

const yamlSeparator = "\n---"

func (c *yamlGroupCollector) collect(dir string) (sets.String, error) {
	groups := sets.NewString()
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("failed to determine absolute path for dir %s: %w", dir, err)
	}
	if err := filepath.Walk(abs,
		func(path string, info os.FileInfo, err error) error {
			if err != nil {
				logrus.WithField("source-file", path).WithError(err).Error("Failed to walk release controller mirror config dir")
				return err
			}

			if skip, err := fileFilter(info, path); skip || err != nil {
				return err
			}

			data, err := ioutil.ReadFile(path)
			if err != nil {
				logrus.WithField("source-file", path).WithError(err).Error("Failed to read file")
				return err
			}
			for _, yamlDoc := range bytes.Split(data, []byte(yamlSeparator)) {
				if onlyComments(yamlDoc) {
					continue
				}
				collected, err := c.collectGroups(yamlDoc, path, false)
				if err != nil {
					return err
				}
				groups = groups.Union(collected)
			}
			return nil
		}); err != nil {
		return nil, fmt.Errorf("failed to walk dir: %w", err)

	}
	return groups, nil
}

type templateProcessErr struct {
	msg string
}

func (e *templateProcessErr) Error() string {
	return e.msg
}

func isTemplateProcessErr(err error) bool {
	if err == nil {
		return false
	}
	_, ok := err.(*templateProcessErr)
	return ok
}

func (c *yamlGroupCollector) collectGroups(doc []byte, path string, isTemplateObject bool) (sets.String, error) {
	groups := sets.NewString()
	obj, _, err := c.decoder.Decode(doc, nil, nil)
	if err != nil {
		if runtime.IsNotRegisteredError(err) && (!c.validateSubjects ||
			!strings.HasPrefix(err.Error(), "no kind \"Group\" is registered for version \"v1\"")) {
			return groups, nil
		}
		if isTemplateObject {
			err = &templateProcessErr{msg: err.Error()}
		}
		logrus.WithField("source-file", path).WithError(err).Error("Failed to decode yaml file")
		return nil, err
	}
	switch o := obj.(type) {
	case *userv1.Group:
		if c.validateSubjects {
			return nil, fmt.Errorf("cannot create any group but found: %s", o.Name)
		}
	case *rbacv1.RoleBinding:
		collected, err := processSubjects(o.Subjects, o.Name, "RoleBinding", c.validateSubjects)
		if err != nil {
			return nil, err
		}
		if collected.Len() > 0 {
			logrus.WithField("collected", collected.List()).WithField("path", path).Debug("Collected groups")
		}
		groups = groups.Union(collected)
	case *rbacv1.ClusterRoleBinding:
		collected, err := processSubjects(o.Subjects, o.Name, "ClusterRoleBinding", c.validateSubjects)
		if err != nil {
			return nil, err
		}
		groups = groups.Union(collected)
	case *v1.List:
		for _, item := range o.Items {
			collected, err := c.collectGroups(item.Raw, path, false)
			if err != nil {
				if runtime.IsNotRegisteredError(err) && (!c.validateSubjects ||
					!strings.HasPrefix(err.Error(), "no kind \"Group\" is registered for version \"v1\"")) {
					continue
				}
				logrus.WithField("source-file", path).WithError(err).Error("Failed to decode sub-object in list")
				return groups, err
			}
			groups = groups.Union(collected)
		}
	case *templatev1.Template:
		for _, object := range o.Objects {
			collected, err := c.collectGroups(object.Raw, path, true)
			if err != nil {
				if runtime.IsNotRegisteredError(err) && (!c.validateSubjects ||
					!strings.HasPrefix(err.Error(), "no kind \"Group\" is registered for version \"v1\"")) {
					continue
				}
				// Trying to avoid reinventing oc-process
				// We might miss some groups but users can make RBACs out of templates
				// Most commonly, it is because of `replicas: ${{REPLICAS}}`
				logrus.WithField("source-file", path).WithError(err).Warning("Failed to decode sub-object in template")
				if isTemplateProcessErr(err) {
					continue
				}
				return groups, err
			}
			groups = groups.Union(collected)
		}
	}
	return groups, nil
}

func processSubjects(subjects []rbacv1.Subject, name, t string, validateSubjects bool) (sets.String, error) {
	groups := sets.NewString()
	for _, s := range subjects {
		if validateSubjects && s.Kind == "User" {
			return nil, fmt.Errorf("cannot use User as subject in %s: %s", t, name)
		}
		if s.Kind == "Group" {
			if strings.HasPrefix(s.Name, "system:") {
				continue
			}
			if strings.Contains(s.Name, "${") {
				return nil, fmt.Errorf("cannot use ${ in a subject of %s: %s", t, name)
			}
			groups.Insert(s.Name)
		}
	}
	return groups, nil
}

func onlyComments(yaml []byte) bool {
	for _, line := range bytes.Split(yaml, []byte("\n")) {
		if bytes.Equal(bytes.TrimSpace(line), []byte("")) {
			continue
		}
		if !bytes.HasPrefix(line, []byte("#")) {
			return false
		}
	}
	return true
}

func newYamlGroupCollector(validateSubjects bool) groupCollector {
	decoder := serializer.NewCodecFactory(scheme.Scheme).UniversalDeserializer()
	return &yamlGroupCollector{decoder: decoder, validateSubjects: validateSubjects}
}

func fileFilter(info os.FileInfo, path string) (bool, error) {
	if info.Mode()&os.ModeSymlink != 0 {
		logrus.Infof("ingore the symlink: %s", path)
		return true, nil
	}

	if info.IsDir() {
		if strings.HasPrefix(info.Name(), "_") {
			logrus.Infof("Skipping directory: %s", path)
			return false, filepath.SkipDir
		}
		logrus.Infof("walk file in directory: %s", path)
		return true, nil
	}

	if filepath.Ext(info.Name()) != ".yaml" {
		return true, nil
	}

	if strings.HasPrefix(info.Name(), "_") {
		return true, nil
	}

	return false, nil
}
