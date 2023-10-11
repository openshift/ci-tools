package secretgenerator

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/getlantern/deepcopy"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"sigs.k8s.io/yaml"

	"github.com/openshift/ci-tools/pkg/util/gzip"
)

func LoadConfigFromPath(path string) (Config, error) {
	var config Config
	cfgBytes, err := gzip.ReadFileMaybeGZIP(path)
	if err != nil {
		return nil, err
	}
	if err := yaml.Unmarshal(cfgBytes, &config); err != nil {
		return nil, err
	}
	return config, nil
}

type Config []SecretItem

func (c *Config) UnmarshalJSON(data []byte) error {
	var errs []error
	var newConfig Config

	var config []SecretItem
	if err := json.Unmarshal(data, &config); err != nil {
		return err
	}

	for _, si := range config {
		items, err := si.generateItemsFromParams()
		if err != nil {
			errs = append(errs, err)
		}
		newConfig = append(newConfig, items...)
	}
	*c = newConfig

	return utilerrors.NewAggregate(errs)
}

func (c Config) itemsByName() map[string][]SecretItem {
	byName := make(map[string][]SecretItem)

	for _, secretItem := range c {
		byName[secretItem.ItemName] = append(byName[secretItem.ItemName], secretItem)
	}

	return byName
}

func (c Config) IsItemGenerated(name string) bool {
	_, ok := c.itemsByName()[name]
	return ok
}

func (c Config) IsFieldGenerated(name, component string) bool {
	byName := c.itemsByName()
	if _, ok := byName[name]; !ok {
		return false
	}

	for _, item := range byName[name] {
		for _, field := range item.Fields {
			if component == field.Name {
				return true
			}
		}
	}
	return false
}

type FieldGenerator struct {
	Name    string `json:"name,omitempty"`
	Cmd     string `json:"cmd,omitempty"`
	Cluster string `json:"-"`
}

type SecretItem struct {
	ItemName string              `json:"item_name"`
	Fields   []FieldGenerator    `json:"fields,omitempty"`
	Notes    string              `json:"notes,omitempty"`
	Params   map[string][]string `json:"params,omitempty"`
}

func (si SecretItem) generateItemsFromParams() ([]SecretItem, error) {
	var errs []error
	var processedBwItems []SecretItem

	replaceParameter := func(paramName, param, template string) string {
		return strings.ReplaceAll(template, fmt.Sprintf("$(%s)", paramName), param)
	}

	itemsProcessingHolder := []SecretItem{si}
	for paramName, params := range si.Params {
		itemsProcessed := []SecretItem{}
		for _, qItem := range itemsProcessingHolder {
			for _, param := range params {
				argItem := SecretItem{}
				err := deepcopy.Copy(&argItem, &qItem)
				if err != nil {
					errs = append(errs, fmt.Errorf("error copying item %v: %w", si, err))
				}
				argItem.ItemName = replaceParameter(paramName, param, argItem.ItemName)
				for i, field := range argItem.Fields {
					argItem.Fields[i].Name = replaceParameter(paramName, param, field.Name)
					argItem.Fields[i].Cmd = replaceParameter(paramName, param, field.Cmd)
					if paramName == "cluster" {
						argItem.Fields[i].Cluster = param
					}
				}
				argItem.Notes = replaceParameter(paramName, param, argItem.Notes)
				itemsProcessed = append(itemsProcessed, argItem)
			}
		}
		itemsProcessingHolder = itemsProcessed
	}
	if len(errs) == 0 {
		processedBwItems = append(processedBwItems, itemsProcessingHolder...)
	}

	return processedBwItems, utilerrors.NewAggregate(errs)
}
