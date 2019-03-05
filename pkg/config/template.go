package config

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"

	templateapi "github.com/openshift/api/template/v1"
	templatescheme "github.com/openshift/client-go/template/clientset/versioned/scheme"
)

type CiTemplates map[string]*templateapi.Template

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
